package store

import (
	"database/sql"
	"errors"
	"math/rand"
	"regexp"
	"strings"
	"time"

	"shortq/internal/models"
)

type Store struct{ DB *sql.DB }

var slugRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,80}$`)

func New(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) CreateUser(email, name, role string, tenantID *int64, pass []byte) error {
	if role == "" {
		role = "customer"
	}
	_, err := s.DB.Exec(`INSERT INTO users(tenant_id,email,name,role,password_hash) VALUES(?,?,?,?,?)`, tenantID, strings.ToLower(email), name, role, pass)
	return err
}

func (s *Store) UserByEmail(email string) (models.User, []byte, error) {
	var u models.User
	var p []byte
	var tid sql.NullInt64
	err := s.DB.QueryRow(`SELECT id,tenant_id,email,name,role,password_hash FROM users WHERE email=?`, strings.ToLower(email)).Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &p)
	if tid.Valid {
		u.TenantID = &tid.Int64
	}
	return u, p, err
}

func (s *Store) UserByID(id int64) (models.User, error) {
	var u models.User
	var tid sql.NullInt64
	err := s.DB.QueryRow(`SELECT id,tenant_id,email,name,role,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.CreatedAt)
	if tid.Valid {
		u.TenantID = &tid.Int64
	}
	return u, err
}

func (s *Store) UpdatePassword(uid int64, pass []byte) error {
	_, err := s.DB.Exec(`UPDATE users SET password_hash=? WHERE id=?`, pass, uid)
	return err
}

func (s *Store) SaveReset(uid int64, hash string, exp time.Time) error {
	_, err := s.DB.Exec(`INSERT INTO password_resets(user_id,token_hash,expires_at) VALUES(?,?,?)`, uid, hash, exp)
	return err
}

func (s *Store) ConsumeReset(hash string) (int64, error) {
	var id, uid int64
	err := s.DB.QueryRow(`SELECT id,user_id FROM password_resets WHERE token_hash=? AND used_at IS NULL AND expires_at>NOW()`, hash).Scan(&id, &uid)
	if err != nil {
		return 0, err
	}
	_, err = s.DB.Exec(`UPDATE password_resets SET used_at=NOW() WHERE id=?`, id)
	return uid, err
}

func (s *Store) CreateTenant(name, slug string) (models.Tenant, error) {
	_, err := s.DB.Exec(`INSERT INTO tenants(name,slug) VALUES(?,?)`, name, slug)
	if err != nil {
		return models.Tenant{}, err
	}
	return s.TenantBySlug(slug)
}

func (s *Store) TenantBySlug(slug string) (models.Tenant, error) {
	var t models.Tenant
	err := s.DB.QueryRow(`SELECT id,name,slug,created_at FROM tenants WHERE slug=?`, slug).Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt)
	return t, err
}

