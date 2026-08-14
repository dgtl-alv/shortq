package handlers

import (
	"testing"

	"shortq/internal/config"
)

func TestAllowedMicrosoftUserByDomain(t *testing.T) {
	h := &Handler{C: config.Config{OIDCAllowedDomain: "alvaauto.com", OIDCAllowedEmail: "redya.febriyanto@alvaauto.com"}}
	if !h.allowedMicrosoftUser("user@alvaauto.com") {
		t.Fatalf("expected alvaauto.com user allowed")
	}
	if !h.allowedMicrosoftUser("USER@ALVAAUTO.COM") {
		t.Fatalf("expected domain match case-insensitive")
	}
	if h.allowedMicrosoftUser("user@example.com") {
		t.Fatalf("expected non-alvaauto.com user denied")
	}
}

func TestAllowedMicrosoftUserByEmailFallback(t *testing.T) {
	h := &Handler{C: config.Config{OIDCAllowedEmail: "redya.febriyanto@alvaauto.com"}}
	if !h.allowedMicrosoftUser("Redya.Febriyanto@alvaauto.com") {
		t.Fatalf("expected configured email allowed")
	}
	if h.allowedMicrosoftUser("user@alvaauto.com") {
		t.Fatalf("expected other user denied when only email allowlist set")
	}
}
