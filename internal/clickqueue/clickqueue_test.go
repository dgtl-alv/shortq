package clickqueue

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"shortq/internal/models"
)

type fakePublisher struct {
	events []models.ClickEvent
	err    error
}

func (p *fakePublisher) Publish(_ context.Context, event models.ClickEvent) error {
	p.events = append(p.events, event)
	return p.err
}

type fakeStore struct {
	events    []models.ClickEvent
	increment []bool
	maxClicks []*int64
	seen      map[string]bool
}

func (s *fakeStore) RecordClick(event models.ClickEvent, increment bool, maxClicks *int64) (bool, error) {
	s.events = append(s.events, event)
	s.increment = append(s.increment, increment)
	s.maxClicks = append(s.maxClicks, maxClicks)
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if event.EventID != "" && s.seen[event.EventID] {
		return true, nil
	}
	s.seen[event.EventID] = true
	return true, nil
}

func testEvent() models.ClickEvent {
	return models.ClickEvent{
		EventID: "11111111-1111-4111-8111-111111111111", LinkID: 42, Slug: "launch",
		IP: "192.0.2.4", CountryCode: "ID", Method: "GET", StatusCode: 302,
		ResolvedURL: "https://example.com/path", RouteType: "default", UserAgent: "test-agent",
		Browser: "Other", OS: "Linux", Device: "desktop", Referrer: "https://ref.example/path",
		ReferrerHost: "ref.example", UTMSource: "newsletter", UTMMedium: "email",
		UTMCampaign: "launch", OccurredAt: time.Date(2026, 9, 19, 8, 30, 0, 0, time.UTC),
	}
}

func TestRecorderPublishesEligibleEventWithoutSynchronousWrite(t *testing.T) {
	publisher := &fakePublisher{}
	store := &fakeStore{}
	recorder := NewRecorder(publisher, store, nil)
	event := testEvent()

	ok, err := recorder.Record(context.Background(), event, true, nil)
	if err != nil || !ok {
		t.Fatalf("Record() = %v, %v", ok, err)
	}
	if len(publisher.events) != 1 || publisher.events[0].EventID != event.EventID {
		t.Fatalf("published events = %#v", publisher.events)
	}
	if len(store.events) != 0 {
		t.Fatalf("unexpected synchronous writes = %#v", store.events)
	}
}

func TestRecorderBrokerFailureFallsBackWithSameEventID(t *testing.T) {
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	store := &fakeStore{}
	recorder := NewRecorder(publisher, store, nil)
	event := testEvent()

	ok, err := recorder.Record(context.Background(), event, true, nil)
	if err != nil || !ok {
		t.Fatalf("Record() = %v, %v", ok, err)
	}
	if len(store.events) != 1 || store.events[0].EventID != publisher.events[0].EventID || store.events[0].EventID != event.EventID {
		t.Fatalf("published=%#v fallback=%#v", publisher.events, store.events)
	}
}

func TestRecorderConfirmAmbiguityFallsBackIdempotently(t *testing.T) {
	publisher := &fakePublisher{err: ErrConfirmTimeout}
	store := &fakeStore{}
	recorder := NewRecorder(publisher, store, nil)
	event := testEvent()

	if ok, err := recorder.Record(context.Background(), event, true, nil); err != nil || !ok {
		t.Fatalf("fallback Record() = %v, %v", ok, err)
	}
	// Simulate later delivery by the worker. The shared event ID must make it a no-op.
	if _, err := store.RecordClick(publisher.events[0], true, nil); err != nil {
		t.Fatal(err)
	}
	if got := len(store.seen); got != 1 {
		t.Fatalf("unique persisted events = %d, want 1", got)
	}
	if store.events[0].EventID != publisher.events[0].EventID {
		t.Fatalf("fallback event ID %q differs from queued ID %q", store.events[0].EventID, publisher.events[0].EventID)
	}
}

func TestRecorderMaxClicksBypassesPublisher(t *testing.T) {
	publisher := &fakePublisher{}
	store := &fakeStore{}
	recorder := NewRecorder(publisher, store, nil)
	maxClicks := int64(5)

	if ok, err := recorder.Record(context.Background(), testEvent(), true, &maxClicks); err != nil || !ok {
		t.Fatalf("Record() = %v, %v", ok, err)
	}
	if len(publisher.events) != 0 || len(store.events) != 1 || store.maxClicks[0] == nil || *store.maxClicks[0] != maxClicks {
		t.Fatalf("published=%d fallback=%#v max=%#v", len(publisher.events), store.events, store.maxClicks)
	}
}

