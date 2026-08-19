package handlers

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"

	"shortq/internal/auth"
	"shortq/internal/config"
	"shortq/internal/models"
	"shortq/internal/store"
)

type Handler struct {
	C          config.Config
	S          *store.Store
	Web        fs.FS
	attemptMu  sync.Mutex
	attempts   map[string][]time.Time
	attemptOps uint64
	qrSlots    chan struct{}
}

func New(c config.Config, s *store.Store, web fs.FS) *Handler {
	return &Handler{C: c, S: s, Web: web, attempts: map[string][]time.Time{}, qrSlots: make(chan struct{}, 2)}
}

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
	mux.HandleFunc("/api/v1/session/mode", h.sessionMode)
	mux.HandleFunc("/api/v1/analytics", h.withAuth(h.analytics))
	mux.HandleFunc("/api/v1/analytics/timeseries", h.withAuth(h.analyticsTimeseries))
	mux.HandleFunc("/api/v1/analytics/breakdown", h.withAuth(h.analyticsBreakdown))
	mux.HandleFunc("/api/v1/clicks", h.withAuth(h.clicks))
	mux.HandleFunc("/api/v1/tenants", h.withAuth(h.tenants))
	mux.HandleFunc("/api/v1/domains", h.withAuth(h.domains))
	mux.HandleFunc("/api/v1/domains/", h.withAuth(h.domainByID))
	mux.HandleFunc("/api/v1/customers", h.withAuth(h.customers))
	mux.HandleFunc("/api/v1/customers/", h.withAuth(h.customerByID))
	mux.HandleFunc("/api/v1/audit-events", h.withAuth(h.auditEvents))
	mux.HandleFunc("/api/v1/links", h.withAuth(h.links))
	mux.HandleFunc("/api/v1/links/page", h.withAuth(h.linksPage))
	mux.HandleFunc("/api/v1/links/import", h.withAuth(h.importLinks))
	mux.HandleFunc("/api/v1/links/export.csv", h.withAuth(h.exportLinks))
	mux.HandleFunc("/api/v1/links/bulk", h.withAuth(h.bulkLinks))
	mux.HandleFunc("/api/v1/links/", h.withAuth(h.linkByID))
	mux.HandleFunc("/api/v1/qr", h.qr)
	mux.HandleFunc("/api/v1/api-keys", h.withAuth(h.apiKeys))
	mux.HandleFunc("/api/v1/api-keys/", h.withAuth(h.apiKeyByID))
	mux.HandleFunc("/r/", h.redirectLegacy)
	mux.HandleFunc("/", h.web)
	return securityHeaders(logReq(mux))
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
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password login disabled; use Microsoft SSO")
}

func (h *Handler) forgot(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password reset disabled; use Microsoft SSO")
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	errOut(w, 410, "password change disabled; use Microsoft SSO")
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	jsonOut(w, 200, mePayload(p))
}

func mePayload(p principal) map[string]any {
	return map[string]any{
		"id": p.Effective.ID, "tenant_id": p.Effective.TenantID, "email": p.Effective.Email,
		"name": p.Effective.Name, "role": p.Effective.Role, "created_at": p.Effective.CreatedAt,
		"actual_role": p.Actual.Role, "dashboard_mode": p.Mode, "can_switch_mode": canSwitchMode(p.Actual) && p.AuthType == "session",
		"deletion_access": p.Actual.DeletionAccess, "active": p.Actual.Active, "can_delete": canDelete(p),
	}
}

func (h *Handler) audit(r *http.Request, action, targetType, targetID, outcome string, before, after map[string]any) error {
	p, ok := principalFrom(r.Context())
	if !ok {
		return fmt.Errorf("missing audit principal")
	}
	id := p.Actual.ID
	return h.S.RecordAudit(models.AuditEvent{ActorUserID: &id, ActorEmail: p.Actual.Email, AuthType: p.AuthType, Action: action, TargetType: targetType, TargetID: targetID, Outcome: outcome, Before: before, After: after, IPAddress: visitorIP(r), UserAgent: r.UserAgent()})
}

func (h *Handler) requireDelete(w http.ResponseWriter, r *http.Request, action, targetType, targetID string) bool {
	p, _ := principalFrom(r.Context())
	if canDelete(p) {
		return true
	}
	if err := h.audit(r, action, targetType, targetID, "denied", nil, nil); err != nil {
		errOut(w, 500, "audit log unavailable")
		return false
	}
	errOut(w, http.StatusForbidden, "deletion access required")
	return false
}

func userAudit(u models.User) map[string]any {
	return map[string]any{"id": u.ID, "email": u.Email, "name": u.Name, "role": u.Role, "tenant_id": u.TenantID, "deletion_access": u.DeletionAccess, "active": u.Active}
}

func linkAudit(l models.Link) map[string]any {
	return map[string]any{"id": l.ID, "slug": l.Slug, "target_url": l.TargetURL, "title": l.Title, "redirect_code": l.RedirectCode, "expires_at": l.ExpiresAt, "max_clicks": l.MaxClicks, "password_protected": l.PasswordProtected, "tags": l.Tags}
}

func domainAudit(d models.TenantDomain) map[string]any {
	return map[string]any{"id": d.ID, "tenant_id": d.TenantID, "domain": d.Domain, "status": d.Status, "not_found_url": d.NotFoundURL}
}

func (h *Handler) auditEvents(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Effective.Role != "superadmin" {
		errOut(w, 403, "superadmin only")
		return
	}
	if r.Method != http.MethodGet {
		w.WriteHeader(405)
		return
	}
	page, err := h.S.ListAuditEvents(r.URL.Query())
	if err != nil {
		errOut(w, 400, err.Error())
		return
	}
	jsonOut(w, 200, page)
}

