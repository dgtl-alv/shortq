package db

import (
	"context"
	"database/sql"
	"fmt"
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
	lockConn, err := db.Conn(context.Background())
	if err != nil {
		return err
	}
	defer lockConn.Close()
	var acquired int
	if err := lockConn.QueryRowContext(context.Background(), `SELECT GET_LOCK('shortq_schema_migrate',60)`).Scan(&acquired); err != nil {
		return err
	}
	if acquired != 1 {
		return fmt.Errorf("could not acquire schema migration lock")
	}
	defer lockConn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('shortq_schema_migrate')`)

	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS tenants (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  name VARCHAR(140) NOT NULL,
  slug VARCHAR(80) NOT NULL UNIQUE,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS tenant_domains (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT NOT NULL,
  domain VARCHAR(190) NOT NULL UNIQUE,
  status ENUM('pending','active') NOT NULL DEFAULT 'pending',
  verification_token VARCHAR(80) NOT NULL,
  not_found_url TEXT NULL,
  verified_at TIMESTAMP NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS users (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  tenant_id BIGINT NULL,
  email VARCHAR(190) NOT NULL UNIQUE,
  name VARCHAR(120) NOT NULL,
  role ENUM('superadmin','tenant','customer') NOT NULL DEFAULT 'customer',
  deletion_access BOOLEAN NOT NULL DEFAULT FALSE,
  active BOOLEAN NOT NULL DEFAULT TRUE,
  password_hash VARBINARY(255) NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS audit_events (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  actor_user_id BIGINT NULL,
  actor_email VARCHAR(190) NOT NULL DEFAULT '',
  auth_type VARCHAR(24) NOT NULL,
  action VARCHAR(80) NOT NULL,
  target_type VARCHAR(40) NOT NULL,
  target_id VARCHAR(255) NOT NULL DEFAULT '',
  outcome ENUM('success','denied') NOT NULL,
  before_json JSON NULL,
  after_json JSON NULL,
  ip_address VARCHAR(64) NOT NULL DEFAULT '',
  user_agent VARCHAR(500) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_audit_created(id,created_at), INDEX idx_audit_actor(actor_user_id,id),
  INDEX idx_audit_action(action,id), INDEX idx_audit_target(target_type,id), INDEX idx_audit_outcome(outcome,id),
  FOREIGN KEY (actor_user_id) REFERENCES users(id) ON DELETE SET NULL
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
  scope ENUM('account','user') NOT NULL DEFAULT 'account',
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
  visibility ENUM('private','department') NOT NULL DEFAULT 'private',
  clicks BIGINT NOT NULL DEFAULT 0,
  redirect_code SMALLINT NOT NULL DEFAULT 302,
  expires_at DATETIME NULL,
  max_clicks BIGINT NULL,
  expired_url TEXT NULL,
  ios_url TEXT NULL,
  android_url TEXT NULL,
  password_hash VARBINARY(255) NULL,
  forward_query BOOLEAN NOT NULL DEFAULT TRUE,
  utm_source VARCHAR(255) NOT NULL DEFAULT '',
  utm_medium VARCHAR(255) NOT NULL DEFAULT '',
  utm_campaign VARCHAR(255) NOT NULL DEFAULT '',
  utm_term VARCHAR(255) NOT NULL DEFAULT '',
  utm_content VARCHAR(255) NOT NULL DEFAULT '',
  tags_json TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  deleted_at TIMESTAMP NULL,
  FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
  FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE SET NULL
);
CREATE TABLE IF NOT EXISTS clicks (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  link_id BIGINT NOT NULL,
  ip VARCHAR(64) NOT NULL,
  user_agent TEXT NOT NULL,
  referrer TEXT NOT NULL,
  country_code CHAR(2) NOT NULL DEFAULT '',
  method VARCHAR(10) NOT NULL DEFAULT 'GET',
  status_code SMALLINT NOT NULL DEFAULT 302,
  resolved_url TEXT NULL,
  route_type VARCHAR(24) NOT NULL DEFAULT 'default',
  browser VARCHAR(80) NOT NULL DEFAULT '',
  os VARCHAR(80) NOT NULL DEFAULT '',
  device VARCHAR(32) NOT NULL DEFAULT '',
  is_bot BOOLEAN NOT NULL DEFAULT FALSE,
  referrer_host VARCHAR(255) NOT NULL DEFAULT '',
  utm_source VARCHAR(255) NOT NULL DEFAULT '',
  utm_medium VARCHAR(255) NOT NULL DEFAULT '',
  utm_campaign VARCHAR(255) NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_clicks_created_at(created_at),
  INDEX idx_clicks_link_created(link_id,created_at),
  FOREIGN KEY (link_id) REFERENCES links(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS link_geo_targets (
  id BIGINT AUTO_INCREMENT PRIMARY KEY,
  link_id BIGINT NOT NULL,
  country_code CHAR(2) NOT NULL,
  target_url TEXT NOT NULL,
  UNIQUE KEY uq_link_country(link_id,country_code),
  FOREIGN KEY (link_id) REFERENCES links(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS click_rollups_daily (
  day DATE NOT NULL,
  link_id BIGINT NOT NULL,
  country_code CHAR(2) NOT NULL DEFAULT '',
  device VARCHAR(32) NOT NULL DEFAULT '',
  browser VARCHAR(80) NOT NULL DEFAULT '',
  referrer_host VARCHAR(255) NOT NULL DEFAULT '',
  utm_campaign VARCHAR(255) NOT NULL DEFAULT '',
  route_type VARCHAR(24) NOT NULL DEFAULT '',
  status_code SMALLINT NOT NULL DEFAULT 0,
  clicks BIGINT NOT NULL DEFAULT 0,
  PRIMARY KEY(day,link_id,country_code,device,browser,referrer_host,utm_campaign,route_type,status_code),
  INDEX idx_rollups_link_day(link_id,day),
  FOREIGN KEY (link_id) REFERENCES links(id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS schema_migrations (
  version BIGINT PRIMARY KEY,
  applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
`)
	if err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN tenant_id BIGINT NULL`)
	_, _ = db.Exec(`ALTER TABLE users ADD COLUMN role ENUM('superadmin','tenant','customer') NOT NULL DEFAULT 'customer'`)
	_, _ = db.Exec(`ALTER TABLE links ADD COLUMN tenant_id BIGINT NULL`)
	_, _ = db.Exec(`ALTER TABLE api_keys ADD COLUMN scope ENUM('account','user') NOT NULL DEFAULT 'account'`)
	columns := []struct{ table, name, definition string }{
		{"users", "deletion_access", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"users", "active", "BOOLEAN NOT NULL DEFAULT TRUE"},
		{"tenant_domains", "not_found_url", "TEXT NULL"},
		{"links", "redirect_code", "SMALLINT NOT NULL DEFAULT 302"},
		{"links", "expires_at", "DATETIME NULL"},
		{"links", "max_clicks", "BIGINT NULL"},
		{"links", "expired_url", "TEXT NULL"},
		{"links", "ios_url", "TEXT NULL"},
		{"links", "android_url", "TEXT NULL"},
		{"links", "password_hash", "VARBINARY(255) NULL"},
		{"links", "forward_query", "BOOLEAN NOT NULL DEFAULT TRUE"},
		{"links", "utm_source", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"links", "utm_medium", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"links", "utm_campaign", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"links", "utm_term", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"links", "utm_content", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"links", "tags_json", "TEXT NULL"},
		{"links", "visibility", "ENUM('private','department') NOT NULL DEFAULT 'private'"},
		{"links", "deleted_at", "TIMESTAMP NULL"},
		{"clicks", "country_code", "CHAR(2) NOT NULL DEFAULT ''"},
		{"clicks", "method", "VARCHAR(10) NOT NULL DEFAULT 'GET'"},
		{"clicks", "status_code", "SMALLINT NOT NULL DEFAULT 302"},
		{"clicks", "resolved_url", "TEXT NULL"},
		{"clicks", "route_type", "VARCHAR(24) NOT NULL DEFAULT 'default'"},
		{"clicks", "browser", "VARCHAR(80) NOT NULL DEFAULT ''"},
		{"clicks", "os", "VARCHAR(80) NOT NULL DEFAULT ''"},
		{"clicks", "device", "VARCHAR(32) NOT NULL DEFAULT ''"},
		{"clicks", "is_bot", "BOOLEAN NOT NULL DEFAULT FALSE"},
		{"clicks", "referrer_host", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"clicks", "utm_source", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"clicks", "utm_medium", "VARCHAR(255) NOT NULL DEFAULT ''"},
		{"clicks", "utm_campaign", "VARCHAR(255) NOT NULL DEFAULT ''"},
	}
	for _, c := range columns {
		if err := ensureColumn(db, c.table, c.name, c.definition); err != nil {
			return err
		}
	}
	indexes := []struct{ table, name, columns string }{
		{"links", "idx_links_tenant_id", "tenant_id,id"},
		{"links", "idx_links_user_id", "user_id,id"},
		{"links", "idx_links_visibility", "tenant_id,visibility,deleted_at,id"},
		{"clicks", "idx_clicks_created_at", "created_at"},
		{"clicks", "idx_clicks_link_created", "link_id,created_at"},
		{"clicks", "idx_clicks_country_created", "country_code,created_at,id"},
		{"clicks", "idx_clicks_device_created", "device,created_at,id"},
		{"tenant_domains", "idx_domains_tenant_active", "tenant_id,status,verified_at,id"},
	}
	for _, index := range indexes {
		if err := ensureIndex(db, index.table, index.name, index.columns); err != nil {
			return err
		}
	}
	_, err = db.Exec(`INSERT IGNORE INTO schema_migrations(version) VALUES(2),(3),(4)`)
	return err
}

func ensureColumn(db *sql.DB, table, column, definition string) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND COLUMN_NAME=?`, table, column).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE `%s` ADD COLUMN `%s` %s", table, column, definition))
	return err
}

func ensureIndex(db *sql.DB, table, name, columns string) error {
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.STATISTICS WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME=? AND INDEX_NAME=?`, table, name).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := db.Exec(fmt.Sprintf("CREATE INDEX `%s` ON `%s` (%s)", name, table, columns))
	return err
}