func (s *Store) ListTenants() ([]models.Tenant, error) {
	rows, err := s.DB.Query(`SELECT id,name,slug,created_at FROM tenants ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Tenant
	for rows.Next() {
		var t models.Tenant
		if err := rows.Scan(&t.ID, &t.Name, &t.Slug, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) ListUsers(scope models.User) ([]models.User, error) {
	q := `SELECT id,tenant_id,email,name,role,created_at FROM users`
	args := []any{}
	if scope.Role == "tenant" {
		q += ` WHERE tenant_id=? AND role='customer'`
		args = append(args, *scope.TenantID)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.User
	for rows.Next() {
		var u models.User
		var tid sql.NullInt64
		if err := rows.Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.CreatedAt); err != nil {
			return nil, err
		}
		if tid.Valid {
			u.TenantID = &tid.Int64
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateAPIKey(uid int64, name, hash, prefix string) error {
	_, err := s.DB.Exec(`INSERT INTO api_keys(user_id,name,key_hash,prefix) VALUES(?,?,?,?)`, uid, name, hash, prefix)
	return err
}

func (s *Store) UserByAPIKey(hash string) (models.User, error) {
	var u models.User
	var keyID int64
	var tid sql.NullInt64
	err := s.DB.QueryRow(`SELECT k.id,u.id,u.tenant_id,u.email,u.name,u.role FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=? AND k.revoked_at IS NULL`, hash).Scan(&keyID, &u.ID, &tid, &u.Email, &u.Name, &u.Role)
	if tid.Valid {
		u.TenantID = &tid.Int64
	}
	if err == nil {
		_, _ = s.DB.Exec(`UPDATE api_keys SET last_used_at=NOW() WHERE id=?`, keyID)
	}
	return u, err
}

func (s *Store) ListAPIKeys(uid int64) ([]models.APIKey, error) {
	rows, err := s.DB.Query(`SELECT id,name,prefix,last_used_at,created_at FROM api_keys WHERE user_id=? AND revoked_at IS NULL ORDER BY id DESC`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.APIKey
	for rows.Next() {
		var k models.APIKey
		var last sql.NullTime
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &last, &k.CreatedAt); err != nil {
			return nil, err
		}
		if last.Valid {
			k.LastUsedAt = &last.Time
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(uid, id int64) error {
	_, err := s.DB.Exec(`UPDATE api_keys SET revoked_at=NOW() WHERE id=? AND user_id=?`, id, uid)
	return err
}

func (s *Store) CreateLink(u models.User, slug, url, title string) (models.Link, error) {
	if slug == "" {
		slug = randomSlug()
	}
	if !slugRe.MatchString(slug) {
		return models.Link{}, errors.New("slug must be 3-80 letters, numbers, _ or -")
	}
	_, err := s.DB.Exec(`INSERT INTO links(user_id,tenant_id,slug,target_url,title) VALUES(?,?,?,?,?)`, u.ID, u.TenantID, slug, url, title)
	if err != nil {
		return models.Link{}, err
	}
	return s.LinkBySlug(slug)
}

func (s *Store) ListLinks(u models.User) ([]models.Link, error) {
	q := `SELECT id,user_id,tenant_id,slug,target_url,title,clicks,created_at FROM links`
	args := []any{}
	if u.Role == "tenant" {
		q += ` WHERE tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` WHERE user_id=?`
		args = append(args, u.ID)
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) LinkBySlug(slug string) (models.Link, error) {
	row := s.DB.QueryRow(`SELECT id,user_id,tenant_id,slug,target_url,title,clicks,created_at FROM links WHERE slug=?`, slug)
	return scanLink(row)
}

func (s *Store) DeleteLink(u models.User, id int64) error {
	q := `DELETE FROM links WHERE id=?`
	args := []any{id}
	if u.Role == "tenant" {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	_, err := s.DB.Exec(q, args...)
	return err
}

func (s *Store) TrackClick(linkID int64, ip, ua, ref string) {
	_, _ = s.DB.Exec(`UPDATE links SET clicks=clicks+1 WHERE id=?`, linkID)
	_, _ = s.DB.Exec(`INSERT INTO clicks(link_id,ip,user_agent,referrer) VALUES(?,?,?,?)`, linkID, ip, ua, ref)
}

func (s *Store) Analytics(u models.User) (models.Analytics, error) {
	var a models.Analytics
	where := ""
	args := []any{}
	if u.Role == "tenant" {
		where = " WHERE tenant_id=?"
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		where = " WHERE user_id=?"
		args = append(args, u.ID)
	}
	if err := s.DB.QueryRow(`SELECT COUNT(*),COALESCE(SUM(clicks),0) FROM links`+where, args...).Scan(&a.TotalLinks, &a.TotalClicks); err != nil {
		return a, err
	}
	clickWhere := ""
	clickArgs := []any{}
	if u.Role == "tenant" {
		clickWhere = " AND l.tenant_id=?"
		clickArgs = append(clickArgs, *u.TenantID)
	} else if u.Role == "customer" {
		clickWhere = " AND l.user_id=?"
		clickArgs = append(clickArgs, u.ID)
	}
	_ = s.DB.QueryRow(`SELECT COUNT(*) FROM clicks c JOIN links l ON l.id=c.link_id WHERE DATE(c.created_at)=CURRENT_DATE()`+clickWhere, clickArgs...).Scan(&a.TodayClicks)
	if u.Role == "superadmin" {
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM tenants`).Scan(&a.TotalTenants)
		_ = s.DB.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&a.TotalUsers)
	}
	return a, nil
}

type linkScanner interface{ Scan(dest ...any) error }

func scanLink(row linkScanner) (models.Link, error) {
	var l models.Link
	var tid sql.NullInt64
	err := row.Scan(&l.ID, &l.UserID, &tid, &l.Slug, &l.TargetURL, &l.Title, &l.Clicks, &l.CreatedAt)
	if tid.Valid {
		l.TenantID = &tid.Int64
	}
	return l, err
}

func randomSlug() string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, 7)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func (s *Store) EnsureSuperadmin(email string, pass []byte) error {
	_, _, err := s.UserByEmail(email)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return s.CreateUser(email, "Super Admin", "superadmin", nil, pass)
}
