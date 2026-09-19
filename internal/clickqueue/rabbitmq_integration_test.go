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
	_ = channel.ExchangeDelete(ExchangeName, false, false)
	_ = channel.ExchangeDelete(DeadLetterExchangeName, false, false)

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
