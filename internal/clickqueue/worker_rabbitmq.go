package clickqueue

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

const (
	RetryExchangeName            = "shortq.analytics.retry"
	RetryQueueName               = "shortq.clicks.retry"
	RetryRoutingKey              = "click.retry"
	DefaultWorkerPrefetch        = 400
	DefaultWorkerShutdownTimeout = 10 * time.Second
	DefaultRetryPublishTimeout   = 2 * time.Second
	baseRetryDelay               = 250 * time.Millisecond
)

type RabbitMQWorker struct {
	rawURL          string
	store           BatchStore
	metrics         *WorkerMetrics
	config          WorkerConfig
	prefetch        int
	shutdownTimeout time.Duration
}

func NewRabbitMQWorker(rawURL string, store BatchStore, metrics *WorkerMetrics) (*RabbitMQWorker, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "amqp" && parsed.Scheme != "amqps") || parsed.Host == "" {
		return nil, errors.New("invalid RabbitMQ URL")
	}
	if store == nil {
		return nil, errors.New("analytics worker store is required")
	}
	if metrics == nil {
		metrics = &WorkerMetrics{}
	}
	return &RabbitMQWorker{
		rawURL:  rawURL,
		store:   store,
		metrics: metrics,
		config: WorkerConfig{
			BatchSize: DefaultWorkerBatchSize, FlushInterval: DefaultWorkerFlushInterval,
			MaxRetries: DefaultWorkerMaxRetries,
		},
		prefetch: DefaultWorkerPrefetch, shutdownTimeout: DefaultWorkerShutdownTimeout,
	}, nil
}

func (w *RabbitMQWorker) Run(ctx context.Context) error {
	connection, err := dialWorkerRabbitMQ(w.rawURL)
	if err != nil {
		return fmt.Errorf("connect RabbitMQ worker: %w", err)
	}
	defer connection.Close()
	ch, err := connection.Channel()
	if err != nil {
		return fmt.Errorf("open RabbitMQ worker channel: %w", err)
	}
	defer ch.Close()
	if err := declareWorkerTopology(ch); err != nil {
		return fmt.Errorf("declare RabbitMQ worker topology: %w", err)
	}
	if err := ch.Qos(w.prefetch, 0, false); err != nil {
		return fmt.Errorf("set RabbitMQ worker prefetch: %w", err)
	}
	if err := ch.Confirm(false); err != nil {
		return fmt.Errorf("enable RabbitMQ worker publisher confirms: %w", err)
	}
	confirms := ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	consumerTag := "shortq-analytics-worker"
	deliveries, err := ch.Consume(QueueName, consumerTag, false, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("consume RabbitMQ analytics queue: %w", err)
	}

	workerCtx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	wrapped := make(chan Delivery)
	go bridgeDeliveries(workerCtx, deliveries, wrapped)
	runDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// Basic.Cancel stops new deliveries. RabbitMQ then closes the delivery
			// channel after the bounded prefetch set has been handed off.
			_ = ch.Cancel(consumerTag, false)
			timer := time.NewTimer(w.shutdownTimeout)
			defer timer.Stop()
			select {
			case <-runDone:
			case <-timer.C:
				stopWorker()
			}
		case <-runDone:
		}
	}()

	retryer := &rabbitRetryPublisher{channel: ch, confirms: confirms, timeout: DefaultRetryPublishTimeout}
	worker := NewWorker(w.store, retryer, w.config, w.metrics)
	workerErr := worker.Run(workerCtx, wrapped)
	close(runDone)
	if workerErr != nil {
		return workerErr
	}
	if ctx.Err() != nil {
		return nil
	}
	return errors.New("RabbitMQ analytics delivery channel closed")
}

func dialWorkerRabbitMQ(rawURL string) (*amqp.Connection, error) {
	return amqp.DialConfig(rawURL, amqp.Config{
		Heartbeat: 10 * time.Second,
		Dial: func(network, address string) (net.Conn, error) {
			return net.DialTimeout(network, address, DefaultRetryPublishTimeout)
		},
	})
}

func declareWorkerTopology(ch *amqp.Channel) error {
	if err := declareTopology(ch); err != nil {
		return err
	}
	if err := ch.ExchangeDeclare(RetryExchangeName, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	arguments := amqp.Table{
		"x-dead-letter-exchange":    ExchangeName,
		"x-dead-letter-routing-key": RoutingKey,
	}
	if _, err := ch.QueueDeclare(RetryQueueName, true, false, false, false, arguments); err != nil {
		return err
	}
	return ch.QueueBind(RetryQueueName, RetryRoutingKey, RetryExchangeName, false, nil)
}

func bridgeDeliveries(ctx context.Context, source <-chan amqp.Delivery, destination chan<- Delivery) {
	defer close(destination)
	for item := range source {
		delivery := item
		wrapped := Delivery{
			Body: append([]byte(nil), delivery.Body...), Headers: delivery.Headers,
			ContentType: delivery.ContentType, MessageID: delivery.MessageId,
			Type: delivery.Type, Timestamp: delivery.Timestamp,
			Ack:    func() error { return delivery.Ack(false) },
			Nack:   func(requeue bool) error { return delivery.Nack(false, requeue) },
			Reject: func() error { return delivery.Reject(false) },
		}
		select {
		case destination <- wrapped:
		case <-ctx.Done():
			return
		}
	}
}

type rabbitRetryPublisher struct {
	channel  *amqp.Channel
	confirms <-chan amqp.Confirmation
	timeout  time.Duration
}

func (p *rabbitRetryPublisher) Retry(parent context.Context, delivery Delivery, attempt int) error {
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	delay := baseRetryDelay << (attempt - 1)
	message := amqp.Publishing{
		Headers:     amqp.Table{RetryCountHeader: int32(attempt)},
		ContentType: delivery.ContentType, DeliveryMode: amqp.Persistent,
		Expiration: strconv.FormatInt(delay.Milliseconds(), 10),
		MessageId:  delivery.MessageID, Timestamp: delivery.Timestamp,
		Type: delivery.Type, Body: delivery.Body,
	}
	if err := p.channel.PublishWithContext(ctx, RetryExchangeName, RetryRoutingKey, false, false, message); err != nil {
		return err
	}
	select {
	case confirmation, ok := <-p.confirms:
		if !ok {
			return ErrConfirmClosed
		}
		if !confirmation.Ack {
			return ErrPublishNack
		}
		return nil
	case <-ctx.Done():
		return fmt.Errorf("retry publish confirmation: %w", ctx.Err())
	}
}
