package config

import "os"

type Config struct {
	Addr              string
	BaseURL           string
	MySQLDSN          string
	JWTSecret         string
	SuperEmail        string
	SuperPassword     string
	OIDCTenantID      string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCAllowedEmail  string
	OIDCAllowedDomain string
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
		MySQLDSN:          env("MYSQL_DSN", "shortq:shortqpass@tcp(127.0.0.1:3306)/shortq?parseTime=true&multiStatements=true"),
		JWTSecret:         env("JWT_SECRET", "dev-secret-change-me-min-32-chars"),
		SuperEmail:        env("SUPERADMIN_EMAIL", "[EMAIL_HASH:f50fd01878cf]"),
		SuperPassword:     env("SUPERADMIN_PASSWORD", "ChangeMe123!"),
		OIDCTenantID:      os.Getenv("OIDC_TENANT_ID"),
		OIDCClientID:      os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:   env("OIDC_REDIRECT_URL", env("APP_BASE_URL", "http://localhost:8080")+"/auth/microsoft/callback"),
		OIDCAllowedEmail:  env("OIDC_ALLOWED_EMAIL", env("SUPERADMIN_EMAIL", "")),
		OIDCAllowedDomain: env("OIDC_ALLOWED_DOMAIN", ""),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          env("SMTP_PORT", "587"),
		SMTPUser:          os.Getenv("SMTP_USER"),
		SMTPPass:          os.Getenv("SMTP_PASS"),
		SMTPFrom:          env("SMTP_FROM", "no-reply@localhost"),
	}
}

func env(k, d string) string {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v
}

func (c Config) OIDCIssuer() string {
	return "https://login.microsoftonline.com/" + c.OIDCTenantID + "/v2.0"
}
