package handlers

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"shortq/internal/auth"
	"shortq/internal/models"
)

const (
	sessionCookie = "shortq_session"
	stateCookie   = "shortq_oidc_state"
	nonceCookie   = "shortq_oidc_nonce"
)

type microsoftClaims struct {
	Email             string `json:"email"`
	PreferredUsername string `json:"preferred_username"`
	Name              string `json:"name"`
	Nonce             string `json:"nonce"`
}

func (h *Handler) microsoftLogin(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.oauthConfig(r.Context())
	if err != nil {
		errOut(w, 500, "sso not configured")
		return
	}
	state, _ := auth.NewToken(32)
	nonce, _ := auth.NewToken(32)
	setTemporaryCookie(w, stateCookie, state)
	setTemporaryCookie(w, nonceCookie, nonce)
	http.Redirect(w, r, cfg.AuthCodeURL(state, oidc.Nonce(nonce)), http.StatusFound)
}

func (h *Handler) microsoftCallback(w http.ResponseWriter, r *http.Request) {
	state, err := r.Cookie(stateCookie)
	if err != nil || state.Value == "" || r.URL.Query().Get("state") != state.Value {
		errOut(w, 400, "invalid sso state")
		return
	}
	nonce, err := r.Cookie(nonceCookie)
	if err != nil || nonce.Value == "" {
		errOut(w, 400, "invalid sso nonce")
		return
	}
	clearCookie(w, stateCookie)
	clearCookie(w, nonceCookie)

	cfg, err := h.oauthConfig(r.Context())
	if err != nil {
		errOut(w, 500, "sso not configured")
		return
	}
	tok, err := cfg.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		errOut(w, 401, "sso exchange failed")
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok || rawID == "" {
		errOut(w, 401, "missing id token")
		return
	}
	provider, err := oidc.NewProvider(r.Context(), h.C.OIDCIssuer())
	if err != nil {
		errOut(w, 500, "sso provider unavailable")
		return
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: h.C.OIDCClientID}).Verify(r.Context(), rawID)
	if err != nil {
		errOut(w, 401, "invalid id token")
		return
	}
	var claims microsoftClaims
	if err := idToken.Claims(&claims); err != nil {
		errOut(w, 401, "invalid sso claims")
		return
	}
	if claims.Nonce != nonce.Value {
		errOut(w, 401, "invalid sso nonce")
		return
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		email = strings.ToLower(strings.TrimSpace(claims.PreferredUsername))
	}
	if !h.allowedMicrosoftUser(email) {
		errOut(w, 403, "user not allowed")
		return
	}
	u, err := h.ensureSSOUser(email, claims.Name)
	if err != nil {
		errOut(w, 500, "sso user setup failed")
		return
	}
	signed, _ := auth.SignJWT(h.C.JWTSecret, auth.Claims{UserID: u.ID, Email: u.Email, Exp: auth.TokenTTL()})
	setSessionCookie(w, signed)
	http.Redirect(w, r, "/#app", http.StatusFound)
}

func (h *Handler) microsoftLogout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, sessionCookie)
	logoutURL := fmt.Sprintf("https://login.microsoftonline.com/%s/oauth2/v2.0/logout?post_logout_redirect_uri=%s", url.PathEscape(h.C.OIDCTenantID), url.QueryEscape(h.C.BaseURL))
	http.Redirect(w, r, logoutURL, http.StatusFound)
}

func (h *Handler) oauthConfig(ctx context.Context) (*oauth2.Config, error) {
	if h.C.OIDCTenantID == "" || h.C.OIDCClientID == "" || h.C.OIDCClientSecret == "" || h.C.OIDCRedirectURL == "" {
		return nil, fmt.Errorf("missing oidc config")
	}
	provider, err := oidc.NewProvider(ctx, h.C.OIDCIssuer())
	if err != nil {
		return nil, err
	}
	return &oauth2.Config{
		ClientID:     h.C.OIDCClientID,
		ClientSecret: h.C.OIDCClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  h.C.OIDCRedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "profile", "email"},
	}, nil
}

func (h *Handler) allowedMicrosoftUser(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	allowedDomain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(h.C.OIDCAllowedDomain)), "@")
	if allowedDomain != "" && strings.HasSuffix(email, "@"+allowedDomain) {
		return true
	}
	allowed := strings.ToLower(strings.TrimSpace(h.C.OIDCAllowedEmail))
	return allowed != "" && email == allowed
}

func (h *Handler) ensureSSOUser(email, name string) (models.User, error) {
	if u, _, err := h.S.UserByEmail(email); err == nil {
		return u, nil
	} else if err != sql.ErrNoRows {
		return models.User{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = email
	}
	pass, _ := auth.HashPassword("sso-disabled-" + time.Now().String() + email)
	if err := h.S.CreateUser(email, name, "superadmin", nil, pass); err != nil {
		return models.User{}, err
	}
	u, _, err := h.S.UserByEmail(email)
	return u, err
}

func (h *Handler) userFromSession(r *http.Request) (models.User, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		return models.User{}, false
	}
	claims, err := auth.ParseJWT(h.C.JWTSecret, cookie.Value)
	if err != nil {
		return models.User{}, false
	}
	u, err := h.S.UserByID(claims.UserID)
	if err != nil || !h.allowedMicrosoftUser(u.Email) {
		return models.User{}, false
	}
	return u, true
}

func setTemporaryCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, MaxAge: 600})
}

func setSessionCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(24 * time.Hour), MaxAge: 86400})
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Expires: time.Unix(0, 0), MaxAge: -1})
}

func ssoRequired(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": "sso required", "login_url": "/auth/microsoft/login"})
}
