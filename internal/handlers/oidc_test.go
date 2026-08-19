package handlers

import (
	"testing"

	"shortq/internal/config"
	"shortq/internal/models"
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

func TestEffectivePrincipalProjectsAdminToUser(t *testing.T) {
	actual := models.User{ID: 1, Role: "superadmin"}
	p := effectivePrincipal(actual, "user", "session")
	if p.Actual.Role != "superadmin" || p.Effective.Role != "customer" || p.Mode != "user" {
		t.Fatalf("unexpected principal: %#v", p)
	}
}

func TestEffectivePrincipalDefaultsAdminsToAdmin(t *testing.T) {
	actual := models.User{ID: 2, Role: "tenant"}
	p := effectivePrincipal(actual, "", "session")
	if p.Effective.Role != "tenant" || p.Mode != "admin" {
		t.Fatalf("unexpected principal: %#v", p)
	}
}

func TestEffectivePrincipalCannotElevateCustomer(t *testing.T) {
	actual := models.User{ID: 3, Role: "customer"}
	p := effectivePrincipal(actual, "admin", "session")
	if p.Effective.Role != "customer" || p.Mode != "user" || canSwitchMode(actual) {
		t.Fatalf("unexpected principal: %#v", p)
	}
}

func TestDeletionAccessRequiresGrantForOrdinaryUser(t *testing.T) {
	p := effectivePrincipal(models.User{ID: 4, Role: "customer"}, "user", "session")
	if canDelete(p) {
		t.Fatal("ordinary user unexpectedly has deletion access")
	}
	p.Actual.DeletionAccess = true
	if !canDelete(p) {
		t.Fatal("activated user should have deletion access")
	}
}

func TestSuperadminAlwaysHasDeletionAccess(t *testing.T) {
	p := effectivePrincipal(models.User{ID: 5, Role: "superadmin", DeletionAccess: false}, "user", "session")
	if !canDelete(p) {
		t.Fatal("superadmin bypass should apply even in user mode")
	}
}
