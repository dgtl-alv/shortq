package clickqueue

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"shortq/internal/models"
)

type workerStore struct {
	mu           sync.Mutex
	calls        [][]models.ClickEvent
	results      []int
	errors       []error
	beforeReturn func()
}

func (s *workerStore) RecordClicks(events []models.ClickEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch := append([]models.ClickEvent(nil), events...)
	s.calls = append(s.calls, batch)
	if s.beforeReturn != nil {
		s.beforeReturn()
	}
	index := len(s.calls) - 1
	var inserted int
	if index < len(s.results) {
		inserted = s.results[index]
	} else {
		inserted = len(events)
	}
	if index < len(s.errors) {
		return inserted, s.errors[index]
	}
	return inserted, nil
}

func (s *workerStore) batches() [][]models.ClickEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][]models.ClickEvent(nil), s.calls...)
}

type deliveryState struct {
	mu       sync.Mutex
	acked    bool
	nacked   bool
	requeue  bool
	rejected bool
}

type deliverySnapshot struct {
	acked    bool
	nacked   bool
	requeue  bool
	rejected bool
}

func (s *deliveryState) snapshot() deliverySnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return deliverySnapshot{acked: s.acked, nacked: s.nacked, requeue: s.requeue, rejected: s.rejected}
}

func workerDelivery(t *testing.T, event models.ClickEvent) (Delivery, *deliveryState) {
	t.Helper()
	body, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	state := &deliveryState{}
	return Delivery{
		Body: body,
		Ack: func() error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.acked = true
			return nil
		},
		Nack: func(requeue bool) error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.nacked = true
			state.requeue = requeue
			return nil
		},
		Reject: func() error {
			state.mu.Lock()
			defer state.mu.Unlock()
			state.rejected = true
			return nil
		},
	}, state
}

type capturedRetry struct {
	delivery Delivery
	attempt  int
}

type workerRetryer struct {
	mu      sync.Mutex
	retries []capturedRetry
	err     error
}

func (r *workerRetryer) Retry(_ context.Context, delivery Delivery, attempt int) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.retries = append(r.retries, capturedRetry{delivery: delivery, attempt: attempt})
	return r.err
}

func (r *workerRetryer) captured() []capturedRetry {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]capturedRetry(nil), r.retries...)
}

func newTestWorker(store BatchStore, retryer RetryPublisher, size int, interval time.Duration) *Worker {
	return NewWorker(store, retryer, WorkerConfig{BatchSize: size, FlushInterval: interval, MaxRetries: 5}, &WorkerMetrics{})
}

func runWorker(t *testing.T, worker *Worker, deliveries <-chan Delivery) chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- worker.Run(context.Background(), deliveries) }()
	return done
}

func TestWorkerFlushesBatchBySize(t *testing.T) {
	store := &workerStore{}
	worker := newTestWorker(store, nil, 2, time.Hour)
	deliveries := make(chan Delivery, 2)
	first, firstState := workerDelivery(t, testEvent())
	secondEvent := testEvent()
	secondEvent.EventID = "22222222-2222-4222-8222-222222222222"
	second, secondState := workerDelivery(t, secondEvent)
	done := runWorker(t, worker, deliveries)
	deliveries <- first
	deliveries <- second
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if batches := store.batches(); len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("batches = %#v", batches)
	}
	if !firstState.snapshot().acked || !secondState.snapshot().acked {
		t.Fatal("committed batch was not acknowledged")
	}
}