func (h *Handler) customerByID(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	if p.Effective.Role != "superadmin" {
		errOut(w, 403, "superadmin only")
		return
	}
	if r.Method == http.MethodPatch && strings.HasSuffix(r.URL.Path, "/active") {
		raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/customers/"), "/active")
		id, err := strconv.ParseInt(strings.Trim(raw, "/"), 10, 64)
		if err != nil || id < 1 {
			errOut(w, http.StatusBadRequest, "invalid user id")
			return
		}
		var in struct {
			Active bool `json:"active"`
		}
		if !decode(w, r, &in) {
			return
		}
		before, err := h.S.UserByID(id)
		if err != nil {
			errOut(w, http.StatusNotFound, "user not found")
			return
		}
		after, err := h.S.SetUserActive(id, in.Active)
		if err != nil {
			errOut(w, http.StatusInternalServerError, "user status update failed")
			return
		}
		if err := h.audit(r, "user.active_changed", "user", strconv.FormatInt(id, 10), "success", userAudit(before), userAudit(after)); err != nil {
			errOut(w, http.StatusInternalServerError, "audit log unavailable")
			return
		}
		jsonOut(w, http.StatusOK, after)
		return
	}
	if r.Method != http.MethodPatch || !strings.HasSuffix(r.URL.Path, "/deletion-access") {
		w.WriteHeader(405)
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/customers/"), "/deletion-access")
	id, err := strconv.ParseInt(strings.Trim(raw, "/"), 10, 64)
	if err != nil || id < 1 {
		errOut(w, 400, "invalid user id")
		return
	}
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &in) {
		return
	}
	before, err := h.S.UserByID(id)
	if err != nil {
		errOut(w, 404, "user not found")
		return
	}
	after, err := h.S.SetDeletionAccess(id, in.Enabled)
	if err != nil {
		errOut(w, 500, err.Error())
		return
	}
	if err := h.audit(r, "user.deletion_access_changed", "user", strconv.FormatInt(id, 10), "success", userAudit(before), userAudit(after)); err != nil {
		errOut(w, 500, "audit log unavailable")
		return
	}
	jsonOut(w, 200, after)
}