func TestRecorderPreservesExpiredAttemptSemantics(t *testing.T) {
	publisher := &fakePublisher{err: errors.New("broker unavailable")}
	store := &fakeStore{}
	recorder := NewRecorder(publisher, store, nil)
	event := testEvent()
	event.StatusCode = 410
	event.RouteType = "expired"

	if ok, err := recorder.Record(context.Background(), event, false, nil); err != nil || !ok {
		t.Fatalf("Record() = %v, %v", ok, err)
	}
	if len(store.increment) != 1 || store.increment[0] || store.events[0].RouteType != "expired" {
		t.Fatalf("fallback increment=%v event=%#v", store.increment, store.events)
	}
}

func TestMarshalEventIsVersionedAndExcludesSensitiveFields(t *testing.T) {
	event := testEvent()
	event.Referrer = "https://ref.example/path?session_token=do-not-leak&api_key=secret"
	event.ResolvedURL = "https://user:password@example.com/path?authorization=bearer-secret#token"

	payload, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"do-not-leak", "bearer-secret", "password", "authorization", "cookie", "session_token", "api_key"} {
		if strings.Contains(strings.ToLower(string(payload)), forbidden) {
			t.Fatalf("payload contains forbidden value %q: %s", forbidden, payload)
		}
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"version", "event_id", "occurred_at", "link_id", "slug", "method", "status_code", "resolved_url", "route_type", "increment"} {
		if _, ok := decoded[required]; !ok {
			t.Fatalf("required field %q absent from %s", required, payload)
		}
	}
	if decoded["version"] != float64(PayloadVersion) {
		t.Fatalf("version = %#v", decoded["version"])
	}
}

type declaration struct {
	kind       string
	name       string
	durable    bool
	autoDelete bool
	key        string
	exchange   string
	args       amqp.Table
}

type fakeChannel struct {
	declarations []declaration
	confirmMode  bool
	confirm      chan amqp.Confirmation
	published    []amqp.Publishing
	publishErr   error
	confirmation *amqp.Confirmation
	closed       bool
}

func (c *fakeChannel) ExchangeDeclare(name, kind string, durable, autoDelete, _ bool, _ bool, args amqp.Table) error {
	c.declarations = append(c.declarations, declaration{kind: kind, name: name, durable: durable, autoDelete: autoDelete, args: args})
	return nil
}
func (c *fakeChannel) QueueDeclare(name string, durable, autoDelete, _ bool, _ bool, args amqp.Table) (amqp.Queue, error) {
	c.declarations = append(c.declarations, declaration{kind: "queue", name: name, durable: durable, autoDelete: autoDelete, args: args})
	return amqp.Queue{Name: name}, nil
}
func (c *fakeChannel) QueueBind(name, key, exchange string, _ bool, args amqp.Table) error {
	c.declarations = append(c.declarations, declaration{kind: "binding", name: name, key: key, exchange: exchange, args: args})
	return nil
}
func (c *fakeChannel) Confirm(_ bool) error { c.confirmMode = true; return nil }
func (c *fakeChannel) NotifyPublish(confirm chan amqp.Confirmation) chan amqp.Confirmation {
	c.confirm = confirm
	return confirm
}
func (c *fakeChannel) PublishWithContext(_ context.Context, _, _ string, _, _ bool, message amqp.Publishing) error {
	c.published = append(c.published, message)
	if c.publishErr != nil {
		return c.publishErr
	}
	if c.confirmation != nil {
		c.confirm <- *c.confirmation
	}
	return nil
}
func (c *fakeChannel) Close() error { c.closed = true; return nil }

type fakeConnection struct {
	channel channel
	closed  bool
}

func (c *fakeConnection) NewChannel() (channel, error) { return c.channel, nil }
func (c *fakeConnection) Close() error                 { c.closed = true; return nil }

