package db

import (
	"database/sql"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

func Open(dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(time.Hour)
	var pingErr error
	for i := 0; i < 30; i++ {
		if pingErr = db.Ping(); pingErr == nil {
			return db, nil
		}
		time.Sleep(2 * time.Second)
	}
	return nil, pingErr
}

func Migrate(db *sql.DB) error {
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS tenants (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(140) NOT NULL,
  slug VARCHAR(80) NOT NULL UNIQUE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT NULL,
  email VARCHAR(190) NOT NULL UNIQUE,
  name VARCHAR(120) NOT NULL,
  role ENUM('superadmin','tenant','customer') NOT NULL DEFAULT 'customer',
  password_hash VARBINARY(255) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS password_resets (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  token_hash CHAR(64) NOT NULL UNIQUE,
  expires_at TIMESTAMP NOT NULL,
  used_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS api_keys (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  name VARCHAR(120) NOT NULL,
  key_hash CHAR(64) NOT NULL UNIQUE,
  prefix VARCHAR(24) NOT NULL,
  last_used_at TIMESTAMP NULL,
  revoked_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS links (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  user_id BIGINT NOT NULL,
  tenant_id BIGINT NULL,
  slug VARCHAR(80) NOT NULL UNIQUE,
  target_url TEXT NOT NULL,
  title VARCHAR(180) NOT NULL DEFAULT '',
  clicks BIGINT NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS clicks (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  link_id BIGINT NOT NULL,
  ip VARCHAR(64) NOT NULL,
  user_agent TEXT NOT NULL,
  referrer TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (link_id) REFERENCES links(id) ON DELETE CASCADE
);
`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN tenant_id BIGINT NULL`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN role ENUM('superadmin','tenant','customer') NOT NULL DEFAULT 'customer'`)
	_, _ = db.Exec(`ALTER TABLE links ADD COLUMN tenant_id BIGINT NULL`)
	return nil
}
