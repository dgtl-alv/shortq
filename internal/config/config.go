package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Addr              string
	BaseURL           string
	DatabaseURL       string
	JWTSecret         string
	SuperEmail        string
	SuperPassword     string
	OIDCTenantID      string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCAllowedEmail  string
	OIDCAllowedDomain string
	DevAuthBypass     bool
	DevAuthEmail      string
	SMTPHost          string
	SMTPPort          string
	SMTPUser          string
	SMTPPass          string
	SMTPFrom          string
}

func Load() Config {
	return Config{
		Addr:              env("APP_ADDR", ":8080"),
		BaseURL:           env("APP_BASE_URL", "http://localhost:8080"),
		DatabaseURL:       env("DATABASE_URL", "postgres://shortq:shortqpass@127.0.0.1:5432/shortq?sslmode=disable"),
		JWTSecret:         env("JWT_SECRET", "dev-secret-change-me-min-32-chars"),
		SuperEmail:        env("SUPERADMIN_EMAIL", "[EMAIL_HASH:f50fd01878cf]"),
		SuperPassword:     env("SUPERADMIN_PASSWORD", "ChangeMe123!"),
		OIDCTenantID:      os.Getenv("OIDC_TENANT_ID"),
		OIDCClientID:      os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:   env("OIDC_REDIRECT_URL", env("APP_BASE_URL", "http://localhost:8080")+"/auth/microsoft/callback"),
		OIDCAllowedEmail:  env("OIDC_ALLOWED_EMAIL", env("SUPERADMIN_EMAIL", "")),
		OIDCAllowedDomain: env("OIDC_ALLOWED_DOMAIN", ""),
		DevAuthBypass:     boolEnv("DEV_AUTH_BYPASS"),
		DevAuthEmail:      env("DEV_AUTH_EMAIL", env("SUPERADMIN_EMAIL", "")),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          env("SMTP_PORT", "587"),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPass:          os.Getenv("SMTP_PASS"),
		SMTPFrom:          env("SMTP_FROM", "no-reply@localhost"),
	}
}

func Validate(c Config) error {
	if len(c.JWTSecret) < 32 || c.JWTSecret == "dev-secret-change-me-min-32-chars" {
		return errors.New("JWT_SECRET must be a non-default secret of at least 32 characters")
	}
	if len(c.SuperPassword) < 16 || c.SuperPassword == "ChangeMe123!" {
		return errors.New("SUPERADMIN_PASSWORD must be a non-default value of at least 16 characters")
	}
	if c.DevAuthBypass {
		if strings.TrimSpace(c.DevAuthEmail) == "" {
			return errors.New("DEV_AUTH_EMAIL is required when DEV_AUTH_BYPASS is enabled")
		}
	} else {
		for name, value := range map[string]string{"OIDC_TENANT_ID": c.OIDCTenantID, "OIDC_CLIENT_ID": c.OIDCClientID, "OIDC_CLIENT_SECRET": c.OIDCClientSecret, "OIDC_REDIRECT_URL": c.OIDCRedirectURL} {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf("%s is required", name)
			}
		}
		if strings.TrimSpace(c.OIDCAllowedEmail) == "" && strings.TrimSpace(c.OIDCAllowedDomain) == "" {
			return errors.New("OIDC_ALLOWED_EMAIL or OIDC_ALLOWED_DOMAIN is required")
		}
	}
	return nil
}

func env(k, d string) string {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v
}

func boolEnv(k string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(k))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (c Config) OIDCIssuer() string {
	return "https://login.microsoftonline.com/" + c.OIDCTenantID + "/v2.0"
}
