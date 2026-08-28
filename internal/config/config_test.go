package config

import "testing"

func validConfig() Config {
	return Config{
		JWTSecret:     "01234567890123456789012345678901",
		SuperPassword: "a-strong-admin-password",
		OIDCTenantID:  "tenant", OIDCClientID: "client", OIDCClientSecret: "secret",
		OIDCRedirectURL: "https://example.com/callback", OIDCAllowedDomain: "example.com",
	}
}

func TestValidateAcceptsSecureConfiguration(t *testing.T) {
	if err := Validate(validConfig()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsKnownDefaults(t *testing.T) {
	c := validConfig()
	c.JWTSecret = "dev-secret-change-me-min-32-chars"
	if err := Validate(c); err == nil {
		t.Fatal("expected default JWT secret to be rejected")
	}
	c = validConfig()
	c.SuperPassword = "ChangeMe123!"
	if err := Validate(c); err == nil {
		t.Fatal("expected default superadmin password to be rejected")
	}
}

func TestValidateAllowsDevAuthBypassWithoutOIDC(t *testing.T) {
	c := validConfig()
	c.OIDCTenantID = ""
	c.OIDCClientID = ""
	c.OIDCClientSecret = ""
	c.OIDCAllowedDomain = ""
	c.OIDCAllowedEmail = ""
	c.DevAuthBypass = true
	c.DevAuthEmail = "admin@shortq.local"
	if err := Validate(c); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequiresDevAuthEmailWhenBypassEnabled(t *testing.T) {
	c := validConfig()
	c.DevAuthBypass = true
	c.DevAuthEmail = ""
	if err := Validate(c); err == nil {
		t.Fatal("expected missing dev auth email to be rejected")
	}
}
