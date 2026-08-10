package config

import "os"

type Config struct {
	Addr          string
	BaseURL       string
	MySQLDSN      string
	JWTSecret     string
	SuperEmail    string
	SuperPassword string
	SMTPHost      string
	SMTPPort      string
	SMTPUser      string
	SMTPPass      string
	SMTPFrom      string
}

func Load() Config {
	return Config{
		Addr:          env("APP_ADDR", ":8080"),
		BaseURL:       env("APP_BASE_URL", "http://localhost:8080"),
		MySQLDSN:      env("MYSQL_DSN", "shortq:shortqpass@tcp(127.0.0.1:3306)/shortq?parseTime=true&multiStatements=true"),
		JWTSecret:     env("JWT_SECRET", "dev-secret-change-me-min-32-chars"),
		SuperEmail:    env("SUPERADMIN_EMAIL", "admin@shortq.local"),
		SuperPassword: env("SUPERADMIN_PASSWORD", "ChangeMe123!"),
		SMTPHost:      os.Getenv("SMTP_HOST"),
		SMTPPort:      env("SMTP_PORT", "587"),
		SMTPUser:      os.Getenv("SMTP_USER"),
		SMTPPass:      os.Getenv("SMTP_PASS"),
		SMTPFrom:      env("SMTP_FROM", "no-reply@localhost"),
	}
}

func env(k, d string) string {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	return v
}
