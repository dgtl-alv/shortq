package handlers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/auth"
	"shortq/internal/config"
	"shortq/internal/store"
)

func TestDocsRoutesRequireSSOSession(t *testing.T) {
	h := &Handler{}
	routes := h.Routes()
	for _, path := range []string{
		"/docs",
		"/docs/",
		"/docs.html",
		"/docs/openapi.yaml",
		"/docs/assets/swagger-ui.css",
		"/docs/assets/swagger-ui-bundle.js",
		"/docs/assets/docs-init.js",
		"/docs/assets/unknown.js",
	} {
		t.Run(path, func(t *testing.T) {
			res := httptest.NewRecorder()
			routes.ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))
			if res.Code != http.StatusFound {
				t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusFound, res.Body.String())
			}
			if got := res.Header().Get("Location"); got != "/auth/microsoft/login" {
				t.Fatalf("Location = %q, want Microsoft login", got)
			}
		})
	}
}

func TestDocsRoutesServeLocalResourcesWithValidSSOSession(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(workingDirectory); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	}()

	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	const secret = "docs-test-session-secret-at-least-32-characters"
	h := &Handler{
		C: config.Config{JWTSecret: secret, OIDCAllowedDomain: "alvaauto.com"},
		S: store.New(database),
	}
	token, err := auth.SignJWT(secret, auth.Claims{UserID: 7, Email: "reviewer@alvaauto.com", Mode: "user", Exp: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	routes := h.Routes()

	resources := []string{
		"/docs",
		"/docs/",
		"/docs/openapi.yaml",
		"/docs/assets/swagger-ui.css",
		"/docs/assets/swagger-ui-bundle.js",
		"/docs/assets/docs-init.js",
	}
	for _, path := range resources {
		t.Run(path, func(t *testing.T) {
			expectDocsUser(mock)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			res := httptest.NewRecorder()
			routes.ServeHTTP(res, req)
			if res.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body = %s", res.Code, http.StatusOK, res.Body.String())
			}
			if res.Body.Len() == 0 {
				t.Fatal("response body is empty")
			}
			if got := res.Header().Get("Cache-Control"); got != "no-store" {
				t.Fatalf("Cache-Control = %q, want no-store", got)
			}
		})
	}

	for path, wantStatus := range map[string]int{
		"/docs.html":              http.StatusPermanentRedirect,
		"/docs/assets/unknown.js": http.StatusNotFound,
	} {
		t.Run(path, func(t *testing.T) {
			expectDocsUser(mock)
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
			res := httptest.NewRecorder()
			routes.ServeHTTP(res, req)
			if res.Code != wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", res.Code, wantStatus, res.Body.String())
			}
		})
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func expectDocsUser(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,tenant_id,email,name,role,deletion_access,active,created_at FROM users WHERE id=$1`)).
		WithArgs(int64(7)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "tenant_id", "email", "name", "role", "deletion_access", "active", "created_at"}).
			AddRow(7, nil, "reviewer@alvaauto.com", "Reviewer", "customer", false, true, time.Now()))
}

func TestDocsCSPAllowsOnlySameOriginScripts(t *testing.T) {
	for _, path := range []string{"/docs", "/docs/", "/docs.html", "/docs/openapi.yaml", "/docs/assets/docs-init.js"} {
		res := httptest.NewRecorder()
		securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(res, httptest.NewRequest(http.MethodGet, path, nil))

		csp := res.Header().Get("Content-Security-Policy")
		if !strings.Contains(csp, "default-src 'none'") || !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "connect-src 'self'") {
			t.Fatalf("%s CSP is not restrictive enough: %q", path, csp)
		}
		if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") || strings.Contains(csp, "script-src https:") {
			t.Fatalf("%s CSP permits unsafe script execution: %q", path, csp)
		}
	}
}

func TestSwaggerUIUsesOnlySelfHostedAssetsAndExternalInit(t *testing.T) {
	document, err := os.ReadFile("../../web/docs.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(document)
	for _, reference := range []string{
		`href="/docs/assets/swagger-ui.css"`,
		`src="/docs/assets/swagger-ui-bundle.js"`,
		`src="/docs/assets/docs-init.js"`,
	} {
		if !strings.Contains(html, reference) {
			t.Errorf("docs.html missing %s", reference)
		}
	}
	if strings.Contains(html, "cdn.jsdelivr.net") || strings.Contains(html, "http://") || strings.Contains(html, "https://") {
		t.Fatal("docs.html still contains a remote asset URL")
	}
	for _, match := range regexp.MustCompile(`(?is)<script(?:\s[^>]*)?>(.*?)</script>`).FindAllStringSubmatch(html, -1) {
		if strings.TrimSpace(match[1]) != "" {
			t.Fatal("docs.html contains inline JavaScript")
		}
		if !strings.Contains(match[0], " src=") {
			t.Fatal("docs.html contains a script without a local src")
		}
	}
	for _, asset := range []string{
		"../../web/docs-assets/swagger-ui.css",
		"../../web/docs-assets/swagger-ui-bundle.js",
		"../../web/docs-assets/docs-init.js",
		"../../web/docs-assets/LICENSE",
		"../../web/docs-assets/NOTICE",
		"../../web/docs-assets/swagger-ui-bundle.js.LICENSE.txt",
		"../../web/docs-assets/VERSION",
	} {
		info, err := os.Stat(asset)
		if err != nil {
			t.Errorf("required local asset %s: %v", asset, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("required local asset %s is empty", asset)
		}
	}
}
