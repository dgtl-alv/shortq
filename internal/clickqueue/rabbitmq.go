package clickqueue

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"shortq/internal/models"
)

const (
	ExchangeName           = "shortq.analytics"
	QueueName              = "shortq.clicks"
	RoutingKey             = "click.v1"
	DeadLetterExchangeName = "shortq.analytics.dlx"
	DeadLetterQueueName    = "shortq.clicks.dlq"
	DeadLetterRoutingKey   = "click.dead"
	DefaultConfirmTimeout  = 250 * time.Millisecond
	MaxConnectAttempts     = 2
)

var (
	ErrConfirmTimeout = errors.New("publisher confirmation timed out; outcome is ambiguous")
	ErrPublishNack    = errors.New("publisher confirmation was negative")
	ErrConfirmClosed  = errors.New("publisher confirmation channel closed; outcome is ambiguous")
)

type channel interface {
	ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error
	QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error)
	QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error
	Confirm(noWait bool) error
	NotifyPublish(confirm chan amqp.Confirmation) chan amqp.Confirmation
	PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error
	Close() error
}

type connection interface {
	NewChannel() (channel, error)
	Close() error
}

type amqpConnection struct{ connection *amqp.Connection }

func (c *amqpConnection) NewChannel() (channel, error) { return c.connection.Channel() }
func (c *amqpConnection) Close() error                 { return c.connection.Close() }

type dialer func(rawURL string, timeout time.Duration) (connection, error)

type RabbitMQPublisher struct {
	rawURL   string
	timeout  time.Duration
	dial     dialer
	observer Observer
	lock     chan struct{}
	conn     connection
	channel  channel
	confirms <-chan amqp.Confirmation
}

func NewRabbitMQPublisher(rawURL string, timeout time.Duration, observer Observer) (*RabbitMQPublisher, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil || (parsed.Scheme != "amqp" && parsed.Scheme != "amqps") || parsed.Host == "" {
		return nil, fmt.Errorf("invalid RabbitMQ URL")
	}
	return newRabbitMQPublisher(rawURL, timeout, dialRabbitMQ, observer), nil
}

func newRabbitMQPublisher(rawURL string, timeout time.Duration, dial dialer, observer Observer) *RabbitMQPublisher {
	if timeout <= 0 {
		timeout = DefaultConfirmTimeout
	}
	return &RabbitMQPublisher{rawURL: rawURL, timeout: timeout, dial: dial, observer: observer, lock: make(chan struct{}, 1)}
}

func dialRabbitMQ(rawURL string, timeout time.Duration) (connection, error) {
	config := amqp.Config{
		Heartbeat: 10 * time.Second,
		Dial: func(network, address string) (net.Conn, error) {
			return net.DialTimeout(network, address, timeout)
		},
	}
	conn, err := amqp.DialConfig(rawURL, config)
	if err != nil {
		return nil, err
	}
	return &amqpConnection{connection: conn}, nil
}

func (p *RabbitMQPublisher) Publish(parent context.Context, event models.ClickEvent) (err error) {
	started := time.Now()
	defer func() {
		duration := time.Since(started)
		if p.observer != nil {
			p.observer.PublishDuration(duration)
			if err != nil {
				p.observer.PublishFailure(err)
			}
		}
		if err != nil {
			log.Printf("click_queue event=publish_failure event_id=%s duration_ms=%d error=%v", event.EventID, duration.Milliseconds(), err)
		}
	}()

	body, err := MarshalEvent(event)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, p.timeout)
	defer cancel()
	select {
	case p.lock <- struct{}{}:
		defer func() { <-p.lock }()
	case <-ctx.Done():
		return fmt.Errorf("wait for publisher channel: %w", ctx.Err())
	}

	message := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		MessageId:    event.EventID,
		Type:         "shortq.click.v1",
		Timestamp:    event.OccurredAt,
		Body:         body,
	}
	for attempt := 1; attempt <= MaxConnectAttempts; attempt++ {
		if err = p.ensureChannel(); err != nil {
			p.reset()
			if ctx.Err() != nil || attempt == MaxConnectAttempts {
				return err
			}
			continue
		}
		if err = p.channel.PublishWithContext(ctx, ExchangeName, RoutingKey, false, false, message); err != nil {
			p.reset()
			if ctx.Err() != nil || attempt == MaxConnectAttempts {
				return err
			}
			continue
		}
		select {
		case confirmation, open := <-p.confirms:
			if !open {
				p.confirmation(false)
				p.reset()
				return ErrConfirmClosed
			}
			if !confirmation.Ack {
				p.confirmation(false)
				p.reset()
				return ErrPublishNack
			}
			p.confirmation(true)
			return nil
		case <-ctx.Done():
			p.confirmation(false)
			p.reset()
			return fmt.Errorf("%w: %v", ErrConfirmTimeout, ctx.Err())
		}
	}
	return err
}

func (p *RabbitMQPublisher) ensureChannel() error {
	if p.channel != nil {
		return nil
	}
	dialTimeout := p.timeout / MaxConnectAttempts
	if dialTimeout <= 0 {
		dialTimeout = p.timeout
	}
	conn, err := p.dial(p.rawURL, dialTimeout)
	if err != nil {
		return err
	}
	ch, err := conn.NewChannel()
	if err != nil {
		_ = conn.Close()
		return err
	}
	if err := declareTopology(ch); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		_ = conn.Close()
		return err
	}
	p.conn = conn
	p.channel = ch
	p.confirms = ch.NotifyPublish(make(chan amqp.Confirmation, 1))
	return nil
}

func declareTopology(ch channel) error {
	if err := ch.ExchangeDeclare(DeadLetterExchangeName, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(DeadLetterQueueName, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.QueueBind(DeadLetterQueueName, DeadLetterRoutingKey, DeadLetterExchangeName, false, nil); err != nil {
		return err
	}
	if err := ch.ExchangeDeclare(ExchangeName, "direct", true, false, false, false, nil); err != nil {
		return err
	}
	arguments := amqp.Table{
		"x-dead-letter-exchange":    DeadLetterExchangeName,
		"x-dead-letter-routing-key": DeadLetterRoutingKey,
	}
	if _, err := ch.QueueDeclare(QueueName, true, false, false, false, arguments); err != nil {
		return err
	}
	return ch.QueueBind(QueueName, RoutingKey, ExchangeName, false, nil)
}

func (p *RabbitMQPublisher) confirmation(ok bool) {
	if p.observer != nil {
		p.observer.Confirmation(ok)
	}
}

func (p *RabbitMQPublisher) reset() {
	if p.channel != nil {
		_ = p.channel.Close()
	}
	if p.conn != nil {
		_ = p.conn.Close()
	}
	p.channel = nil
	p.conn = nil
	p.confirms = nil
}

func (p *RabbitMQPublisher) Close() error {
	p.lock <- struct{}{}
	defer func() { <-p.lock }()
	var errs []error
	if p.channel != nil {
		errs = append(errs, p.channel.Close())
	}
	if p.conn != nil {
		errs = append(errs, p.conn.Close())
	}
	p.channel = nil
	p.conn = nil
	p.confirms = nil
	return errors.Join(errs...)
}
