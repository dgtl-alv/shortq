package handlers

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/skip2/go-qrcode"

	"shortq/internal/auth"
	"shortq/internal/config"
	"shortq/internal/models"
	"shortq/internal/store"
)

type Handler struct {
	C   config.Config
	S   *store.Store
	Web fs.FS
}

func New(c config.Config, s *store.Store, web fs.FS) *Handler { return &Handler{C: c, S: s, Web: web} }

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.health)
	mux.HandleFunc("/docs/openapi.yaml", h.openapi)
	mux.HandleFunc("/docs", h.swagger)
	mux.HandleFunc("/api/v1/auth/register", h.register)
	mux.HandleFunc("/api/v1/auth/login", h.login)
	mux.HandleFunc("/api/v1/auth/forgot-password", h.forgot)
	mux.HandleFunc("/api/v1/auth/change-password", h.withAuth(h.changePassword))
	mux.HandleFunc("/auth/microsoft/login", h.microsoftLogin)
	mux.HandleFunc("/auth/microsoft/callback", h.microsoftCallback)
	mux.HandleFunc("/auth/logout", h.microsoftLogout)
	mux.HandleFunc("/api/v1/me", h.withAuth(h.me))
	mux.HandleFunc("/api/v1/analytics", h.withAuth(h.analytics))
	mux.HandleFunc("/api/v1/tenants", h.withAuth(h.tenants))
	mux.HandleFunc("/api/v1/domains", h.withAuth(h.domains))
	mux.HandleFunc("/api/v1/domains/", h.withAuth(h.domainByID))
	mux.HandleFunc("/api/v1/customers", h.withAuth(h.customers))
	mux.HandleFunc("/api/v1/links", h.withAuth(h.links))
	mux.HandleFunc("/api/v1/links/", h.withAuth(h.linkByID))
	mux.HandleFunc("/api/v1/qr", h.qr)
	mux.HandleFunc("/api/v1/api-keys", h.withAuth(h.apiKeys))
	mux.HandleFunc("/api/v1/api-keys/", h.withAuth(h.apiKeyByID))
	mux.HandleFunc("/r/", h.redirectLegacy)
	mux.HandleFunc("/", h.web)
	return logReq(mux)
}

func (h *Handler) web(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if r.URL.Path == "/" || r.URL.Path == "/index.html" {
		if _, ok := h.userFromSession(r); !ok {
			http.Redirect(w, r, "/auth/microsoft/login", http.StatusFound)
			return
		}
		http.FileServer(http.FS(h.Web)).ServeHTTP(w, r)
		return
	}
	if isReservedRootPath(r.URL.Path) {
		http.FileServer(http.FS(h.Web)).ServeHTTP(w, r)
		return
	}
	h.redirectSlug(w, r, strings.Trim(r.URL.Path, "/"))
}

func isReservedRootPath(path string) bool {
	clean := strings.Trim(path, "/")
	if clean == "" || strings.Contains(clean, "/") {
		return true
	}
	reserved := map[string]bool{
		"app.js": true, "style.css": true, "docs.html": true, "favicon.ico": true, "robots.txt": true,
	}
	return reserved[clean] || strings.Contains(clean, ".")
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	jsonOut(w, 200, map[string]string{"status": "ok"})
}
func (h *Handler) openapi(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "docs/openapi.yaml")
}
func (h *Handler) swagger(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/docs.html")
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password registration disabled; use Microsoft SSO")
	return

	var in struct{ Email, Name, Password string }
	if !decode(w, r, &in) {
		return
	}
	if len(in.Password) < 8 {
		errOut(w, 400, "password min 8 chars")
		return
	}
	hp, _ := auth.HashPassword(in.Password)
	if err := h.S.CreateUser(in.Email, in.Name, "customer", nil, hp); err != nil {
		errOut(w, 400, "email already used")
		return
	}
	jsonOut(w, 201, map[string]string{"message": "registered"})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password login disabled; use Microsoft SSO")
	return

	var in struct{ Email, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, p, err := h.S.UserByEmail(in.Email)
	if err != nil || !auth.CheckPassword(p, in.Password) {
		errOut(w, 401, "invalid credentials")
		return
	}
	tok, _ := auth.SignJWT(h.C.JWTSecret, auth.Claims{UserID: u.ID, Email: u.Email, Exp: auth.TokenTTL()})
	jsonOut(w, 200, map[string]any{"token": tok, "user": u})
}

func (h *Handler) forgot(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password reset disabled; use Microsoft SSO")
	return

	var in struct{ Email string }
	if !decode(w, r, &in) {
		return
	}
	if u, _, err := h.S.UserByEmail(in.Email); err == nil {
		tok, _ := auth.NewToken(32)
		_ = h.S.SaveReset(u.ID, auth.SHA256Hex(tok), auth.ResetExpiry())
		fmt.Printf("password reset for %s: %s/reset?token=%s\n", u.Email, h.C.BaseURL, tok)
	}
	jsonOut(w, 200, map[string]string{"message": "if email exists, reset link generated"})
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password change disabled; use Microsoft SSO")
	return

	var in struct{ Token, OldPassword, NewPassword string }
	if !decode(w, r, &in) {
		return
	}
	if len(in.NewPassword) < 8 {
		errOut(w, 400, "password min 8 chars")
		return
	}
	uid := int64(0)
	if in.Token != "" {
		id, err := h.S.ConsumeReset(auth.SHA256Hex(in.Token))
		if err != nil {
			errOut(w, 400, "invalid reset token")
			return
		}
		uid = id
	} else {
		u, ok := userFrom(r.Context())
		if !ok {
			errOut(w, 401, "auth required")
			return
		}
		_, p, _ := h.S.UserByEmail(u.Email)
		if !auth.CheckPassword(p, in.OldPassword) {
			errOut(w, 401, "old password invalid")
			return
		}
		uid = u.ID
	}
	hp, _ := auth.HashPassword(in.NewPassword)
	_ = h.S.UpdatePassword(uid, hp)
	jsonOut(w, 200, map[string]string{"message": "password changed"})
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	jsonOut(w, 200, u)
}
func (h *Handler) analytics(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	a, err := h.S.Analytics(u)
	if err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, a)
}