func TestWorkerFlushesBatchByTime(t *testing.T) {
	store := &workerStore{}
	worker := newTestWorker(store, nil, 200, 20*time.Millisecond)
	deliveries := make(chan Delivery, 1)
	delivery, state := workerDelivery(t, testEvent())
	done := runWorker(t, worker, deliveries)
	deliveries <- delivery
	deadline := time.After(time.Second)
	for !state.snapshot().acked {
		select {
		case <-deadline:
			t.Fatal("timed flush did not commit and ACK")
		case <-time.After(time.Millisecond):
		}
	}
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWorkerDuplicateAndMixedBatch(t *testing.T) {
	store := &workerStore{results: []int{1}}
	metrics := &WorkerMetrics{}
	worker := NewWorker(store, nil, WorkerConfig{BatchSize: 2, FlushInterval: time.Hour, MaxRetries: 5}, metrics)
	deliveries := make(chan Delivery, 3)
	valid, validState := workerDelivery(t, testEvent())
	duplicate, duplicateState := workerDelivery(t, testEvent())
	invalidState := &deliveryState{}
	invalid := Delivery{Body: []byte(`{"version":99}`), Reject: func() error {
		invalidState.mu.Lock()
		invalidState.rejected = true
		invalidState.mu.Unlock()
		return nil
	}}
	done := runWorker(t, worker, deliveries)
	deliveries <- valid
	deliveries <- invalid
	deliveries <- duplicate
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	snapshot := metrics.Snapshot()
	if snapshot["received"] != 3 || snapshot["committed"] != 1 || snapshot["duplicate"] != 1 || snapshot["invalid"] != 1 || snapshot["dlq"] != 1 {
		t.Fatalf("metrics = %#v", snapshot)
	}
	if !validState.snapshot().acked || !duplicateState.snapshot().acked || !invalidState.snapshot().rejected {
		t.Fatalf("valid=%#v duplicate=%#v invalid=%#v", validState.snapshot(), duplicateState.snapshot(), invalidState.snapshot())
	}
}

func TestWorkerRollbackThenRetryAndAckOnlyAfterCommit(t *testing.T) {
	state := &deliveryState{}
	store := &workerStore{errors: []error{errors.New("temporary database failure"), nil}}
	store.beforeReturn = func() {
		if state.snapshot().acked {
			t.Error("delivery ACKed before Store.RecordClicks returned")
		}
	}
	retryer := &workerRetryer{}
	worker := newTestWorker(store, retryer, 1, time.Hour)
	delivery, actualState := workerDelivery(t, testEvent())
	state = actualState
	deliveries := make(chan Delivery, 1)
	done := runWorker(t, worker, deliveries)
	deliveries <- delivery
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	retries := retryer.captured()
	if len(retries) != 1 || retries[0].attempt != 1 || !state.snapshot().acked {
		t.Fatalf("retries=%#v state=%#v", retries, state.snapshot())
	}

	retriedState := &deliveryState{}
	state = retriedState
	retried := retries[0].delivery
	retried.Headers = amqp.Table{RetryCountHeader: int32(1)}
	retried.Ack = func() error { retriedState.mu.Lock(); retriedState.acked = true; retriedState.mu.Unlock(); return nil }
	secondRun := make(chan Delivery, 1)
	secondDone := runWorker(t, worker, secondRun)
	secondRun <- retried
	close(secondRun)
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if len(store.batches()) != 2 || !retriedState.snapshot().acked {
		t.Fatalf("calls=%d retried=%#v", len(store.batches()), retriedState.snapshot())
	}
}

func TestWorkerPoisonMessageGoesDirectlyToDLQ(t *testing.T) {
	store := &workerStore{}
	state := &deliveryState{}
	delivery := Delivery{
		Body:   []byte(`{"version":1,"event_id":"not-a-uuid"}`),
		Reject: func() error { state.mu.Lock(); state.rejected = true; state.mu.Unlock(); return nil },
	}
	deliveries := make(chan Delivery, 1)
	done := runWorker(t, newTestWorker(store, nil, 1, time.Hour), deliveries)
	deliveries <- delivery
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !state.snapshot().rejected || len(store.batches()) != 0 {
		t.Fatalf("state=%#v batches=%#v", state.snapshot(), store.batches())
	}
}

func TestWorkerExhaustedRetryGoesToDLQWithoutRequeue(t *testing.T) {
	store := &workerStore{errors: []error{errors.New("database unavailable")}}
	retryer := &workerRetryer{}
	delivery, state := workerDelivery(t, testEvent())
	delivery.Headers = amqp.Table{RetryCountHeader: int32(4)}
	deliveries := make(chan Delivery, 1)
	done := runWorker(t, newTestWorker(store, retryer, 1, time.Hour), deliveries)
	deliveries <- delivery
	close(deliveries)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got := state.snapshot()
	if !got.rejected || got.nacked || got.acked || len(retryer.captured()) != 0 {
		t.Fatalf("state=%#v retries=%#v", got, retryer.captured())
	}
}

func TestWorkerStopsIntakeAndFlushesOnShutdown(t *testing.T) {
	store := &workerStore{}
	worker := newTestWorker(store, nil, 200, time.Hour)
	deliveries := make(chan Delivery, 1)
	delivery, state := workerDelivery(t, testEvent())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, deliveries) }()
	deliveries <- delivery
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not shut down")
	}
	if len(store.batches()) != 1 || !state.snapshot().acked {
		t.Fatalf("batches=%#v state=%#v", store.batches(), state.snapshot())
	}
}

func TestDecodeEventPreservesOccurredAtForRollups(t *testing.T) {
	event := testEvent()
	event.OccurredAt = time.Date(2026, 9, 18, 23, 59, 0, 0, time.FixedZone("UTC-7", -7*60*60))
	body, err := MarshalEvent(event)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeEvent(body)
	if err != nil {
		t.Fatal(err)
	}
	if !decoded.OccurredAt.Equal(event.OccurredAt) || decoded.OccurredAt.UTC().Day() != 19 {
		t.Fatalf("occurred_at = %s, want instant %s on UTC day 19", decoded.OccurredAt, event.OccurredAt)
	}
}