func (h *Handler) sessionMode(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	p, ok := h.userFromSession(r)
	if !ok {
		ssoRequired(w)
		return
	}
	if !canSwitchMode(p.Actual) {
		errOut(w, http.StatusForbidden, "admin account required")
		return
	}
	var in struct {
		Mode string `json:"mode"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Mode != "admin" && in.Mode != "user" {
		errOut(w, http.StatusBadRequest, "mode must be admin or user")
		return
	}
	signed, err := auth.SignJWT(h.C.JWTSecret, auth.Claims{UserID: p.Actual.ID, Email: p.Actual.Email, Mode: in.Mode, Exp: auth.TokenTTL()})
	if err != nil {
		errOut(w, http.StatusInternalServerError, "session update failed")
		return
	}
	setSessionCookie(w, signed)
	jsonOut(w, http.StatusOK, mePayload(effectivePrincipal(p.Actual, in.Mode, "session")))
}
func (h *Handler) analytics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, _ := userFrom(r.Context())
	a, err := h.S.Analytics(u)
	if err != nil {
		errOut(w, 500, err.Error())
		return
	}
	jsonOut(w, 200, a)
}

func (h *Handler) clicks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, _ := userFrom(r.Context())
	page, err := h.S.ListClicks(u, r.URL.Query())
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, page)
}

func (h *Handler) analyticsTimeseries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, _ := userFrom(r.Context())
	points, err := h.S.AnalyticsTimeseries(u, r.URL.Query())
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, points)
}

func (h *Handler) analyticsBreakdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, _ := userFrom(r.Context())
	points, err := h.S.AnalyticsBreakdown(u, r.URL.Query().Get("group_by"), r.URL.Query())
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	jsonOut(w, http.StatusOK, points)
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
		errOut(w, http.StatusConflict, "the ALVA department is fixed")
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
		created, _, _ := h.S.UserByEmail(in.Email)
		if err := h.audit(r, "user.created", "user", strconv.FormatInt(created.ID, 10), "success", nil, userAudit(created)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, 201, map[string]string{"message": "user created"})
		return
	}
	w.WriteHeader(405)
}

type linkPayload struct {
	URL            *string             `json:"url"`
	TargetURL      *string             `json:"target_url"`
	Slug           *string             `json:"slug"`
	Title          *string             `json:"title"`
	RedirectCode   *int                `json:"redirect_code"`
	ExpiresAt      *string             `json:"expires_at"`
	MaxClicks      *int64              `json:"max_clicks"`
	ClearMaxClicks bool                `json:"clear_max_clicks"`
	ExpiredURL     *string             `json:"expired_url"`
	IOSURL         *string             `json:"ios_url"`
	AndroidURL     *string             `json:"android_url"`
	ForwardQuery   *bool               `json:"forward_query"`
	UTMSource      *string             `json:"utm_source"`
	UTMMedium      *string             `json:"utm_medium"`
	UTMCampaign    *string             `json:"utm_campaign"`
	UTMTerm        *string             `json:"utm_term"`
	UTMContent     *string             `json:"utm_content"`
	Tags           *[]string           `json:"tags"`
	GeoTargets     *[]models.GeoTarget `json:"geo_targets"`
	Password       *string             `json:"password"`
	ClearPassword  bool                `json:"clear_password"`
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
		h.addShortURLs(xs)
		jsonOut(w, 200, xs)
	case "POST":
		var in linkPayload
		if !decode(w, r, &in) {
			return
		}
		link, passwordHash, _, err := applyLinkPayload(models.Link{ForwardQuery: true, RedirectCode: 302}, in, true)
		if err != nil {
			errOut(w, http.StatusBadRequest, err.Error())
			return
		}
		l, err := h.S.CreateLink(u, link, passwordHash)
		if err != nil {
			errOut(w, 400, err.Error())
			return
		}
		if err := h.audit(r, "link.created", "link", strconv.FormatInt(l.ID, 10), "success", nil, linkAudit(l)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		l.ShortURL = h.shortURL(l)
		jsonOut(w, 201, l)
	default:
		w.WriteHeader(405)
	}
}

func (h *Handler) linksPage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			errOut(w, http.StatusBadRequest, "limit must be 1 to 500")
			return
		}
		limit = parsed
	}
	var cursor int64
	if raw := r.URL.Query().Get("cursor"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 {
			errOut(w, http.StatusBadRequest, "cursor must be a positive link id")
			return
		}
		cursor = parsed
	}
	u, _ := userFrom(r.Context())
	page, err := h.S.ListLinksPage(u, limit, cursor)
	if err != nil {
		errOut(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.addShortURLs(page.Items)
	jsonOut(w, http.StatusOK, page)
}

func (h *Handler) linkByID(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/links/"), 10, 64)
	switch r.Method {
	case "GET":
		link, err := h.S.LinkByID(u, id)
		if err != nil {
			errOut(w, http.StatusNotFound, "link not found")
			return
		}
		link.ShortURL = h.shortURL(link)
		jsonOut(w, http.StatusOK, link)
	case "PUT", "PATCH":
		var in linkPayload
		if !decode(w, r, &in) {
			return
		}
		if in.Slug != nil {
			errOut(w, http.StatusBadRequest, "slug is immutable")
			return
		}
		current, err := h.S.LinkByID(u, id)
		if err != nil {
			errOut(w, http.StatusNotFound, "link not found")
			return
		}
		link, passwordHash, passwordChanged, err := applyLinkPayload(current, in, false)
		if err != nil {
			errOut(w, http.StatusBadRequest, err.Error())
			return
		}
		l, err := h.S.UpdateLink(u, id, link, passwordHash, passwordChanged)
		if err != nil {
			errOut(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.audit(r, "link.updated", "link", strconv.FormatInt(id, 10), "success", linkAudit(current), linkAudit(l)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		l.ShortURL = h.shortURL(l)
		jsonOut(w, 200, l)
	case "DELETE":
		if !h.requireDelete(w, r, "link.deleted", "link", strconv.FormatInt(id, 10)) {
			return
		}
		before, err := h.S.LinkByID(u, id)
		if err != nil {
			errOut(w, 404, "link not found")
			return
		}
		if err := h.S.DeleteLink(u, id); err != nil {
			errOut(w, 404, "link not found")
			return
		}
		if err := h.audit(r, "link.deleted", "link", strconv.FormatInt(id, 10), "success", linkAudit(before), nil); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		w.WriteHeader(204)
	default:
		w.WriteHeader(405)
	}
}

func applyLinkPayload(link models.Link, in linkPayload, creating bool) (models.Link, []byte, bool, error) {
	if in.URL != nil && in.TargetURL != nil && strings.TrimSpace(*in.URL) != strings.TrimSpace(*in.TargetURL) {
		return link, nil, false, fmt.Errorf("url and target_url must match when both are supplied")
	}
	if in.URL != nil {
		link.TargetURL = strings.TrimSpace(*in.URL)
	}
	if in.TargetURL != nil {
		link.TargetURL = strings.TrimSpace(*in.TargetURL)
	}
	if in.Slug != nil && creating {
		link.Slug = strings.TrimSpace(*in.Slug)
	}
	if in.Title != nil {
		link.Title = strings.TrimSpace(*in.Title)
	}
	if in.RedirectCode != nil {
		link.RedirectCode = *in.RedirectCode
	}
	if in.ExpiresAt != nil {
		if strings.TrimSpace(*in.ExpiresAt) == "" {
			link.ExpiresAt = nil
		} else {
			parsed, err := time.Parse(time.RFC3339, *in.ExpiresAt)
			if err != nil {
				return link, nil, false, fmt.Errorf("expires_at must be RFC3339")
			}
			parsed = parsed.UTC()
			link.ExpiresAt = &parsed
		}
	}
	if in.ClearMaxClicks {
		link.MaxClicks = nil
	} else if in.MaxClicks != nil {
		link.MaxClicks = in.MaxClicks
	}
	if in.ExpiredURL != nil {
		link.ExpiredURL = strings.TrimSpace(*in.ExpiredURL)
	}
	if in.IOSURL != nil {
		link.IOSURL = strings.TrimSpace(*in.IOSURL)
	}
	if in.AndroidURL != nil {
		link.AndroidURL = strings.TrimSpace(*in.AndroidURL)
	}
	if in.ForwardQuery != nil {
		link.ForwardQuery = *in.ForwardQuery
	}
	for dst, src := range map[*string]*string{&link.UTMSource: in.UTMSource, &link.UTMMedium: in.UTMMedium, &link.UTMCampaign: in.UTMCampaign, &link.UTMTerm: in.UTMTerm, &link.UTMContent: in.UTMContent} {
		if src != nil {
			*dst = strings.TrimSpace(*src)
		}
	}
	if in.Tags != nil {
		link.Tags = *in.Tags
	}
	if in.GeoTargets != nil {
		link.GeoTargets = *in.GeoTargets
	}
	passwordChanged := in.Password != nil || in.ClearPassword
	var passwordHash []byte
	if in.Password != nil {
		if len(*in.Password) < 8 {
			return link, nil, false, fmt.Errorf("password must be at least 8 characters")
		}
		var err error
		passwordHash, err = auth.HashSecret(*in.Password)
		if err != nil {
			return link, nil, false, err
		}
		link.PasswordProtected = true
	} else if in.ClearPassword {
		link.PasswordProtected = false
	}
	if creating && link.TargetURL == "" {
		return link, nil, false, fmt.Errorf("url is required")
	}
	return link, passwordHash, passwordChanged, nil
}

var csvLinkHeaders = []string{"slug", "url", "title", "redirect_code", "expires_at", "max_clicks", "expired_url", "ios_url", "android_url", "forward_query", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "tags", "geo_targets"}

func (h *Handler) exportLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	u, _ := userFrom(r.Context())
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="shortq-links.csv"`)
	writer := csv.NewWriter(w)
	_ = writer.Write(csvLinkHeaders)
	var cursor int64
	for {
		page, err := h.S.ListLinksPage(u, 500, cursor)
		if err != nil {
			return
		}
		for _, link := range page.Items {
			expiresAt, maxClicks := "", ""
			if link.ExpiresAt != nil {
				expiresAt = link.ExpiresAt.UTC().Format(time.RFC3339)
			}
			if link.MaxClicks != nil {
				maxClicks = strconv.FormatInt(*link.MaxClicks, 10)
			}
			geo, _ := json.Marshal(link.GeoTargets)
			row := []string{link.Slug, link.TargetURL, link.Title, strconv.Itoa(link.RedirectCode), expiresAt, maxClicks, link.ExpiredURL, link.IOSURL, link.AndroidURL, strconv.FormatBool(link.ForwardQuery), link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent, strings.Join(link.Tags, ";"), string(geo)}
			for index := range row {
				row[index] = safeCSVCell(row[index])
			}
			_ = writer.Write(row)
		}
		if page.NextCursor == 0 {
			break
		}
		cursor = page.NextCursor
	}
	writer.Flush()
}

func safeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}