func (h *Handler) tenants(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	if u.Role != "superadmin" {
		errOut(w, 403, "superadmin only")
		return
	}
	if r.Method == "GET" {
		xs, err := h.S.ListTenants()
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, xs)
		return
	}
	if r.Method == "POST" {
		var in struct{ Name, Slug string }
		if !decode(w, r, &in) {
			return
		}
		t, err := h.S.CreateTenant(in.Name, in.Slug)
		if err != nil {
			errOut(w, 400, err.Error())
			return
		}
		jsonOut(w, 201, t)
		return
	}
	w.WriteHeader(405)
}

func (h *Handler) customers(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	if u.Role == "customer" {
		errOut(w, 403, "tenant or superadmin only")
		return
	}
	if r.Method == "GET" {
		xs, err := h.S.ListUsers(u)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, xs)
		return
	}
	if r.Method == "POST" {
		var in struct {
			Email, Name, Password, Role string
			TenantID                    *int64 `json:"tenant_id"`
		}
		if !decode(w, r, &in) {
			return
		}
		role := in.Role
		if role == "" {
			role = "customer"
		}
		if u.Role == "tenant" {
			role = "customer"
			in.TenantID = u.TenantID
		}
		if u.Role == "superadmin" && role == "superadmin" {
			errOut(w, 400, "superadmin creation disabled here")
			return
		}
		if role == "customer" || role == "tenant" {
			if in.TenantID == nil {
				errOut(w, 400, "tenant_id required")
				return
			}
		} else {
			errOut(w, 400, "role must be tenant or customer")
			return
		}
		if len(in.Password) < 8 {
			errOut(w, 400, "password min 8 chars")
			return
		}
		hp, _ := auth.HashPassword(in.Password)
		if err := h.S.CreateUser(in.Email, in.Name, role, in.TenantID, hp); err != nil {
			errOut(w, 400, err.Error())
			return
		}
		jsonOut(w, 201, map[string]string{"message": "user created"})
		return
	}
	w.WriteHeader(405)
}

func (h *Handler) links(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	switch r.Method {
	case "GET":
		xs, err := h.S.ListLinks(u)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		for i := range xs {
			xs[i].ShortURL = h.shortURL(xs[i])
		}
		jsonOut(w, 200, xs)
	case "POST":
		var in struct {
			URL, Slug, Title string `json:",omitempty"`
		}
		if !decode(w, r, &in) {
			return
		}
		l, err := h.S.CreateLink(u, in.Slug, in.URL, in.Title)
		if err != nil {
			errOut(w, 400, err.Error())
			return
		}
		l.ShortURL = h.shortURL(l)
		jsonOut(w, 201, l)
	default:
		w.WriteHeader(405)
	}
}

func (h *Handler) linkByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		w.WriteHeader(405)
		return
	}
	u, _ := userFrom(r.Context())
	id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/links/"), 10, 64)
	_ = h.S.DeleteLink(u, id)
	w.WriteHeader(204)
}
func (h *Handler) qr(w http.ResponseWriter, r *http.Request) {
	text := r.URL.Query().Get("text")
	if text == "" {
		text = r.URL.Query().Get("url")
	}
	if text == "" {
		errOut(w, 400, "text or url required")
		return
	}
	png, err := qrcode.Encode(text, qrcode.Medium, 256)
	if err != nil {
		errOut(w, 500, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(png)
}
func (h *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	switch r.Method {
	case "GET":
		ks, err := h.S.ListAPIKeys(u.ID)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, ks)
	case "POST":
		var in struct{ Name string }
		if !decode(w, r, &in) {
			return
		}
		key, prefix, err := auth.NewAPIKey()
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		if err := h.S.CreateAPIKey(u.ID, in.Name, auth.SHA256Hex(key), prefix); err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 201, map[string]string{"key": key, "prefix": prefix})
	default:
		w.WriteHeader(405)
	}
}
func (h *Handler) apiKeyByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		w.WriteHeader(405)
		return
	}
	u, _ := userFrom(r.Context())
	id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/api-keys/"), 10, 64)
	_ = h.S.RevokeAPIKey(u.ID, id)
	w.WriteHeader(204)
}
func (h *Handler) redirectLegacy(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimPrefix(r.URL.Path, "/r/")
	h.redirectSlug(w, r, slug)
}