func TestRabbitMQPublisherDeclaresDurableTopologyAndPublishesPersistentMessage(t *testing.T) {
	ack := amqp.Confirmation{Ack: true}
	ch := &fakeChannel{confirmation: &ack}
	conn := &fakeConnection{channel: ch}
	dials := 0
	metrics := &Metrics{}
	publisher := newRabbitMQPublisher("amqp://example.invalid/", 100*time.Millisecond, func(string, time.Duration) (connection, error) {
		dials++
		return conn, nil
	}, metrics)

	if err := publisher.Publish(context.Background(), testEvent()); err != nil {
		t.Fatal(err)
	}
	if err := publisher.Publish(context.Background(), testEvent()); err != nil {
		t.Fatal(err)
	}
	if !ch.confirmMode || len(ch.published) != 2 || dials != 1 {
		t.Fatalf("confirm=%v published=%d dials=%d", ch.confirmMode, len(ch.published), dials)
	}
	snapshot := metrics.Snapshot()
	if snapshot["publish_attempts"] != 2 || snapshot["confirmations"] != 2 || snapshot["publish_failures"] != 0 {
		t.Fatalf("metrics = %#v", snapshot)
	}
	message := ch.published[0]
	if message.DeliveryMode != amqp.Persistent || message.ContentType != "application/json" || message.MessageId != testEvent().EventID {
		t.Fatalf("publishing properties = %#v", message)
	}
	want := map[string]bool{ExchangeName: false, QueueName: false, DeadLetterExchangeName: false, DeadLetterQueueName: false}
	for _, got := range ch.declarations {
		if _, exists := want[got.name]; exists && got.kind != "binding" {
			if !got.durable || got.autoDelete {
				t.Fatalf("non-durable topology declaration: %#v", got)
			}
			want[got.name] = true
		}
		if got.name == QueueName && got.kind == "queue" {
			if got.args["x-dead-letter-exchange"] != DeadLetterExchangeName || got.args["x-dead-letter-routing-key"] != DeadLetterRoutingKey {
				t.Fatalf("click queue dead-letter args = %#v", got.args)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Fatalf("topology %q was not declared: %#v", name, ch.declarations)
		}
	}
}

func TestRabbitMQPublisherRequiresPositiveConfirmation(t *testing.T) {
	nack := amqp.Confirmation{Ack: false}
	ch := &fakeChannel{confirmation: &nack}
	publisher := newRabbitMQPublisher("amqp://example.invalid/", 100*time.Millisecond, func(string, time.Duration) (connection, error) {
		return &fakeConnection{channel: ch}, nil
	}, nil)

	if err := publisher.Publish(context.Background(), testEvent()); !errors.Is(err, ErrPublishNack) {
		t.Fatalf("Publish() error = %v, want nack", err)
	}
	if !ch.closed {
		t.Fatal("ambiguous/nacked channel was not reset")
	}
}

func TestRabbitMQPublisherConfirmTimeoutIsAmbiguousAndResetsChannel(t *testing.T) {
	ch := &fakeChannel{}
	publisher := newRabbitMQPublisher("amqp://example.invalid/", 10*time.Millisecond, func(string, time.Duration) (connection, error) {
		return &fakeConnection{channel: ch}, nil
	}, nil)

	if err := publisher.Publish(context.Background(), testEvent()); !errors.Is(err, ErrConfirmTimeout) {
		t.Fatalf("Publish() error = %v, want confirm timeout", err)
	}
	if !ch.closed {
		t.Fatal("timed-out channel was not reset")
	}
}

func TestRabbitMQPublisherReconnectIsBounded(t *testing.T) {
	dials := 0
	publisher := newRabbitMQPublisher("amqp://example.invalid/", 20*time.Millisecond, func(string, time.Duration) (connection, error) {
		dials++
		return nil, errors.New("broker unavailable")
	}, nil)

	if err := publisher.Publish(context.Background(), testEvent()); err == nil {
		t.Fatal("expected broker error")
	}
	if dials != MaxConnectAttempts {
		t.Fatalf("dial attempts = %d, want %d", dials, MaxConnectAttempts)
	}
}

func TestRabbitMQPublisherSerializesConcurrentRequestsOnOneChannel(t *testing.T) {
	ack := amqp.Confirmation{Ack: true}
	ch := &fakeChannel{confirmation: &ack}
	dials := 0
	publisher := newRabbitMQPublisher("amqp://example.invalid/", time.Second, func(string, time.Duration) (connection, error) {
		dials++
		return &fakeConnection{channel: ch}, nil
	}, nil)

	const requests = 24
	var wait sync.WaitGroup
	errorsSeen := make(chan error, requests)
	for range requests {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errorsSeen <- publisher.Publish(context.Background(), testEvent())
		}()
	}
	wait.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		if err != nil {
			t.Fatal(err)
		}
	}
	if dials != 1 || len(ch.published) != requests {
		t.Fatalf("dials=%d published=%d", dials, len(ch.published))
	}
}
