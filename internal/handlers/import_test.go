package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"shortq/internal/models"
)

func TestImportDryRunAcceptsTargetURLColumn(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links/import?dry_run=true", strings.NewReader("slug,target_url,title\nsmoke-link,https://example.org,Smoke\n"))
	res := httptest.NewRecorder()
	h.importLinks(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"valid":true`) || !strings.Contains(res.Body.String(), `"rows":1`) {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestImportDryRunRejectsConflictingURLAliases(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links/import?dry_run=true", strings.NewReader("slug,url,target_url\nsmoke-link,https://one.example,https://two.example\n"))
	res := httptest.NewRecorder()
	h.importLinks(res, req)
	if res.Code != http.StatusUnprocessableEntity || !strings.Contains(res.Body.String(), "must match") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestImportRequiresDestinationColumn(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/links/import?dry_run=true", strings.NewReader("slug,title\nsmoke-link,Smoke\n"))
	res := httptest.NewRecorder()
	h.importLinks(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "url or target_url") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestAPIKeyCreationRequiresName(t *testing.T) {
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/api-keys", strings.NewReader(`{"name":"   "}`))
	req = req.WithContext(withPrincipal(req.Context(), effectivePrincipal(models.User{ID: 7, Role: "customer"}, "user", "session")))
	res := httptest.NewRecorder()
	h.apiKeys(res, req)
	if res.Code != http.StatusBadRequest || !strings.Contains(res.Body.String(), "1 to 120") {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
}

func TestShortioLinkIDParsing(t *testing.T) {
	for _, raw := range []string{"42", "lnk_shortq_42", "lnk_anything_42"} {
		got, err := parseShortioLinkID(raw)
		if err != nil || got != 42 {
			t.Fatalf("parseShortioLinkID(%q) = %d, %v; want 42, nil", raw, got, err)
		}
	}
}

func TestShortioTimeParsing(t *testing.T) {
	got, err := parseShortioTime("2026-08-26T10:30:00Z")
	if err != nil || got != "2026-08-26T10:30:00Z" {
		t.Fatalf("RFC3339 parse = %q, %v", got, err)
	}
	got, err = parseShortioTime(float64(1798260000000))
	if err != nil {
		t.Fatalf("millis parse error: %v", err)
	}
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("millis result is not RFC3339: %q", got)
	}
}

func TestShortioPayloadMapping(t *testing.T) {
	h := &Handler{}
	skip := true
	redirect := 302
	limit := int64(5)
	path := "campaign"
	android := "https://play.google.com/store/apps/details?id=com.example"
	iphone := "https://apps.apple.com/app/example/id123"
	expired := "https://example.org/expired"
	password := "secret-password"
	link, passwordHash, err := h.shortioCreatePayloadToLink(shortioCreateLinkPayload{
		OriginalURL:  "https://example.org/destination",
		Path:         &path,
		Title:        "Campaign",
		RedirectType: &redirect,
		ExpiresAt:    "2026-08-26T10:30:00Z",
		ExpiredURL:   &expired,
		AndroidURL:   &android,
		IPhoneURL:    &iphone,
		ClicksLimit:  &limit,
		SkipQS:       &skip,
		Password:     &password,
		Tags:         []string{"team", "sms"},
		UTMSource:    "crm",
	})
	if err != nil {
		t.Fatalf("payload mapping failed: %v", err)
	}
	if link.TargetURL != "https://example.org/destination" || link.Slug != "campaign" || link.IOSURL != iphone || link.AndroidURL != android || link.ExpiredURL != expired {
		t.Fatalf("mapped link mismatch: %#v", link)
	}
	if link.ForwardQuery || link.MaxClicks == nil || *link.MaxClicks != 5 || link.UTMSource != "crm" || len(passwordHash) == 0 {
		t.Fatalf("advanced mapping mismatch: %#v hash=%d", link, len(passwordHash))
	}
}