func (h *Handler) redirectSlug(w http.ResponseWriter, r *http.Request, slug string) {
	if slug == "" || strings.Contains(slug, "/") || isReservedRootPath("/"+slug) {
		http.NotFound(w, r)
		return
	}
	l, err := h.S.LinkBySlug(slug)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	host := strings.Trim(strings.ToLower(r.Host), ".")
	if strings.Contains(host, ":") {
		host = strings.Split(host, ":")[0]
	}
	if host != "" && host != h.baseHost() {
		d, err := h.S.TenantDomainByDomain(host)
		if err != nil || d.Status != "active" || l.TenantID == nil || d.TenantID != *l.TenantID {
			http.NotFound(w, r)
			return
		}
	}
	h.S.TrackClick(l.ID, r.RemoteAddr, r.UserAgent(), r.Referer())
	http.Redirect(w, r, l.TargetURL, 302)
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("X-API-Key"); key != "" {
			u, err := h.S.UserByAPIKey(auth.SHA256Hex(key))
			if err == nil {
				next(w, r.WithContext(withUser(r.Context(), u)))
				return
			}
		}
		if u, ok := h.userFromSession(r); ok {
			next(w, r.WithContext(withUser(r.Context(), u)))
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, err := auth.ParseJWT(h.C.JWTSecret, bearer)
		if err != nil {
			ssoRequired(w)
			return
		}
		u, err := h.S.UserByID(c.UserID)
		if err != nil {
			errOut(w, 401, "auth required")
			return
		}
		next(w, r.WithContext(withUser(r.Context(), u)))
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		errOut(w, 400, "bad json")
		return false
	}
	return true
}
func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func errOut(w http.ResponseWriter, code int, msg string) {
	jsonOut(w, code, map[string]string{"error": msg})
}
func logReq(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		fmt.Printf("%s %s %s\n", r.Method, r.URL.Path, time.Since(start))
	})
}

var _ = models.User{}

func (h *Handler) domains(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	switch r.Method {
	case "GET":
		xs, err := h.S.ListTenantDomains(u)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, xs)
	case "POST":
		var in struct {
			TenantID int64  `json:"tenant_id"`
			Domain   string `json:"domain"`
		}
		if !decode(w, r, &in) {
			return
		}
		if u.Role == "tenant" && u.TenantID != nil {
			in.TenantID = *u.TenantID
		}
		tok, _ := auth.NewToken(18)
		d, err := h.S.CreateTenantDomain(u, in.TenantID, in.Domain, tok)
		if err != nil {
			errOut(w, 400, err.Error())
			return
		}
		jsonOut(w, 201, d)
	default:
		w.WriteHeader(405)
	}
}

func (h *Handler) domainByID(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/domains/")
	verify := strings.HasSuffix(path, "/verify")
	path = strings.TrimSuffix(path, "/verify")
	id, _ := strconv.ParseInt(strings.Trim(path, "/"), 10, 64)
	if verify && r.Method == "POST" {
		d, err := h.S.TenantDomainByID(u, id)
		if err != nil {
			errOut(w, 404, "domain not found")
			return
		}
		if !h.verifyDomain(d) {
			errOut(w, 400, "DNS not verified yet")
			return
		}
		_ = h.S.ActivateTenantDomain(id)
		jsonOut(w, 200, map[string]string{"message": "domain verified"})
		return
	}
	if r.Method == "DELETE" {
		_ = h.S.DeleteTenantDomain(u, id)
		w.WriteHeader(204)
		return
	}
	w.WriteHeader(405)
}

func (h *Handler) verifyDomain(d models.TenantDomain) bool {
	baseHost := h.baseHost()
	if baseHost == "" {
		return false
	}
	if cname, err := net.LookupCNAME(d.Domain); err == nil && strings.Trim(strings.ToLower(cname), ".") == baseHost {
		return true
	}
	txts, _ := net.LookupTXT("_shortq." + d.Domain)
	want := "shortq-verify=" + d.VerificationToken
	for _, t := range txts {
		if t == want {
			return true
		}
	}
	return false
}

func (h *Handler) shortURL(l models.Link) string {
	if domain, err := h.S.ActiveDomainForTenant(l.TenantID); err == nil && domain != "" {
		return "https://" + domain + "/" + l.Slug
	}
	return strings.TrimRight(h.C.BaseURL, "/") + "/" + l.Slug
}

func (h *Handler) baseHost() string {
	u, err := url.Parse(h.C.BaseURL)
	if err != nil {
		return ""
	}
	host := u.Hostname()
	if host == "" {
		host = strings.TrimPrefix(strings.TrimPrefix(h.C.BaseURL, "https://"), "http://")
	}
	return strings.Trim(strings.ToLower(host), ".")
}
