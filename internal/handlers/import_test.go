package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