func (h *Handler) importLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20)
	var source io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			errOut(w, http.StatusBadRequest, "invalid multipart upload")
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			errOut(w, http.StatusBadRequest, "file field required")
			return
		}
		defer file.Close()
		source = file
	}
	reader := csv.NewReader(source)
	reader.TrimLeadingSpace = true
	rows, err := reader.ReadAll()
	if err != nil || len(rows) < 1 {
		errOut(w, http.StatusBadRequest, "invalid or empty CSV")
		return
	}
	if len(rows) > 10001 {
		errOut(w, http.StatusBadRequest, "CSV may contain at most 10000 links")
		return
	}
	header := map[string]int{}
	for index, name := range rows[0] {
		header[strings.ToLower(strings.TrimSpace(name))] = index
	}
	_, hasURL := header["url"]
	_, hasTargetURL := header["target_url"]
	if !hasURL && !hasTargetURL {
		errOut(w, http.StatusBadRequest, "CSV requires a url or target_url column")
		return
	}
	value := func(row []string, name string) string {
		index, ok := header[name]
		if !ok || index >= len(row) {
			return ""
		}
		return strings.TrimSpace(row[index])
	}
	parsed := make([]models.Link, 0, len(rows)-1)
	errorsByRow := map[int]string{}
	for rowNumber, row := range rows[1:] {
		urlValue, targetURLValue := value(row, "url"), value(row, "target_url")
		if urlValue != "" && targetURLValue != "" && urlValue != targetURLValue {
			errorsByRow[rowNumber+2] = "url and target_url must match when both are provided"
		}
		if targetURLValue == "" {
			targetURLValue = urlValue
		}
		link := models.Link{Slug: value(row, "slug"), TargetURL: targetURLValue, Title: value(row, "title"), RedirectCode: 302, ForwardQuery: true}
		if raw := value(row, "redirect_code"); raw != "" {
			link.RedirectCode, _ = strconv.Atoi(raw)
		}
		if raw := value(row, "expires_at"); raw != "" {
			parsedTime, e := time.Parse(time.RFC3339, raw)
			if e != nil {
				errorsByRow[rowNumber+2] = "expires_at must be RFC3339"
			} else {
				link.ExpiresAt = &parsedTime
			}
		}
		if raw := value(row, "max_clicks"); raw != "" {
			clicks, e := strconv.ParseInt(raw, 10, 64)
			if e != nil {
				errorsByRow[rowNumber+2] = "max_clicks must be an integer"
			} else {
				link.MaxClicks = &clicks
			}
		}
		link.ExpiredURL, link.IOSURL, link.AndroidURL = value(row, "expired_url"), value(row, "ios_url"), value(row, "android_url")
		if raw := value(row, "forward_query"); raw != "" {
			enabled, e := strconv.ParseBool(raw)
			if e != nil {
				errorsByRow[rowNumber+2] = "forward_query must be true or false"
			} else {
				link.ForwardQuery = enabled
			}
		}
		link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent = value(row, "utm_source"), value(row, "utm_medium"), value(row, "utm_campaign"), value(row, "utm_term"), value(row, "utm_content")
		if raw := value(row, "tags"); raw != "" {
			for _, tag := range strings.Split(raw, ";") {
				if tag = strings.TrimSpace(tag); tag != "" {
					link.Tags = append(link.Tags, tag)
				}
			}
		}
		if raw := value(row, "geo_targets"); raw != "" {
			if e := json.Unmarshal([]byte(raw), &link.GeoTargets); e != nil {
				errorsByRow[rowNumber+2] = "geo_targets must be JSON"
			}
		}
		if e := store.ValidateLink(link); e != nil {
			errorsByRow[rowNumber+2] = e.Error()
		}
		parsed = append(parsed, link)
	}
	if len(errorsByRow) > 0 {
		jsonOut(w, http.StatusUnprocessableEntity, map[string]any{"imported": 0, "errors": errorsByRow})
		return
	}
	if r.URL.Query().Get("dry_run") == "true" {
		jsonOut(w, http.StatusOK, map[string]any{"valid": true, "rows": len(parsed)})
		return
	}
	u, _ := userFrom(r.Context())
	created, err := h.S.CreateLinksBulk(u, parsed)
	if err != nil {
		errOut(w, http.StatusConflict, "no links imported: "+err.Error())
		return
	}
	createdAudit := make([]map[string]any, len(created))
	for i, link := range created {
		createdAudit[i] = linkAudit(link)
	}
	if err := h.audit(r, "link.imported", "link", strconv.Itoa(len(created)), "success", nil, map[string]any{"links": createdAudit}); err != nil {
		errOut(w, 500, "audit log unavailable")
		return
	}
	h.addShortURLs(created)
	jsonOut(w, http.StatusCreated, map[string]any{"imported": len(created), "links": created, "errors": errorsByRow})
}

