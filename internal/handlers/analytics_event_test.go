package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"shortq/internal/clickqueue"
	"shortq/internal/config"
	"shortq/internal/models"
	"shortq/internal/redirectcache"
)

type recordingClickPublisher struct{ events []models.ClickEvent }

func (p *recordingClickPublisher) Publish(_ context.Context, event models.ClickEvent) error {
	p.events = append(p.events, event)
	return nil
}

type staticRedirectCache struct{ value redirectcache.CachedRedirect }

func (c *staticRedirectCache) Get(context.Context, string, string) (redirectcache.CachedRedirect, bool, error) {
	return c.value, true, nil
}
func (c *staticRedirectCache) Set(context.Context, string, string, redirectcache.CachedRedirect, time.Duration) error {
	return nil
}
func (c *staticRedirectCache) Delete(context.Context, string, string) error { return nil }

func TestRedirectEventHasStableAnalyticsIdentityFields(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodGet, "https://sho.rt/example", nil)
	before := time.Now().UTC()
	event := h.redirectEvent(req, models.Link{ID: 42, Slug: "example"}, "https://example.com", "default", http.StatusFound)
	after := time.Now().UTC()

	if !regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`).MatchString(event.EventID) {
		t.Fatalf("event ID is not UUID v4: %q", event.EventID)
	}
	if event.OccurredAt.Before(before) || event.OccurredAt.After(after) {
		t.Fatalf("occurred_at outside event creation window: %v", event.OccurredAt)
	}
}

func TestEligibleRedirectPublishesOnce(t *testing.T) {
	link := models.Link{ID: 42, Slug: "example", TargetURL: "https://destination.example/path", RedirectCode: http.StatusFound}
	cached, eligible := redirectcache.FromLink(link)
	if !eligible {
		t.Fatal("test link is not cache eligible")
	}
	publisher := &recordingClickPublisher{}
	resolver := redirectcache.NewResolver(&staticRedirectCache{value: cached}, nil)
	h := NewWithInfrastructure(config.Config{BaseURL: "https://sho.rt", ClickQueueEnabled: true}, nil, nil, resolver, nil, publisher, &clickqueue.Metrics{})
	req := httptest.NewRequest(http.MethodGet, "https://sho.rt/example", nil)
	response := httptest.NewRecorder()

	h.redirectSlug(response, req, "example")

	if response.Code != http.StatusFound || response.Header().Get("Location") != link.TargetURL {
		t.Fatalf("response status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	if len(publisher.events) != 1 || publisher.events[0].LinkID != link.ID || !publisher.events[0].Increment {
		t.Fatalf("published events = %#v", publisher.events)
	}
}
