package handlers

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"shortq/internal/models"
)

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