func (h *Handler) bulkLinks(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch && r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		IDs []int64     `json:"ids"`
		Set linkPayload `json:"set"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.IDs) < 1 || len(in.IDs) > 500 {
		errOut(w, http.StatusBadRequest, "ids must contain 1 to 500 links")
		return
	}
	if in.Set.Slug != nil {
		errOut(w, http.StatusBadRequest, "slug is immutable")
		return
	}
	seenIDs := map[int64]bool{}
	for _, id := range in.IDs {
		if id < 1 || seenIDs[id] {
			errOut(w, http.StatusBadRequest, "ids must be unique positive link ids")
			return
		}
		seenIDs[id] = true
	}
	u, _ := userFrom(r.Context())
	if r.Method == http.MethodDelete {
		ids := make([]string, len(in.IDs))
		for i, id := range in.IDs {
			ids[i] = strconv.FormatInt(id, 10)
		}
		targetID := strings.Join(ids, ",")
		if !h.requireDelete(w, r, "link.bulk_deleted", "link", targetID) {
			return
		}
		beforeLinks, err := h.S.LinksByIDs(u, in.IDs)
		if err != nil {
			errOut(w, 404, "one or more links were not found; no links deleted")
			return
		}
		if err := h.S.DeleteLinksBulk(u, in.IDs); err != nil {
			errOut(w, http.StatusNotFound, "one or more links were not found; no links deleted")
			return
		}
		before := map[string]any{"links": func() []map[string]any {
			out := make([]map[string]any, len(beforeLinks))
			for i, link := range beforeLinks {
				out[i] = linkAudit(link)
			}
			return out
		}()}
		if err := h.audit(r, "link.bulk_deleted", "link", targetID, "success", before, nil); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, http.StatusOK, map[string]int{"deleted": len(in.IDs)})
		return
	}
	currentLinks, err := h.S.LinksByIDs(u, in.IDs)
	if err != nil {
		errOut(w, http.StatusNotFound, "one or more links were not found")
		return
	}
	updates := make([]store.LinkUpdate, 0, len(in.IDs))
	for index, id := range in.IDs {
		current := currentLinks[index]
		link, passwordHash, changed, err := applyLinkPayload(current, in.Set, false)
		if err != nil {
			errOut(w, http.StatusBadRequest, fmt.Sprintf("link %d: %s", id, err.Error()))
			return
		}
		updates = append(updates, store.LinkUpdate{ID: id, Link: link, PasswordHash: passwordHash, PasswordChanged: changed})
	}
	updated, err := h.S.UpdateLinksBulk(u, updates)
	if err != nil {
		errOut(w, http.StatusConflict, "no links updated: "+err.Error())
		return
	}
	beforeAudit := make([]map[string]any, len(currentLinks))
	afterAudit := make([]map[string]any, len(updated))
	for i := range currentLinks {
		beforeAudit[i] = linkAudit(currentLinks[i])
		afterAudit[i] = linkAudit(updated[i])
	}
	if err := h.audit(r, "link.bulk_updated", "link", strconv.Itoa(len(updated)), "success", map[string]any{"links": beforeAudit}, map[string]any{"links": afterAudit}); err != nil {
		errOut(w, 500, "audit log unavailable")
		return
	}
	h.addShortURLs(updated)
	jsonOut(w, http.StatusOK, map[string]any{"updated": updated, "errors": map[int64]string{}})
}

func (h *Handler) qr(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	text := r.URL.Query().Get("text")
	if text == "" {
		text = r.URL.Query().Get("url")
	}
	if text == "" {
		errOut(w, http.StatusBadRequest, "text or url required")
		return
	}
	if len(text) > 2048 {
		errOut(w, http.StatusRequestEntityTooLarge, "QR text may contain at most 2048 bytes")
		return
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size == 0 {
		size = 512
	}
	if size < 128 || size > 1024 {
		errOut(w, http.StatusBadRequest, "size must be 128 to 1024")
		return
	}
	if h.qrSlots != nil {
		select {
		case h.qrSlots <- struct{}{}:
			defer func() { <-h.qrSlots }()
		default:
			w.Header().Set("Retry-After", "1")
			errOut(w, http.StatusTooManyRequests, "QR renderer busy; retry shortly")
			return
		}
	}
	code, err := qrcode.New(text, qrcode.High)
	if err != nil {
		errOut(w, http.StatusInternalServerError, err.Error())
		return
	}
	foreground, err := parseHexColor(r.URL.Query().Get("foreground"), color.Black)
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	background, err := parseHexColor(r.URL.Query().Get("background"), color.White)
	if err != nil {
		errOut(w, http.StatusBadRequest, err.Error())
		return
	}
	code.ForegroundColor, code.BackgroundColor = foreground, background
	code.DisableBorder = r.URL.Query().Get("margin") == "0"
	qrBytes, err := code.PNG(size)
	if err != nil {
		errOut(w, http.StatusInternalServerError, err.Error())
		return
	}
	qrImage, err := png.Decode(bytes.NewReader(qrBytes))
	if err != nil {
		errOut(w, http.StatusInternalServerError, "QR rendering failed")
		return
	}
	var branded image.Image = qrImage
	if r.URL.Query().Get("logo") != "false" {
		logoBytes, err := fs.ReadFile(h.Web, "alva-qr-logo.png")
		if err != nil {
			errOut(w, http.StatusInternalServerError, "ALVA logo unavailable")
			return
		}
		logo, err := png.Decode(bytes.NewReader(logoBytes))
		if err != nil {
			errOut(w, http.StatusInternalServerError, "ALVA logo invalid")
			return
		}
		branded = addCenteredLogo(qrImage, logo)
	}
	w.Header().Set("Cache-Control", "no-store")
	format := strings.ToLower(r.URL.Query().Get("format"))
	switch format {
	case "", "png":
		w.Header().Set("Content-Type", "image/png")
		_ = png.Encode(w, branded)
	case "svg":
		var encoded bytes.Buffer
		_ = png.Encode(&encoded, branded)
		w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="shortq-qr.svg"`)
		_, _ = fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><image width="%d" height="%d" href="data:image/png;base64,%s"/></svg>`, size, size, size, size, size, size, base64.StdEncoding.EncodeToString(encoded.Bytes()))
	case "pdf":
		pdf, err := imagePDF(branded)
		if err != nil {
			errOut(w, http.StatusInternalServerError, "PDF rendering failed")
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="shortq-qr.pdf"`)
		_, _ = w.Write(pdf)
	default:
		errOut(w, http.StatusBadRequest, "format must be png, svg or pdf")
	}
}

func parseHexColor(raw string, fallback color.Color) (color.Color, error) {
	if raw == "" {
		return fallback, nil
	}
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if len(raw) != 6 {
		return nil, fmt.Errorf("colors must use six-digit hex")
	}
	value, err := strconv.ParseUint(raw, 16, 32)
	if err != nil {
		return nil, fmt.Errorf("colors must use six-digit hex")
	}
	return color.RGBA{R: uint8(value >> 16), G: uint8(value >> 8), B: uint8(value), A: 255}, nil
}

func imagePDF(src image.Image) ([]byte, error) {
	var jpegData bytes.Buffer
	if err := jpeg.Encode(&jpegData, src, &jpeg.Options{Quality: 100}); err != nil {
		return nil, err
	}
	width, height := src.Bounds().Dx(), src.Bounds().Dy()
	content := fmt.Sprintf("q\n%d 0 0 %d 0 0 cm\n/Im0 Do\nQ\n", width, height)
	objects := [][]byte{
		[]byte(`<< /Type /Catalog /Pages 2 0 R >>`),
		[]byte(`<< /Type /Pages /Kids [3 0 R] /Count 1 >>`),
		[]byte(fmt.Sprintf(`<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %d %d] /Resources << /XObject << /Im0 4 0 R >> >> /Contents 5 0 R >>`, width, height)),
		append([]byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n", width, height, jpegData.Len())), append(jpegData.Bytes(), []byte("\nendstream")...)...),
		[]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content)),
	}
	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", index+1)
		out.Write(object)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&out, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objects)+1, xref)
	return out.Bytes(), nil
}

