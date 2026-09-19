package clickqueue

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"

	"shortq/internal/models"
)

const (
	DefaultWorkerBatchSize     = 200
	DefaultWorkerFlushInterval = 250 * time.Millisecond
	DefaultWorkerMaxRetries    = 5
	RetryCountHeader           = "x-shortq-retry-count"
)

type BatchStore interface {
	RecordClicks(events []models.ClickEvent) (int, error)
}

type Delivery struct {
	Body        []byte
	Headers     amqp.Table
	ContentType string
	MessageID   string
	Type        string
	Timestamp   time.Time
	Ack         func() error
	Nack        func(requeue bool) error
	Reject      func() error
}

type RetryPublisher interface {
	Retry(ctx context.Context, delivery Delivery, attempt int) error
}

type WorkerConfig struct {
	BatchSize     int
	FlushInterval time.Duration
	MaxRetries    int
}

type Worker struct {
	store   BatchStore
	retryer RetryPublisher
	config  WorkerConfig
	metrics *WorkerMetrics
}

func NewWorker(store BatchStore, retryer RetryPublisher, config WorkerConfig, metrics *WorkerMetrics) *Worker {
	if config.BatchSize <= 0 || config.BatchSize > DefaultWorkerBatchSize {
		config.BatchSize = DefaultWorkerBatchSize
	}
	if config.FlushInterval <= 0 {
		config.FlushInterval = DefaultWorkerFlushInterval
	}
	if config.MaxRetries <= 0 {
		config.MaxRetries = DefaultWorkerMaxRetries
	}
	if metrics == nil {
		metrics = &WorkerMetrics{}
	}
	return &Worker{store: store, retryer: retryer, config: config, metrics: metrics}
}

type pendingDelivery struct {
	delivery Delivery
	event    models.ClickEvent
}

func (w *Worker) Run(ctx context.Context, deliveries <-chan Delivery) error {
	batch := make([]pendingDelivery, 0, w.config.BatchSize)
	timer := time.NewTimer(w.config.FlushInterval)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	var timerC <-chan time.Time

	resetTimer := func() {
		if timerC == nil {
			timer.Reset(w.config.FlushInterval)
			timerC = timer.C
		}
	}
	stopTimer := func() {
		if timerC != nil {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timerC = nil
		}
	}
	flush := func() error {
		stopTimer()
		if len(batch) == 0 {
			return nil
		}
		current := batch
		batch = make([]pendingDelivery, 0, w.config.BatchSize)
		return w.flush(ctx, current)
	}
	accept := func(delivery Delivery) error {
		w.metrics.received.Add(1)
		event, err := DecodeEvent(delivery.Body)
		if err != nil {
			w.metrics.invalid.Add(1)
			w.metrics.dlq.Add(1)
			log.Printf("analytics_worker event=invalid_message message_id=%s error=%v", delivery.MessageID, err)
			if delivery.Reject != nil {
				return delivery.Reject()
			}
			return nil
		}
		w.metrics.observeLag(event.OccurredAt)
		batch = append(batch, pendingDelivery{delivery: delivery, event: event})
		resetTimer()
		if len(batch) >= w.config.BatchSize {
			return flush()
		}
		return nil
	}

	for {
		select {
		case delivery, ok := <-deliveries:
			if !ok {
				return flush()
			}
			if err := accept(delivery); err != nil {
				return err
			}
		case <-timerC:
			timerC = nil
			if err := flush(); err != nil {
				return err
			}
		case <-ctx.Done():
			// Intake has already been cancelled by the RabbitMQ runner. Include
			// deliveries already handed to this bounded consumer, then flush.
			for {
				select {
				case delivery, ok := <-deliveries:
					if !ok {
						return flush()
					}
					if err := accept(delivery); err != nil {
						return err
					}
				default:
					return flush()
				}
			}
		}
	}
}

