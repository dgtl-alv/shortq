package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"shortq/internal/models"
)

func TestAPIKeyCannotMintAPIKey(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", strings.NewReader(`{"name":"replacement"}`))
	req = req.WithContext(withPrincipal(req.Context(), effectivePrincipal(models.User{ID: 7, Role: "customer", Active: true}, "user", "api_key")))
	res := httptest.NewRecorder()
	h.apiKeys(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestDecodeRejectsOversizedJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":"`+strings.Repeat("x", 1<<20)+`"}`))
	res := httptest.NewRecorder()
	var in map[string]string
	if decode(res, req, &in) || res.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("decode = true or status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestSafeCSVCell(t *testing.T) {
	for _, input := range []string{"=cmd", " +SUM(A1:A2)", "\t@evil"} {
		if got := safeCSVCell(input); !strings.HasPrefix(got, "'") {
			t.Errorf("safeCSVCell(%q) = %q", input, got)
		}
	}
	if got := safeCSVCell("ordinary"); got != "ordinary" {
		t.Fatalf("ordinary cell changed to %q", got)
	}
}

func TestAnalyticsResolvedURLStripsSecrets(t *testing.T) {
	got := analyticsResolvedURL("https://user:pass@example.com/path?token=secret#fragment")
	if got != "https://example.com/path" {
		t.Fatalf("resolved URL = %q", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	res := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
	for name, want := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin"} {
		if got := res.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}