func addCenteredLogo(qrImage, logo image.Image) image.Image {
	canvas := image.NewRGBA(qrImage.Bounds())
	draw.Draw(canvas, canvas.Bounds(), qrImage, qrImage.Bounds().Min, draw.Src)

	logo = trimWhiteMargin(logo)
	logoBounds := logo.Bounds()
	if logoBounds.Dx() == 0 || logoBounds.Dy() == 0 {
		return canvas
	}
	logoWidth := canvas.Bounds().Dx() / 4
	logoHeight := logoWidth * logoBounds.Dy() / logoBounds.Dx()
	if logoHeight < 1 {
		logoHeight = 1
	}
	scaled := scaleNearest(logo, logoWidth, logoHeight)
	padding := canvas.Bounds().Dx() / 128
	if padding < 1 {
		padding = 1
	}
	logoAt := image.Pt(
		(canvas.Bounds().Dx()-logoWidth)/2,
		(canvas.Bounds().Dy()-logoHeight)/2,
	)
	badge := image.Rect(
		logoAt.X-padding,
		logoAt.Y-padding,
		logoAt.X+logoWidth+padding,
		logoAt.Y+logoHeight+padding,
	)
	draw.Draw(canvas, badge, image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rectangle{Min: logoAt, Max: logoAt.Add(scaled.Bounds().Size())}, scaled, scaled.Bounds().Min, draw.Over)
	return canvas
}

func trimWhiteMargin(src image.Image) image.Image {
	bounds := src.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false
	const whiteCutoff = 0xf5f5
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := src.At(x, y).RGBA()
			if a == 0 || (r >= whiteCutoff && g >= whiteCutoff && b >= whiteCutoff) {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if y < minY {
				minY = y
			}
			if x+1 > maxX {
				maxX = x + 1
			}
			if y+1 > maxY {
				maxY = y + 1
			}
		}
	}
	if !found {
		return src
	}
	content := image.Rect(minX, minY, maxX, maxY)
	trimmed := image.NewNRGBA(image.Rect(0, 0, content.Dx(), content.Dy()))
	draw.Draw(trimmed, trimmed.Bounds(), src, content.Min, draw.Src)
	return trimmed
}

func scaleNearest(src image.Image, width, height int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, width, height))
	bounds := src.Bounds()
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			srcX := bounds.Min.X + x*bounds.Dx()/width
			srcY := bounds.Min.Y + y*bounds.Dy()/height
			dst.Set(x, y, src.At(srcX, srcY))
		}
	}
	return dst
}