func (w *Worker) flush(ctx context.Context, batch []pendingDelivery) error {
	started := time.Now()
	events := make([]models.ClickEvent, len(batch))
	for i := range batch {
		events[i] = batch[i].event
	}
	inserted, err := w.store.RecordClicks(events)
	duration := time.Since(started)
	w.metrics.observeBatch(duration, len(batch))
	if err != nil {
		w.metrics.failures.Add(1)
		log.Printf("analytics_worker event=batch_failure size=%d duration_ms=%d error=%v", len(batch), duration.Milliseconds(), err)
		return w.retryBatch(ctx, batch)
	}
	w.metrics.committed.Add(uint64(inserted))
	w.metrics.duplicate.Add(uint64(len(batch) - inserted))
	for _, pending := range batch {
		if pending.delivery.Ack != nil {
			if err := pending.delivery.Ack(); err != nil {
				return fmt.Errorf("ack committed event %s: %w", pending.event.EventID, err)
			}
		}
	}
	log.Printf("analytics_worker event=batch_committed size=%d inserted=%d duplicate=%d duration_ms=%d", len(batch), inserted, len(batch)-inserted, duration.Milliseconds())
	return nil
}

func (w *Worker) retryBatch(ctx context.Context, batch []pendingDelivery) error {
	for _, pending := range batch {
		attempt := retryCount(pending.delivery.Headers) + 1
		if attempt >= w.config.MaxRetries {
			w.metrics.dlq.Add(1)
			log.Printf("analytics_worker event=retry_exhausted event_id=%s attempts=%d", pending.event.EventID, attempt)
			if pending.delivery.Reject != nil {
				if err := pending.delivery.Reject(); err != nil {
					return err
				}
			}
			continue
		}
		if w.retryer == nil {
			if pending.delivery.Nack != nil {
				if err := pending.delivery.Nack(true); err != nil {
					return err
				}
			}
			w.metrics.requeue.Add(1)
			continue
		}
		if err := w.retryer.Retry(ctx, pending.delivery, attempt); err != nil {
			// Requeueing unchanged would never advance the retry header. Route
			// the original to the existing DLQ instead of creating an infinite
			// loop when the retry publish path is unavailable.
			w.metrics.dlq.Add(1)
			log.Printf("analytics_worker event=retry_schedule_failure event_id=%s attempt=%d error=%v", pending.event.EventID, attempt, err)
			if pending.delivery.Reject != nil {
				if rejectErr := pending.delivery.Reject(); rejectErr != nil {
					return errors.Join(err, rejectErr)
				}
			}
			continue
		}
		// ACK is safe here because the replacement is durably confirmed in
		// the retry queue. No failed DB delivery is discarded.
		if pending.delivery.Ack != nil {
			if err := pending.delivery.Ack(); err != nil {
				return err
			}
		}
		w.metrics.requeue.Add(1)
		log.Printf("analytics_worker event=retry_scheduled event_id=%s attempt=%d", pending.event.EventID, attempt)
	}
	return nil
}

