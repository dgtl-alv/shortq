package clickqueue

import (
	"context"
	"encoding/json"
	"log"
	"net/url"
	"sync/atomic"
	"time"

	"shortq/internal/models"
)

const PayloadVersion = 1

type Publisher interface {
	Publish(ctx context.Context, event models.ClickEvent) error
}

type ClickStore interface {
	RecordClick(event models.ClickEvent, increment bool, maxClicks *int64) (bool, error)
}

type Observer interface {
	PublishDuration(time.Duration)
	PublishFailure(error)
	Confirmation(bool)
	Fallback(error)
}

type EventPayload struct {
	Version      int       `json:"version"`
	EventID      string    `json:"event_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	LinkID       int64     `json:"link_id"`
	Slug         string    `json:"slug"`
	IP           string    `json:"ip"`
	CountryCode  string    `json:"country_code"`
	Method       string    `json:"method"`
	StatusCode   int       `json:"status_code"`
	ResolvedURL  string    `json:"resolved_url"`
	RouteType    string    `json:"route_type"`
	UserAgent    string    `json:"user_agent"`
	Browser      string    `json:"browser"`
	OS           string    `json:"os"`
	Device       string    `json:"device"`
	IsBot        bool      `json:"is_bot"`
	Referrer     string    `json:"referrer"`
	ReferrerHost string    `json:"referrer_host"`
	UTMSource    string    `json:"utm_source"`
	UTMMedium    string    `json:"utm_medium"`
	UTMCampaign  string    `json:"utm_campaign"`
	Increment    bool      `json:"increment"`
}

func MarshalEvent(event models.ClickEvent) ([]byte, error) {
	payload := EventPayload{
		Version: PayloadVersion, EventID: event.EventID, OccurredAt: event.OccurredAt,
		LinkID: event.LinkID, Slug: event.Slug, IP: event.IP, CountryCode: event.CountryCode,
		Method: event.Method, StatusCode: event.StatusCode, ResolvedURL: scrubURL(event.ResolvedURL),
		RouteType: event.RouteType, UserAgent: event.UserAgent, Browser: event.Browser, OS: event.OS,
		Device: event.Device, IsBot: event.IsBot, Referrer: scrubURL(event.Referrer),
		ReferrerHost: event.ReferrerHost, UTMSource: event.UTMSource, UTMMedium: event.UTMMedium,
		UTMCampaign: event.UTMCampaign, Increment: event.Increment,
	}
	return json.Marshal(payload)
}

func scrubURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

type Recorder struct {
	publisher Publisher
	store     ClickStore
	observer  Observer
}

func NewRecorder(publisher Publisher, store ClickStore, observer Observer) *Recorder {
	return &Recorder{publisher: publisher, store: store, observer: observer}
}

func (r *Recorder) Record(ctx context.Context, event models.ClickEvent, increment bool, maxClicks *int64) (bool, error) {
	event.Increment = increment
	if r.publisher == nil || maxClicks != nil {
		return r.store.RecordClick(event, increment, maxClicks)
	}
	if err := r.publisher.Publish(ctx, event); err == nil {
		return true, nil
	} else {
		if r.observer != nil {
			r.observer.Fallback(err)
		}
		log.Printf("click_queue event=synchronous_fallback event_id=%s cause=%v", event.EventID, err)
		ok, fallbackErr := r.store.RecordClick(event, increment, nil)
		if fallbackErr != nil {
			log.Printf("click_queue event=fallback_failure event_id=%s error=%v", event.EventID, fallbackErr)
		}
		return ok, fallbackErr
	}
}

type Metrics struct {
	publishAttempts      atomic.Uint64
	publishFailures      atomic.Uint64
	confirmations        atomic.Uint64
	confirmationFailures atomic.Uint64
	fallbacks            atomic.Uint64
	durationMillisTotal  atomic.Uint64
	durationMillisMax    atomic.Uint64
}

func (m *Metrics) PublishDuration(duration time.Duration) {
	m.publishAttempts.Add(1)
	milliseconds := uint64(duration.Milliseconds())
	m.durationMillisTotal.Add(milliseconds)
	for current := m.durationMillisMax.Load(); milliseconds > current && !m.durationMillisMax.CompareAndSwap(current, milliseconds); current = m.durationMillisMax.Load() {
	}
}

func (m *Metrics) PublishFailure(error) { m.publishFailures.Add(1) }
func (m *Metrics) Confirmation(ok bool) {
	if ok {
		m.confirmations.Add(1)
	} else {
		m.confirmationFailures.Add(1)
	}
}
func (m *Metrics) Fallback(error) { m.fallbacks.Add(1) }

func (m *Metrics) Snapshot() map[string]uint64 {
	return map[string]uint64{
		"publish_attempts":          m.publishAttempts.Load(),
		"publish_failures":          m.publishFailures.Load(),
		"confirmations":             m.confirmations.Load(),
		"confirmation_failures":     m.confirmationFailures.Load(),
		"fallbacks":                 m.fallbacks.Load(),
		"publish_duration_ms_total": m.durationMillisTotal.Load(),
		"publish_duration_ms_max":   m.durationMillisMax.Load(),
	}
}