func (h *Handler) apiKeys(w http.ResponseWriter, r *http.Request) {
	p, _ := principalFrom(r.Context())
	scope := "account"
	if p.Mode == "user" {
		scope = "user"
	}
	switch r.Method {
	case "GET":
		ks, err := h.S.ListAPIKeys(p.Actual.ID, scope)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		jsonOut(w, 200, ks)
	case "POST":
		if p.AuthType != "session" {
			errOut(w, http.StatusForbidden, "API keys may only be created from an SSO session")
			return
		}
		var in struct{ Name string }
		if !decode(w, r, &in) {
			return
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" || len(in.Name) > 120 {
			errOut(w, http.StatusBadRequest, "name must contain 1 to 120 characters")
			return
		}
		key, prefix, err := auth.NewAPIKey()
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		id, err := h.S.CreateAPIKey(p.Actual.ID, in.Name, auth.SHA256Hex(key), prefix, scope)
		if err != nil {
			errOut(w, 500, err.Error())
			return
		}
		if err := h.audit(r, "api_key.created", "api_key", prefix, "success", nil, map[string]any{"name": in.Name, "prefix": prefix, "scope": scope}); err != nil {
			_ = h.S.RevokeAPIKey(p.Actual.ID, id, scope)
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, 201, map[string]any{"id": id, "name": in.Name, "key": key, "prefix": prefix, "scope": scope})
	default:
		w.WriteHeader(405)
	}
}
func (h *Handler) apiKeyByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != "DELETE" {
		w.WriteHeader(405)
		return
	}
	p, _ := principalFrom(r.Context())
	scope := "account"
	if p.Mode == "user" {
		scope = "user"
	}
	id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/api-keys/"), 10, 64)
	if !h.requireDelete(w, r, "api_key.revoked", "api_key", strconv.FormatInt(id, 10)) {
		return
	}
	if err := h.S.RevokeAPIKey(p.Actual.ID, id, scope); err != nil {
		errOut(w, 404, "API key not found")
		return
	}
	if err := h.audit(r, "api_key.revoked", "api_key", strconv.FormatInt(id, 10), "success", map[string]any{"id": id, "scope": scope}, nil); err != nil {
		errOut(w, 500, "audit log unavailable")
		return
	}
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
	host := strings.Trim(strings.ToLower(r.Host), ".")
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		host = parsedHost
	}
	var domain *models.TenantDomain
	if host != "" && host != h.baseHost() {
		d, err := h.S.TenantDomainByDomain(host)
		if err != nil || d.Status != "active" {
			http.NotFound(w, r)
			return
		}
		if d.VerifiedAt == nil || time.Since(d.VerifiedAt.UTC()) >= 24*time.Hour {
			if !h.verifyDomain(d) {
				_ = h.S.SetTenantDomainVerified(d.ID, false)
				http.NotFound(w, r)
				return
			}
			if err := h.S.SetTenantDomainVerified(d.ID, true); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		domain = &d
	}
	l, err := h.S.LinkBySlug(slug)
	if err != nil {
		if domain != nil && domain.NotFoundURL != "" {
			http.Redirect(w, r, domain.NotFoundURL, http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	if domain != nil && (l.TenantID == nil || domain.TenantID != *l.TenantID) {
		http.NotFound(w, r)
		return
	}
	if l.PasswordProtected && !h.hasLinkAccess(r, l.ID) {
		h.passwordGate(w, r, l)
		return
	}
	if linkExpired(l) {
		h.recordRedirectEvent(r, l, l.ExpiredURL, "expired", http.StatusGone, false)
		h.expiredResponse(w, r, l)
		return
	}
	target, route := resolveTarget(l, countryFromRequest(r), r.UserAgent())
	target = mergeTargetQuery(target, l, r.URL.Query())
	ok, err := h.S.RecordClick(h.redirectEvent(r, l, target, route, l.RedirectCode), true, l.MaxClicks)
	if err != nil {
		errOut(w, http.StatusInternalServerError, "redirect tracking failed")
		return
	}
	if !ok {
		h.expiredResponse(w, r, l)
		return
	}
	http.Redirect(w, r, target, l.RedirectCode)
}

func linkExpired(l models.Link) bool {
	return (l.ExpiresAt != nil && !time.Now().UTC().Before(l.ExpiresAt.UTC())) || (l.MaxClicks != nil && l.Clicks >= *l.MaxClicks)
}

func (h *Handler) expiredResponse(w http.ResponseWriter, r *http.Request, l models.Link) {
	if l.ExpiredURL != "" {
		http.Redirect(w, r, l.ExpiredURL, http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.WriteHeader(http.StatusGone)
	_, _ = fmt.Fprint(w, `<!doctype html><title>Link expired</title><meta name="viewport" content="width=device-width"><style>body{font:16px system-ui;display:grid;place-items:center;min-height:80vh;color:#202124}main{text-align:center;max-width:32rem}</style><main><h1>This link has expired</h1><p>The destination is no longer available.</p></main>`)
}

func (h *Handler) hasLinkAccess(r *http.Request, linkID int64) bool {
	cookie, err := r.Cookie(fmt.Sprintf("sq_access_%d", linkID))
	return err == nil && auth.CheckLinkAccess(h.C.JWTSecret, cookie.Value, linkID)
}

func (h *Handler) passwordGate(w http.ResponseWriter, r *http.Request, l models.Link) {
	if r.Method == http.MethodPost {
		key := fmt.Sprintf("%d:%s", l.ID, visitorIP(r))
		if h.tooManyAttempts(key) {
			errOut(w, http.StatusTooManyRequests, "too many password attempts; retry in 15 minutes")
			return
		}
		if err := r.ParseForm(); err == nil && auth.CheckSecret(l.PasswordHash, r.FormValue("password")) {
			h.clearAttempts(key)
			expires := time.Now().Add(12 * time.Hour)
			token, err := auth.SignLinkAccess(h.C.JWTSecret, l.ID, expires)
			if err != nil {
				errOut(w, http.StatusInternalServerError, "access token failed")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: fmt.Sprintf("sq_access_%d", l.ID), Value: token, Path: "/", Expires: expires, MaxAge: 12 * 60 * 60, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
			http.Redirect(w, r, r.URL.RequestURI(), http.StatusSeeOther)
			return
		}
		h.addAttempt(key)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	message := ""
	if r.Method == http.MethodPost {
		message = `<p style="color:#b3261e">Incorrect password.</p>`
	}
	_, _ = fmt.Fprintf(w, `<!doctype html><title>Protected link</title><meta name="viewport" content="width=device-width"><style>body{font:16px system-ui;display:grid;place-items:center;min-height:80vh;color:#202124}main{width:min(90%%,24rem)}input,button{box-sizing:border-box;width:100%%;padding:.8rem;margin:.35rem 0}button{background:#111;color:white;border:0;border-radius:4px}</style><main><h1>Protected link</h1><p>Enter the password to continue to %s.</p>%s<form method="post"><input type="password" name="password" autocomplete="current-password" required autofocus><button>Continue</button></form></main>`, html.EscapeString(l.Title), message)
}

func (h *Handler) tooManyAttempts(key string) bool {
	h.attemptMu.Lock()
	defer h.attemptMu.Unlock()
	h.attemptOps++
	h.pruneAttemptsLocked()
	if len(h.attempts) >= 100000 && len(h.attempts[key]) == 0 {
		return true
	}
	cutoff := time.Now().Add(-15 * time.Minute)
	kept := h.attempts[key][:0]
	for _, attempt := range h.attempts[key] {
		if attempt.After(cutoff) {
			kept = append(kept, attempt)
		}
	}
	h.attempts[key] = kept
	return len(kept) >= 5
}

func (h *Handler) pruneAttemptsLocked() {
	if h.attemptOps%256 != 0 && len(h.attempts) < 10000 {
		return
	}
	cutoff := time.Now().Add(-15 * time.Minute)
	for key, attempts := range h.attempts {
		kept := attempts[:0]
		for _, attempt := range attempts {
			if attempt.After(cutoff) {
				kept = append(kept, attempt)
			}
		}
		if len(kept) == 0 {
			delete(h.attempts, key)
		} else {
			h.attempts[key] = kept
		}
	}
}

func (h *Handler) addAttempt(key string) {
	h.attemptMu.Lock()
	h.attempts[key] = append(h.attempts[key], time.Now())
	h.attemptMu.Unlock()
}
func (h *Handler) clearAttempts(key string) {
	h.attemptMu.Lock()
	delete(h.attempts, key)
	h.attemptMu.Unlock()
}

func countryFromRequest(r *http.Request) string {
	country := strings.ToUpper(strings.TrimSpace(r.Header.Get("CF-IPCountry")))
	if len(country) == 2 {
		return country
	}
	return ""
}

func resolveTarget(l models.Link, country, userAgent string) (string, string) {
	for _, geo := range l.GeoTargets {
		if strings.EqualFold(geo.CountryCode, country) {
			return geo.TargetURL, "country"
		}
	}
	ua := strings.ToLower(userAgent)
	if strings.Contains(ua, "iphone") || strings.Contains(ua, "ipad") || strings.Contains(ua, "ipod") {
		if l.IOSURL != "" {
			return l.IOSURL, "ios"
		}
	}
	if strings.Contains(ua, "android") && l.AndroidURL != "" {
		return l.AndroidURL, "android"
	}
	return l.TargetURL, "default"
}

func mergeTargetQuery(raw string, l models.Link, inbound url.Values) string {
	destination, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	query := destination.Query()
	for key, value := range map[string]string{"utm_source": l.UTMSource, "utm_medium": l.UTMMedium, "utm_campaign": l.UTMCampaign, "utm_term": l.UTMTerm, "utm_content": l.UTMContent} {
		if value != "" {
			query.Set(key, value)
		}
	}
	if l.ForwardQuery {
		for key, values := range inbound {
			query.Del(key)
			for _, value := range values {
				query.Add(key, value)
			}
		}
	}
	destination.RawQuery = query.Encode()
	return destination.String()
}

func visitorIP(r *http.Request) string {
	if ip := strings.TrimSpace(r.Header.Get("CF-Connecting-IP")); net.ParseIP(ip) != nil {
		return ip
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return ip
	}
	return r.RemoteAddr
}

func clientTraits(ua string) (browser, osName, device string, bot bool) {
	lower := strings.ToLower(ua)
	bot = strings.Contains(lower, "bot") || strings.Contains(lower, "crawler") || strings.Contains(lower, "spider") || strings.Contains(lower, "slurp")
	switch {
	case strings.Contains(lower, "edg/"):
		browser = "Edge"
	case strings.Contains(lower, "chrome/") || strings.Contains(lower, "crios/"):
		browser = "Chrome"
	case strings.Contains(lower, "firefox/"):
		browser = "Firefox"
	case strings.Contains(lower, "safari/"):
		browser = "Safari"
	default:
		browser = "Other"
	}
	switch {
	case strings.Contains(lower, "android"):
		osName = "Android"
	case strings.Contains(lower, "iphone") || strings.Contains(lower, "ipad"):
		osName = "iOS"
	case strings.Contains(lower, "windows"):
		osName = "Windows"
	case strings.Contains(lower, "mac os"):
		osName = "macOS"
	case strings.Contains(lower, "linux"):
		osName = "Linux"
	default:
		osName = "Other"
	}
	switch {
	case bot:
		device = "bot"
	case strings.Contains(lower, "ipad") || strings.Contains(lower, "tablet"):
		device = "tablet"
	case strings.Contains(lower, "mobile") || strings.Contains(lower, "iphone") || strings.Contains(lower, "android"):
		device = "mobile"
	default:
		device = "desktop"
	}
	return
}

func (h *Handler) redirectEvent(r *http.Request, l models.Link, target, route string, status int) models.ClickEvent {
	browser, osName, device, bot := clientTraits(r.UserAgent())
	refHost := ""
	if ref, err := url.Parse(r.Referer()); err == nil {
		refHost = ref.Hostname()
	}
	values := url.Values{}
	if destination, err := url.Parse(target); err == nil {
		values = destination.Query()
	}
	return models.ClickEvent{LinkID: l.ID, Slug: l.Slug, IP: visitorIP(r), CountryCode: countryFromRequest(r), Method: r.Method, StatusCode: status, ResolvedURL: analyticsResolvedURL(target), RouteType: route, UserAgent: r.UserAgent(), Browser: browser, OS: osName, Device: device, IsBot: bot, Referrer: r.Referer(), ReferrerHost: refHost, UTMSource: values.Get("utm_source"), UTMMedium: values.Get("utm_medium"), UTMCampaign: values.Get("utm_campaign")}
}

func analyticsResolvedURL(raw string) string {
	destination, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	destination.RawQuery = ""
	destination.Fragment = ""
	destination.User = nil
	return destination.String()
}

func (h *Handler) recordRedirectEvent(r *http.Request, l models.Link, target, route string, status int, increment bool) {
	_, _ = h.S.RecordClick(h.redirectEvent(r, l, target, route, status), increment, nil)
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if key := r.Header.Get("X-API-Key"); key != "" {
			u, scope, err := h.S.UserByAPIKey(auth.SHA256Hex(key))
			if err == nil && u.Active && h.allowedMicrosoftUser(u.Email) {
				mode := defaultDashboardMode(u)
				if scope == "user" {
					mode = "user"
				}
				next(w, r.WithContext(withPrincipal(r.Context(), effectivePrincipal(u, mode, "api_key"))))
				return
			}
		}
		if p, ok := h.userFromSession(r); ok {
			next(w, r.WithContext(withPrincipal(r.Context(), p)))
			return
		}
		bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		c, err := auth.ParseJWT(h.C.JWTSecret, bearer)
		if err != nil {
			ssoRequired(w)
			return
		}
		u, err := h.S.UserByID(c.UserID)
		if err != nil || !u.Active || !h.allowedMicrosoftUser(u.Email) {
			errOut(w, 401, "auth required")
			return
		}
		next(w, r.WithContext(withPrincipal(r.Context(), effectivePrincipal(u, c.Mode, "bearer"))))
	}
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			errOut(w, http.StatusRequestEntityTooLarge, "JSON body may contain at most 1 MiB")
			return false
		}
		errOut(w, 400, "bad json")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
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

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
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
		if err := h.audit(r, "domain.created", "domain", strconv.FormatInt(d.ID, 10), "success", nil, domainAudit(d)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, 201, d)
	default:
		w.WriteHeader(405)
	}
}

func (h *Handler) domainByID(w http.ResponseWriter, r *http.Request) {
	u, _ := userFrom(r.Context())
	if u.Role != "tenant" && u.Role != "superadmin" {
		errOut(w, http.StatusForbidden, "tenant or superadmin only")
		return
	}
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
		if err := h.S.SetTenantDomainVerified(id, true); err != nil {
			errOut(w, http.StatusInternalServerError, "domain activation failed")
			return
		}
		after, _ := h.S.TenantDomainByID(u, id)
		if err := h.audit(r, "domain.verified", "domain", strconv.FormatInt(id, 10), "success", domainAudit(d), domainAudit(after)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, 200, map[string]string{"message": "domain verified"})
		return
	}
	if !verify && (r.Method == http.MethodPatch || r.Method == http.MethodPut) {
		var in struct {
			NotFoundURL string `json:"not_found_url"`
		}
		if !decode(w, r, &in) {
			return
		}
		before, err := h.S.TenantDomainByID(u, id)
		if err != nil {
			errOut(w, 404, "domain not found")
			return
		}
		domain, err := h.S.UpdateTenantDomain(u, id, strings.TrimSpace(in.NotFoundURL))
		if err != nil {
			errOut(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := h.audit(r, "domain.updated", "domain", strconv.FormatInt(id, 10), "success", domainAudit(before), domainAudit(domain)); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
		jsonOut(w, http.StatusOK, domain)
		return
	}
	if r.Method == "DELETE" {
		if !h.requireDelete(w, r, "domain.deleted", "domain", strconv.FormatInt(id, 10)) {
			return
		}
		before, err := h.S.TenantDomainByID(u, id)
		if err != nil {
			errOut(w, 404, "domain not found")
			return
		}
		if err := h.S.DeleteTenantDomain(u, id); err != nil {
			errOut(w, 404, "domain not found")
			return
		}
		if err := h.audit(r, "domain.deleted", "domain", strconv.FormatInt(id, 10), "success", domainAudit(before), nil); err != nil {
			errOut(w, 500, "audit log unavailable")
			return
		}
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

func (h *Handler) addShortURLs(links []models.Link) {
	tenantIDs := []int64{}
	seen := map[int64]bool{}
	for _, link := range links {
		if link.TenantID != nil && !seen[*link.TenantID] {
			seen[*link.TenantID] = true
			tenantIDs = append(tenantIDs, *link.TenantID)
		}
	}
	domains, _ := h.S.ActiveDomainsForTenants(tenantIDs)
	base := strings.TrimRight(h.C.BaseURL, "/")
	for index := range links {
		if links[index].TenantID != nil {
			if domain := domains[*links[index].TenantID]; domain != "" {
				links[index].ShortURL = "https://" + domain + "/" + links[index].Slug
				continue
			}
		}
		links[index].ShortURL = base + "/" + links[index].Slug
	}
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