func DecodeEvent(body []byte) (models.ClickEvent, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var payload EventPayload
	if err := decoder.Decode(&payload); err != nil {
		return models.ClickEvent{}, fmt.Errorf("decode payload: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return models.ClickEvent{}, err
	}
	if payload.Version != PayloadVersion {
		return models.ClickEvent{}, fmt.Errorf("unsupported payload version %d", payload.Version)
	}
	if !validUUID(payload.EventID) {
		return models.ClickEvent{}, errors.New("event_id must be a UUID")
	}
	if payload.OccurredAt.IsZero() {
		return models.ClickEvent{}, errors.New("occurred_at is required")
	}
	if payload.LinkID <= 0 {
		return models.ClickEvent{}, errors.New("link_id must be positive")
	}
	if strings.TrimSpace(payload.Slug) == "" || strings.TrimSpace(payload.Method) == "" || strings.TrimSpace(payload.RouteType) == "" {
		return models.ClickEvent{}, errors.New("slug, method, and route_type are required")
	}
	if payload.StatusCode < 100 || payload.StatusCode > 599 {
		return models.ClickEvent{}, errors.New("status_code is invalid")
	}
	return models.ClickEvent{
		EventID: payload.EventID, OccurredAt: payload.OccurredAt, LinkID: payload.LinkID,
		Slug: payload.Slug, IP: payload.IP, CountryCode: payload.CountryCode, Method: payload.Method,
		StatusCode: payload.StatusCode, ResolvedURL: payload.ResolvedURL, RouteType: payload.RouteType,
		UserAgent: payload.UserAgent, Browser: payload.Browser, OS: payload.OS, Device: payload.Device,
		IsBot: payload.IsBot, Referrer: payload.Referrer, ReferrerHost: payload.ReferrerHost,
		UTMSource: payload.UTMSource, UTMMedium: payload.UTMMedium, UTMCampaign: payload.UTMCampaign,
		Increment: payload.Increment,
	}, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("payload contains multiple JSON values")
		}
		return fmt.Errorf("decode trailing payload: %w", err)
	}
	return nil
}

func validUUID(value string) bool {
	compact := strings.ReplaceAll(value, "-", "")
	if len(value) != 36 || len(compact) != 32 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	_, err := hex.DecodeString(compact)
	return err == nil
}

func retryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	switch value := headers[RetryCountHeader].(type) {
	case int:
		return value
	case int8:
		return int(value)
	case int16:
		return int(value)
	case int32:
		return int(value)
	case int64:
		return int(value)
	case uint8:
		return int(value)
	case uint16:
		return int(value)
	case uint32:
		return int(value)
	case uint64:
		return int(value)
	default:
		return 0
	}
}

type WorkerMetrics struct {
	received             atomic.Uint64
	committed            atomic.Uint64
	duplicate            atomic.Uint64
	invalid              atomic.Uint64
	requeue              atomic.Uint64
	dlq                  atomic.Uint64
	failures             atomic.Uint64
	batches              atomic.Uint64
	batchEvents          atomic.Uint64
	batchDurationMsTotal atomic.Uint64
	batchDurationMsMax   atomic.Uint64
	eventLagMsTotal      atomic.Uint64
	eventLagMsMax        atomic.Uint64
}

func (m *WorkerMetrics) observeBatch(duration time.Duration, size int) {
	m.batches.Add(1)
	m.batchEvents.Add(uint64(size))
	milliseconds := uint64(duration.Milliseconds())
	m.batchDurationMsTotal.Add(milliseconds)
	for current := m.batchDurationMsMax.Load(); milliseconds > current && !m.batchDurationMsMax.CompareAndSwap(current, milliseconds); current = m.batchDurationMsMax.Load() {
	}
}

func (m *WorkerMetrics) observeLag(occurredAt time.Time) {
	lag := time.Since(occurredAt)
	if lag < 0 {
		lag = 0
	}
	milliseconds := uint64(lag.Milliseconds())
	m.eventLagMsTotal.Add(milliseconds)
	for current := m.eventLagMsMax.Load(); milliseconds > current && !m.eventLagMsMax.CompareAndSwap(current, milliseconds); current = m.eventLagMsMax.Load() {
	}
}

func (m *WorkerMetrics) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"received":                m.received.Load(),
		"committed":               m.committed.Load(),
		"duplicate":               m.duplicate.Load(),
		"invalid":                 m.invalid.Load(),
		"requeue":                 m.requeue.Load(),
		"dlq":                     m.dlq.Load(),
		"failures":                m.failures.Load(),
		"batches":                 m.batches.Load(),
		"batch_events":            m.batchEvents.Load(),
		"batch_duration_ms_total": m.batchDurationMsTotal.Load(),
		"batch_duration_ms_max":   m.batchDurationMsMax.Load(),
		"event_lag_ms_total":      m.eventLagMsTotal.Load(),
		"event_lag_ms_max":        m.eventLagMsMax.Load(),
	}
}
