package config

import (
	"testing"
	"time"
)

func validConfig() Config {
	return Config{
		DatabaseURL:   "postgres://example.invalid/shortq",
		JWTSecret:     "01234567890123456789012345678901",
		SuperEmail:    "admin@example.invalid",
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

func TestRedirectCacheDefaultsDisabled(t *testing.T) {
	t.Setenv("REDIRECT_CACHE_ENABLED", "")
	t.Setenv("REDIS_URL", "")
	t.Setenv("REDIRECT_CACHE_TIMEOUT", "")
	c := Load()
	if c.RedirectCacheEnabled {
		t.Fatal("redirect cache must default to disabled")
	}
	if c.RedisURL != "redis://redis:6379/0" || c.RedirectCacheTimeout != 250*time.Millisecond {
		t.Fatalf("Redis defaults: URL=%q timeout=%s", c.RedisURL, c.RedirectCacheTimeout)
	}
}

func TestClickQueueDefaultsDisabled(t *testing.T) {
	t.Setenv("CLICK_QUEUE_ENABLED", "")
	t.Setenv("RABBITMQ_URL", "")
	t.Setenv("CLICK_QUEUE_CONFIRM_TIMEOUT", "")
	c := Load()
	if c.ClickQueueEnabled {
		t.Fatal("click queue must default to disabled")
	}
	if c.RabbitMQURL != "amqp://rabbitmq:5672/" || c.ClickQueueConfirmTimeout != 250*time.Millisecond {
		t.Fatalf("RabbitMQ defaults: URL=%q timeout=%s", c.RabbitMQURL, c.ClickQueueConfirmTimeout)
	}
}
