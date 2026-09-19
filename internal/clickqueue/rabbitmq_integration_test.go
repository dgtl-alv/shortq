package clickqueue

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestRabbitMQPublishIntegration(t *testing.T) {
	rawURL := os.Getenv("TEST_RABBITMQ_URL")
	if rawURL == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}
	connection, err := amqp.Dial(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	_, _ = channel.QueueDelete(QueueName, false, false, false)
	_, _ = channel.QueueDelete(DeadLetterQueueName, false, false, false)
	_, _ = channel.QueueDelete(RetryQueueName, false, false, false)
	_ = channel.ExchangeDelete(ExchangeName, false, false)
	_ = channel.ExchangeDelete(DeadLetterExchangeName, false, false)
	_ = channel.ExchangeDelete(RetryExchangeName, false, false)

	publisher, err := NewRabbitMQPublisher(rawURL, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	event := testEvent()
	if err := publisher.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}

	// A matching active declaration verifies topology can be declared repeatedly.
	if err := declareTopology(channel); err != nil {
		t.Fatalf("topology redeclaration: %v", err)
	}
	delivery, ok, err := channel.Get(QueueName, true)
	if err != nil || !ok {
		t.Fatalf("Get() = ok %v, err %v", ok, err)
	}
	if delivery.DeliveryMode != amqp.Persistent || delivery.MessageId != event.EventID {
		t.Fatalf("delivery properties = %#v", delivery)
	}
	var payload EventPayload
	if err := json.Unmarshal(delivery.Body, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != PayloadVersion || payload.EventID != event.EventID || !payload.OccurredAt.Equal(event.OccurredAt) {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestRabbitMQWorkerRoutesPoisonMessageToDLQ(t *testing.T) {
	rawURL := os.Getenv("TEST_RABBITMQ_URL")
	if rawURL == "" {
		t.Skip("TEST_RABBITMQ_URL is not set")
	}
	connection, err := amqp.Dial(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	channel, err := connection.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer channel.Close()
	_, _ = channel.QueueDelete(QueueName, false, false, false)
	_, _ = channel.QueueDelete(DeadLetterQueueName, false, false, false)
	_, _ = channel.QueueDelete(RetryQueueName, false, false, false)
	_ = channel.ExchangeDelete(ExchangeName, false, false)
	_ = channel.ExchangeDelete(DeadLetterExchangeName, false, false)
	_ = channel.ExchangeDelete(RetryExchangeName, false, false)

	metrics := &WorkerMetrics{}
	worker, err := NewRabbitMQWorker(rawURL, &workerStore{}, metrics)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, probeErr := connection.Channel()
		if probeErr != nil {
			cancel()
			t.Fatal(probeErr)
		}
		_, inspectErr := probe.QueueInspect(QueueName)
		_ = probe.Close()
		if inspectErr == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("worker did not declare topology")
		}
		time.Sleep(20 * time.Millisecond)
	}
	deadline = time.Now().Add(5 * time.Second)
	if err := channel.PublishWithContext(context.Background(), ExchangeName, RoutingKey, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent,
		MessageId: "poison-message", Body: []byte(`{"version":99}`),
	}); err != nil {
		cancel()
		t.Fatal(err)
	}
	var poison amqp.Delivery
	for {
		delivery, ok, err := channel.Get(DeadLetterQueueName, true)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if ok {
			poison = delivery
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("poison message did not reach DLQ")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not shut down")
	}
	if poison.MessageId != "poison-message" {
		t.Fatalf("DLQ message ID = %q", poison.MessageId)
	}
	snapshot := metrics.Snapshot()
	if snapshot["received"] != 1 || snapshot["invalid"] != 1 || snapshot["dlq"] != 1 {
		t.Fatalf("metrics = %#v", snapshot)
	}
}
