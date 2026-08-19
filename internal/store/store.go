package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"shortq/internal/models"
)

type Store struct{ DB *sql.DB }

var slugRe = regexp.MustCompile(`^[a-zA-Z0-9_-]{3,80}$`)

var isoCountryCodes = func() map[string]bool {
	codes := `AD AE AF AG AI AL AM AO AQ AR AS AT AU AW AX AZ BA BB BD BE BF BG BH BI BJ BL BM BN BO BQ BR BS BT BV BW BY BZ CA CC CD CF CG CH CI CK CL CM CN CO CR CU CV CW CX CY CZ DE DJ DK DM DO DZ EC EE EG EH ER ES ET FI FJ FK FM FO FR GA GB GD GE GF GG GH GI GL GM GN GP GQ GR GS GT GU GW GY HK HM HN HR HT HU ID IE IL IM IN IO IQ IR IS IT JE JM JO JP KE KG KH KI KM KN KP KR KW KY KZ LA LB LC LI LK LR LS LT LU LV LY MA MC MD ME MF MG MH MK ML MM MN MO MP MQ MR MS MT MU MV MW MX MY MZ NA NC NE NF NG NI NL NO NP NR NU NZ OM PA PE PF PG PH PK PL PM PN PR PS PT PW PY QA RE RO RS RU RW SA SB SC SD SE SG SH SI SJ SK SL SM SN SO SR SS ST SV SX SY SZ TC TD TF TG TH TJ TK TL TM TN TO TR TT TV TW TZ UA UG UM US UY UZ VA VC VE VG VI VN VU WF WS YE YT ZA ZM ZW`
	out := make(map[string]bool, 249)
	for _, code := range strings.Fields(codes) {
		out[code] = true
	}
	return out
}()

func New(db *sql.DB) *Store { return &Store{DB: db} }

func (s *Store) SetUserActive(id int64, active bool) (models.User, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return models.User{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE users SET active=? WHERE id=?`, active, id)
	if err != nil {
		return models.User{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return models.User{}, sql.ErrNoRows
	}
	if !active {
		if _, err := tx.Exec(`UPDATE api_keys SET revoked_at=NOW() WHERE user_id=? AND revoked_at IS NULL`, id); err != nil {
			return models.User{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return models.User{}, err
	}
	return s.UserByID(id)
}

func (s *Store) SetDeletionAccess(id int64, enabled bool) (models.User, error) {
	res, err := s.DB.Exec(`UPDATE users SET deletion_access=? WHERE id=?`, enabled, id)
	if err != nil {
		return models.User{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.User{}, sql.ErrNoRows
	}
	return s.UserByID(id)
}

func (s *Store) RecordAudit(event models.AuditEvent) error {
	event.ActorEmail = truncateAuditField(event.ActorEmail, 190)
	event.AuthType = truncateAuditField(event.AuthType, 24)
	event.Action = truncateAuditField(event.Action, 80)
	event.TargetType = truncateAuditField(event.TargetType, 40)
	event.TargetID = truncateAuditField(event.TargetID, 255)
	event.IPAddress = truncateAuditField(event.IPAddress, 64)
	event.UserAgent = truncateAuditField(event.UserAgent, 500)
	before, _ := json.Marshal(event.Before)
	after, _ := json.Marshal(event.After)
	if event.Before == nil {
		before = nil
	}
	if event.After == nil {
		after = nil
	}
	_, err := s.DB.Exec(`INSERT INTO audit_events(actor_user_id,actor_email,auth_type,action,target_type,target_id,outcome,before_json,after_json,ip_address,user_agent) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, event.ActorUserID, event.ActorEmail, event.AuthType, event.Action, event.TargetType, event.TargetID, event.Outcome, before, after, event.IPAddress, event.UserAgent)
	return err
}

func truncateAuditField(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes])
}

func (s *Store) ListAuditEvents(values url.Values) (models.AuditEventPage, error) {
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			return models.AuditEventPage{}, errors.New("limit must be 1 to 500")
		}
		limit = parsed
	}
	q := `SELECT id,actor_user_id,actor_email,auth_type,action,target_type,target_id,outcome,before_json,after_json,ip_address,user_agent,created_at FROM audit_events WHERE 1=1`
	args := []any{}
	if raw := values.Get("cursor"); raw != "" {
		cursor, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || cursor < 1 {
			return models.AuditEventPage{}, errors.New("cursor must be positive")
		}
		q += ` AND id<?`
		args = append(args, cursor)
	}
	filters := []struct{ key, column string }{{"actor", "actor_email"}, {"action", "action"}, {"target_type", "target_type"}, {"outcome", "outcome"}}
	for _, filter := range filters {
		if value := strings.TrimSpace(values.Get(filter.key)); value != "" {
			q += ` AND ` + filter.column + `=?`
			args = append(args, value)
		}
	}
	if value := values.Get("from"); value != "" {
		q += ` AND created_at>=?`
		args = append(args, value)
	}
	if value := values.Get("to"); value != "" {
		q += ` AND created_at<?`
		args = append(args, value)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return models.AuditEventPage{}, err
	}
	defer rows.Close()
	page := models.AuditEventPage{Items: []models.AuditEvent{}}
	for rows.Next() {
		var event models.AuditEvent
		var actor sql.NullInt64
		var before, after []byte
		if err := rows.Scan(&event.ID, &actor, &event.ActorEmail, &event.AuthType, &event.Action, &event.TargetType, &event.TargetID, &event.Outcome, &before, &after, &event.IPAddress, &event.UserAgent, &event.CreatedAt); err != nil {
			return models.AuditEventPage{}, err
		}
		if actor.Valid {
			event.ActorUserID = &actor.Int64
		}
		if len(before) > 0 {
			_ = json.Unmarshal(before, &event.Before)
		}
		if len(after) > 0 {
			_ = json.Unmarshal(after, &event.After)
		}
		page.Items = append(page.Items, event)
	}
	if len(page.Items) > limit {
		page.NextCursor = page.Items[limit-1].ID
		page.Items = page.Items[:limit]
	}
	return page, rows.Err()
}

func (s *Store) PurgeOldAuditEvents() error {
	for {
		res, err := s.DB.Exec(`DELETE FROM audit_events WHERE created_at < NOW() - INTERVAL 365 DAY LIMIT 1000`)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil || n < 1000 {
			return err
		}
	}
}

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
	err := s.DB.QueryRow(`SELECT id,tenant_id,email,name,role,deletion_access,active,password_hash FROM users WHERE email=?`, strings.ToLower(email)).Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.DeletionAccess, &u.Active, &p)
	if tid.Valid {
		u.TenantID = &tid.Int64
	}
	return u, p, err
}

func (s *Store) UserByID(id int64) (models.User, error) {
	var u models.User
	var tid sql.NullInt64
	err := s.DB.QueryRow(`SELECT id,tenant_id,email,name,role,deletion_access,active,created_at FROM users WHERE id=?`, id).Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.DeletionAccess, &u.Active, &u.CreatedAt)
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

func (s *Store) EnsureTenant(name, slug string) (models.Tenant, error) {
	_, err := s.DB.Exec(`INSERT INTO tenants(name,slug) VALUES(?,?) ON DUPLICATE KEY UPDATE name=VALUES(name)`, name, slug)
	if err != nil {
		return models.Tenant{}, err
	}
	return s.TenantBySlug(slug)
}

func (s *Store) AssignUserTenant(email string, tenantID int64) error {
	_, err := s.DB.Exec(`UPDATE users SET tenant_id=? WHERE email=?`, tenantID, strings.ToLower(email))
	return err
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
	q := `SELECT id,tenant_id,email,name,role,deletion_access,active,created_at FROM users`
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
		if err := rows.Scan(&u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.DeletionAccess, &u.Active, &u.CreatedAt); err != nil {
			return nil, err
		}
		if tid.Valid {
			u.TenantID = &tid.Int64
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) CreateAPIKey(uid int64, name, hash, prefix, scope string) (int64, error) {
	if scope != "user" {
		scope = "account"
	}
	result, err := s.DB.Exec(`INSERT INTO api_keys(user_id,name,key_hash,prefix,scope) VALUES(?,?,?,?,?)`, uid, name, hash, prefix, scope)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (s *Store) UserByAPIKey(hash string) (models.User, string, error) {
	var u models.User
	var keyID int64
	var scope string
	var tid sql.NullInt64
	err := s.DB.QueryRow(`SELECT k.id,u.id,u.tenant_id,u.email,u.name,u.role,u.deletion_access,u.active,k.scope FROM api_keys k JOIN users u ON u.id=k.user_id WHERE k.key_hash=? AND k.revoked_at IS NULL AND u.active=TRUE`, hash).Scan(&keyID, &u.ID, &tid, &u.Email, &u.Name, &u.Role, &u.DeletionAccess, &u.Active, &scope)
	if tid.Valid {
		u.TenantID = &tid.Int64
	}
	if err == nil {
		_, _ = s.DB.Exec(`UPDATE api_keys SET last_used_at=NOW() WHERE id=?`, keyID)
	}
	return u, scope, err
}

func (s *Store) ListAPIKeys(uid int64, scope string) ([]models.APIKey, error) {
	q := `SELECT id,name,prefix,scope,last_used_at,created_at FROM api_keys WHERE user_id=? AND revoked_at IS NULL`
	if scope == "user" {
		q += ` AND scope='user'`
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.Query(q, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.APIKey
	for rows.Next() {
		var k models.APIKey
		var last sql.NullTime
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.Scope, &last, &k.CreatedAt); err != nil {
			return nil, err
		}
		if last.Valid {
			k.LastUsedAt = &last.Time
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAPIKey(uid, id int64, scope string) error {
	q := `UPDATE api_keys SET revoked_at=NOW() WHERE id=? AND user_id=?`
	if scope == "user" {
		q += ` AND scope='user'`
	}
	result, err := s.DB.Exec(q, id, uid)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

const linkColumns = `id,user_id,tenant_id,slug,target_url,title,clicks,redirect_code,expires_at,max_clicks,
COALESCE(expired_url,''),COALESCE(ios_url,''),COALESCE(android_url,''),forward_query,
utm_source,utm_medium,utm_campaign,utm_term,utm_content,COALESCE(tags_json,'[]'),password_hash,created_at`

func (s *Store) CreateLink(u models.User, link models.Link, passwordHash []byte) (models.Link, error) {
	if link.Slug == "" {
		link.Slug = randomSlug()
	}
	if !slugRe.MatchString(link.Slug) {
		return models.Link{}, errors.New("slug must be 3-80 letters, numbers, _ or -")
	}
	if link.RedirectCode == 0 {
		link.RedirectCode = 302
	}
	link.PasswordProtected = len(passwordHash) > 0
	if err := validateLink(link); err != nil {
		return models.Link{}, err
	}
	tags, _ := json.Marshal(normalizeTags(link.Tags))
	tx, err := s.DB.Begin()
	if err != nil {
		return models.Link{}, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO links(user_id,tenant_id,slug,target_url,title,redirect_code,expires_at,max_clicks,expired_url,ios_url,android_url,password_hash,forward_query,utm_source,utm_medium,utm_campaign,utm_term,utm_content,tags_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.ID, u.TenantID, link.Slug, link.TargetURL, link.Title, link.RedirectCode, link.ExpiresAt, link.MaxClicks, link.ExpiredURL, link.IOSURL, link.AndroidURL, passwordHash, link.ForwardQuery, link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent, tags)
	if err != nil {
		return models.Link{}, err
	}
	id, _ := res.LastInsertId()
	if err := replaceGeoTargets(tx, id, link.GeoTargets); err != nil {
		return models.Link{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Link{}, err
	}
	return s.LinkBySlug(link.Slug)
}

// CreateLinksBulk imports links in one transaction so a collision or invalid row writes nothing.
func (s *Store) CreateLinksBulk(u models.User, links []models.Link) ([]models.Link, error) {
	if len(links) == 0 {
		return []models.Link{}, nil
	}
	seen := map[string]bool{}
	for index := range links {
		if links[index].Slug == "" {
			links[index].Slug = randomSlug()
		}
		if !slugRe.MatchString(links[index].Slug) {
			return nil, fmt.Errorf("row %d: slug must be 3-80 letters, numbers, _ or -", index+2)
		}
		if seen[links[index].Slug] {
			return nil, fmt.Errorf("row %d: duplicate slug in CSV", index+2)
		}
		seen[links[index].Slug] = true
		if links[index].RedirectCode == 0 {
			links[index].RedirectCode = 302
		}
		if err := validateLink(links[index]); err != nil {
			return nil, fmt.Errorf("row %d: %w", index+2, err)
		}
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	insertStmt, err := tx.Prepare(`INSERT INTO links(user_id,tenant_id,slug,target_url,title,redirect_code,expires_at,max_clicks,expired_url,ios_url,android_url,forward_query,utm_source,utm_medium,utm_campaign,utm_term,utm_content,tags_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer insertStmt.Close()
	geoStmt, err := tx.Prepare(`INSERT INTO link_geo_targets(link_id,country_code,target_url) VALUES(?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer geoStmt.Close()
	ids := make([]int64, 0, len(links))
	for index, link := range links {
		tags, _ := json.Marshal(normalizeTags(link.Tags))
		res, err := insertStmt.Exec(u.ID, u.TenantID, link.Slug, link.TargetURL, link.Title, link.RedirectCode, link.ExpiresAt, link.MaxClicks, link.ExpiredURL, link.IOSURL, link.AndroidURL, link.ForwardQuery, link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent, tags)
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", index+2, err)
		}
		id, _ := res.LastInsertId()
		ids = append(ids, id)
		for _, target := range link.GeoTargets {
			if _, err := geoStmt.Exec(id, strings.ToUpper(strings.TrimSpace(target.CountryCode)), strings.TrimSpace(target.TargetURL)); err != nil {
				return nil, fmt.Errorf("row %d: %w", index+2, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.LinksByIDs(u, ids)
}

func (s *Store) ListLinks(u models.User) ([]models.Link, error) {
	return s.listLinks(u, 0, 0)
}

func (s *Store) ListLinksPage(u models.User, limit int, cursor int64) (models.LinkPage, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	links, err := s.listLinks(u, limit+1, cursor)
	if err != nil {
		return models.LinkPage{}, err
	}
	page := models.LinkPage{Items: links}
	if len(links) > limit {
		page.Items = links[:limit]
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, nil
}

func (s *Store) listLinks(u models.User, limit int, cursor int64) ([]models.Link, error) {
	q := `SELECT ` + linkColumns + ` FROM links WHERE 1=1`
	args := []any{}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	if cursor > 0 {
		q += ` AND id<?`
		args = append(args, cursor)
	}
	q += ` ORDER BY id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	var out []models.Link
	for rows.Next() {
		l, err := scanLink(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return s.hydrateLinks(out)
}

func (s *Store) LinksByIDs(u models.User, ids []int64) ([]models.Link, error) {
	if len(ids) == 0 {
		return []models.Link{}, nil
	}
	byID := make(map[int64]models.Link, len(ids))
	const chunkSize = 500
	for start := 0; start < len(ids); start += chunkSize {
		end := start + chunkSize
		if end > len(ids) {
			end = len(ids)
		}
		args := make([]any, 0, end-start+1)
		for _, id := range ids[start:end] {
			args = append(args, id)
		}
		q := `SELECT ` + linkColumns + ` FROM links WHERE id IN (` + placeholders(end-start) + `)`
		if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
			q += ` AND tenant_id=?`
			args = append(args, *u.TenantID)
		} else if u.Role == "customer" {
			q += ` AND user_id=?`
			args = append(args, u.ID)
		}
		rows, err := s.DB.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			link, err := scanLink(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			byID[link.ID] = link
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	ordered := make([]models.Link, 0, len(ids))
	for _, id := range ids {
		link, ok := byID[id]
		if !ok {
			return nil, sql.ErrNoRows
		}
		ordered = append(ordered, link)
	}
	return s.hydrateLinks(ordered)
}

type LinkUpdate struct {
	ID              int64
	Link            models.Link
	PasswordHash    []byte
	PasswordChanged bool
}

func (s *Store) UpdateLinksBulk(u models.User, updates []LinkUpdate) ([]models.Link, error) {
	if len(updates) == 0 {
		return []models.Link{}, nil
	}
	ids := make([]int64, 0, len(updates))
	for _, update := range updates {
		if err := validateLink(update.Link); err != nil {
			return nil, fmt.Errorf("link %d: %w", update.ID, err)
		}
		ids = append(ids, update.ID)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	lockQ := `SELECT id FROM links WHERE id IN (` + placeholders(len(ids)) + `)`
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		lockQ += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		lockQ += ` AND user_id=?`
		args = append(args, u.ID)
	}
	lockQ += ` FOR UPDATE`
	rows, err := tx.Query(lockQ, args...)
	if err != nil {
		return nil, err
	}
	found := 0
	for rows.Next() {
		found++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if found != len(ids) {
		return nil, sql.ErrNoRows
	}
	baseSQL := `UPDATE links SET target_url=?,title=?,redirect_code=?,expires_at=?,max_clicks=?,expired_url=?,ios_url=?,android_url=?,forward_query=?,utm_source=?,utm_medium=?,utm_campaign=?,utm_term=?,utm_content=?,tags_json=?`
	plainStmt, err := tx.Prepare(baseSQL + ` WHERE id=?`)
	if err != nil {
		return nil, err
	}
	defer plainStmt.Close()
	passwordStmt, err := tx.Prepare(baseSQL + `,password_hash=? WHERE id=?`)
	if err != nil {
		return nil, err
	}
	defer passwordStmt.Close()
	deleteGeoStmt, err := tx.Prepare(`DELETE FROM link_geo_targets WHERE link_id=?`)
	if err != nil {
		return nil, err
	}
	defer deleteGeoStmt.Close()
	insertGeoStmt, err := tx.Prepare(`INSERT INTO link_geo_targets(link_id,country_code,target_url) VALUES(?,?,?)`)
	if err != nil {
		return nil, err
	}
	defer insertGeoStmt.Close()
	for _, update := range updates {
		link := update.Link
		tags, _ := json.Marshal(normalizeTags(link.Tags))
		values := []any{link.TargetURL, link.Title, link.RedirectCode, link.ExpiresAt, link.MaxClicks, link.ExpiredURL, link.IOSURL, link.AndroidURL, link.ForwardQuery, link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent, tags}
		if update.PasswordChanged {
			values = append(values, update.PasswordHash, update.ID)
			_, err = passwordStmt.Exec(values...)
		} else {
			values = append(values, update.ID)
			_, err = plainStmt.Exec(values...)
		}
		if err != nil {
			return nil, err
		}
		if _, err := deleteGeoStmt.Exec(update.ID); err != nil {
			return nil, err
		}
		for _, target := range link.GeoTargets {
			if _, err := insertGeoStmt.Exec(update.ID, strings.ToUpper(strings.TrimSpace(target.CountryCode)), strings.TrimSpace(target.TargetURL)); err != nil {
				return nil, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.LinksByIDs(u, ids)
}

func (s *Store) LinkBySlug(slug string) (models.Link, error) {
	row := s.DB.QueryRow(`SELECT `+linkColumns+` FROM links WHERE slug=?`, slug)
	l, err := scanLink(row)
	if err != nil {
		return l, err
	}
	links, err := s.hydrateLinks([]models.Link{l})
	if err != nil {
		return l, err
	}
	return links[0], nil
}

func (s *Store) UpdateLink(u models.User, id int64, link models.Link, passwordHash []byte, passwordChanged bool) (models.Link, error) {
	if link.RedirectCode == 0 {
		link.RedirectCode = 302
	}
	if passwordChanged {
		link.PasswordProtected = len(passwordHash) > 0
	}
	if err := validateLink(link); err != nil {
		return models.Link{}, err
	}
	tags, _ := json.Marshal(normalizeTags(link.Tags))
	tx, err := s.DB.Begin()
	if err != nil {
		return models.Link{}, err
	}
	defer tx.Rollback()
	checkQ := `SELECT id FROM links WHERE id=?`
	checkArgs := []any{id}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		checkQ += ` AND tenant_id=?`
		checkArgs = append(checkArgs, *u.TenantID)
	} else if u.Role == "customer" {
		checkQ += ` AND user_id=?`
		checkArgs = append(checkArgs, u.ID)
	}
	checkQ += ` FOR UPDATE`
	var scopedID int64
	if err := tx.QueryRow(checkQ, checkArgs...).Scan(&scopedID); err != nil {
		return models.Link{}, err
	}
	q := `UPDATE links SET target_url=?,title=?,redirect_code=?,expires_at=?,max_clicks=?,expired_url=?,ios_url=?,android_url=?,forward_query=?,utm_source=?,utm_medium=?,utm_campaign=?,utm_term=?,utm_content=?,tags_json=?`
	args := []any{link.TargetURL, link.Title, link.RedirectCode, link.ExpiresAt, link.MaxClicks, link.ExpiredURL, link.IOSURL, link.AndroidURL, link.ForwardQuery, link.UTMSource, link.UTMMedium, link.UTMCampaign, link.UTMTerm, link.UTMContent, tags}
	if passwordChanged {
		q += `,password_hash=?`
		args = append(args, passwordHash)
	}
	q += ` WHERE id=?`
	args = append(args, id)
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	_, err = tx.Exec(q, args...)
	if err != nil {
		return models.Link{}, err
	}
	if err := replaceGeoTargets(tx, id, link.GeoTargets); err != nil {
		return models.Link{}, err
	}
	if err := tx.Commit(); err != nil {
		return models.Link{}, err
	}
	return s.LinkByID(u, id)
}

func (s *Store) LinkByID(u models.User, id int64) (models.Link, error) {
	q := `SELECT ` + linkColumns + ` FROM links WHERE id=?`
	args := []any{id}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	l, err := scanLink(s.DB.QueryRow(q, args...))
	if err != nil {
		return l, err
	}
	links, err := s.hydrateLinks([]models.Link{l})
	if err != nil {
		return l, err
	}
	return links[0], nil
}

func (s *Store) DeleteLink(u models.User, id int64) error {
	q := `DELETE FROM links WHERE id=?`
	args := []any{id}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteLinksBulk(u models.User, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	q := `DELETE FROM links WHERE id IN (` + placeholders(len(ids)) + `)`
	args := make([]any, 0, len(ids)+1)
	for _, id := range ids {
		args = append(args, id)
	}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND user_id=?`
		args = append(args, u.ID)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(q, args...)
	if err != nil {
		return err
	}
	deleted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if deleted != int64(len(ids)) {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) RecordClick(event models.ClickEvent, increment bool, maxClicks *int64) (bool, error) {
	tx, err := s.DB.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if increment {
		q := `UPDATE links SET clicks=clicks+1 WHERE id=?`
		args := []any{event.LinkID}
		if maxClicks != nil {
			q += ` AND clicks<?`
			args = append(args, *maxClicks)
		}
		res, err := tx.Exec(q, args...)
		if err != nil {
			return false, err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return false, tx.Rollback()
		}
	}
	_, err = tx.Exec(`INSERT INTO clicks(link_id,ip,user_agent,referrer,country_code,method,status_code,resolved_url,route_type,browser,os,device,is_bot,referrer_host,utm_source,utm_medium,utm_campaign) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		event.LinkID, event.IP, event.UserAgent, event.Referrer, event.CountryCode, event.Method, event.StatusCode, event.ResolvedURL, event.RouteType, event.Browser, event.OS, event.Device, event.IsBot, event.ReferrerHost, event.UTMSource, event.UTMMedium, event.UTMCampaign)
	if err != nil {
		return false, err
	}
	_, err = tx.Exec(`INSERT INTO click_rollups_daily(day,link_id,country_code,device,browser,referrer_host,utm_campaign,route_type,status_code,clicks) VALUES(CURRENT_DATE(),?,?,?,?,?,?,?,?,1) ON DUPLICATE KEY UPDATE clicks=clicks+1`,
		event.LinkID, event.CountryCode, event.Device, event.Browser, event.ReferrerHost, event.UTMCampaign, event.RouteType, event.StatusCode)
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) PurgeOldClicks() error {
	_, err := s.DB.Exec(`DELETE FROM clicks WHERE created_at < UTC_TIMESTAMP() - INTERVAL 90 DAY ORDER BY id LIMIT 10000`)
	return err
}

func (s *Store) Analytics(u models.User) (models.Analytics, error) {
	var a models.Analytics
	where := ""
	args := []any{}
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
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
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
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

func (s *Store) ListClicks(u models.User, values url.Values) (models.ClickPage, error) {
	limit := 100
	if raw := values.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 200 {
			return models.ClickPage{}, errors.New("limit must be 1 to 200")
		}
		limit = parsed
	}
	q := `SELECT c.id,c.link_id,l.slug,c.ip,c.country_code,c.method,c.status_code,COALESCE(c.resolved_url,''),c.route_type,c.user_agent,c.browser,c.os,c.device,c.is_bot,c.referrer,c.referrer_host,c.utm_source,c.utm_medium,c.utm_campaign,c.created_at FROM clicks c JOIN links l ON l.id=c.link_id WHERE 1=1`
	args := []any{}
	q, args = addLinkScope(q, args, u, "l")
	if raw := values.Get("link_id"); raw != "" {
		linkID, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || linkID < 1 {
			return models.ClickPage{}, errors.New("link_id must be a positive integer")
		}
		q += ` AND c.link_id=?`
		args = append(args, linkID)
	}
	if raw := strings.ToUpper(strings.TrimSpace(values.Get("country"))); raw != "" {
		if !isoCountryCodes[raw] {
			return models.ClickPage{}, errors.New("country must be an ISO-3166 alpha-2 code")
		}
		q += ` AND c.country_code=?`
		args = append(args, raw)
	}
	filters := []struct{ key, column string }{
		{"device", "c.device"},
		{"browser", "c.browser"}, {"campaign", "c.utm_campaign"}, {"route_type", "c.route_type"},
	}
	for _, filter := range filters {
		if value := strings.TrimSpace(values.Get(filter.key)); value != "" {
			q += ` AND ` + filter.column + `=?`
			args = append(args, value)
		}
	}
	from, to, err := parseTimeRange(values)
	if err != nil {
		return models.ClickPage{}, err
	}
	if !from.IsZero() {
		q += ` AND c.created_at>=?`
		args = append(args, from)
	}
	if !to.IsZero() {
		q += ` AND c.created_at<?`
		args = append(args, to)
	}
	if value := values.Get("cursor"); value != "" {
		cursor, err := strconv.ParseInt(value, 10, 64)
		if err != nil || cursor < 1 {
			return models.ClickPage{}, errors.New("cursor must be a positive click id")
		}
		q += ` AND c.id<?`
		args = append(args, cursor)
	}
	q += ` ORDER BY c.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return models.ClickPage{}, err
	}
	defer rows.Close()
	page := models.ClickPage{Items: []models.ClickEvent{}}
	for rows.Next() {
		var event models.ClickEvent
		if err := rows.Scan(&event.ID, &event.LinkID, &event.Slug, &event.IP, &event.CountryCode, &event.Method, &event.StatusCode, &event.ResolvedURL, &event.RouteType, &event.UserAgent, &event.Browser, &event.OS, &event.Device, &event.IsBot, &event.Referrer, &event.ReferrerHost, &event.UTMSource, &event.UTMMedium, &event.UTMCampaign, &event.CreatedAt); err != nil {
			return page, err
		}
		page.Items = append(page.Items, event)
	}
	if len(page.Items) == limit {
		page.NextCursor = page.Items[len(page.Items)-1].ID
	}
	return page, rows.Err()
}

func (s *Store) AnalyticsTimeseries(u models.User, values url.Values) ([]models.AnalyticsPoint, error) {
	q := `SELECT DATE_FORMAT(r.day,'%Y-%m-%d'),COALESCE(SUM(r.clicks),0) FROM click_rollups_daily r JOIN links l ON l.id=r.link_id WHERE 1=1`
	args := []any{}
	q, args = addLinkScope(q, args, u, "l")
	if value := values.Get("link_id"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err != nil || parsed < 1 {
			return nil, errors.New("link_id must be a positive integer")
		}
		q += ` AND r.link_id=?`
		args = append(args, value)
	}
	from, to, err := parseDateRange(values)
	if err != nil {
		return nil, err
	}
	if value := from; value != "" {
		q += ` AND r.day>=?`
		args = append(args, value)
	}
	if value := to; value != "" {
		q += ` AND r.day<?`
		args = append(args, value)
	}
	q += ` GROUP BY r.day ORDER BY r.day`
	return scanAnalyticsPoints(s.DB.Query(q, args...))
}

func (s *Store) AnalyticsBreakdown(u models.User, dimension string, values url.Values) ([]models.AnalyticsPoint, error) {
	columns := map[string]string{"country": "r.country_code", "device": "r.device", "browser": "r.browser", "referrer": "r.referrer_host", "campaign": "r.utm_campaign", "route": "r.route_type", "status": "CAST(r.status_code AS CHAR)"}
	column, ok := columns[dimension]
	if !ok {
		return nil, errors.New("group_by must be country, device, browser, referrer, campaign, route or status")
	}
	q := `SELECT COALESCE(NULLIF(` + column + `,''),'unknown'),COALESCE(SUM(r.clicks),0) FROM click_rollups_daily r JOIN links l ON l.id=r.link_id WHERE 1=1`
	args := []any{}
	q, args = addLinkScope(q, args, u, "l")
	if value := values.Get("link_id"); value != "" {
		if parsed, err := strconv.ParseInt(value, 10, 64); err != nil || parsed < 1 {
			return nil, errors.New("link_id must be a positive integer")
		}
		q += ` AND r.link_id=?`
		args = append(args, value)
	}
	from, to, err := parseDateRange(values)
	if err != nil {
		return nil, err
	}
	if value := from; value != "" {
		q += ` AND r.day>=?`
		args = append(args, value)
	}
	if value := to; value != "" {
		q += ` AND r.day<?`
		args = append(args, value)
	}
	q += ` GROUP BY ` + column + ` ORDER BY SUM(r.clicks) DESC LIMIT 100`
	return scanAnalyticsPoints(s.DB.Query(q, args...))
}

func parseTimeRange(values url.Values) (time.Time, time.Time, error) {
	var from, to time.Time
	for key, destination := range map[string]*time.Time{"from": &from, "to": &to} {
		raw := strings.TrimSpace(values.Get(key))
		if raw == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			if day, dayErr := time.Parse("2006-01-02", raw); dayErr == nil {
				parsed = day
			} else {
				return from, to, fmt.Errorf("%s must be RFC3339 or YYYY-MM-DD", key)
			}
		}
		*destination = parsed.UTC()
	}
	if !from.IsZero() && !to.IsZero() && !from.Before(to) {
		return from, to, errors.New("from must be before to")
	}
	return from, to, nil
}

func parseDateRange(values url.Values) (string, string, error) {
	from, to := strings.TrimSpace(values.Get("from")), strings.TrimSpace(values.Get("to"))
	for key, raw := range map[string]string{"from": from, "to": to} {
		if raw == "" {
			continue
		}
		if _, err := time.Parse("2006-01-02", raw); err != nil {
			return from, to, fmt.Errorf("%s must be YYYY-MM-DD", key)
		}
	}
	if from != "" && to != "" && from >= to {
		return from, to, errors.New("from must be before to")
	}
	return from, to, nil
}

func addLinkScope(q string, args []any, u models.User, alias string) (string, []any) {
	if u.Role == "tenant" || (u.Role == "customer" && u.TenantID != nil) {
		q += ` AND ` + alias + `.tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role == "customer" {
		q += ` AND ` + alias + `.user_id=?`
		args = append(args, u.ID)
	}
	return q, args
}

func scanAnalyticsPoints(rows *sql.Rows, err error) ([]models.AnalyticsPoint, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []models.AnalyticsPoint{}
	for rows.Next() {
		var point models.AnalyticsPoint
		if err := rows.Scan(&point.Key, &point.Clicks); err != nil {
			return nil, err
		}
		out = append(out, point)
	}
	return out, rows.Err()
}

type linkScanner interface{ Scan(dest ...any) error }

func scanLink(row linkScanner) (models.Link, error) {
	var l models.Link
	var tid sql.NullInt64
	var expires sql.NullTime
	var maxClicks sql.NullInt64
	var tags []byte
	err := row.Scan(&l.ID, &l.UserID, &tid, &l.Slug, &l.TargetURL, &l.Title, &l.Clicks, &l.RedirectCode, &expires, &maxClicks,
		&l.ExpiredURL, &l.IOSURL, &l.AndroidURL, &l.ForwardQuery, &l.UTMSource, &l.UTMMedium, &l.UTMCampaign, &l.UTMTerm, &l.UTMContent, &tags, &l.PasswordHash, &l.CreatedAt)
	if tid.Valid {
		l.TenantID = &tid.Int64
	}
	if expires.Valid {
		l.ExpiresAt = &expires.Time
	}
	if maxClicks.Valid {
		l.MaxClicks = &maxClicks.Int64
	}
	_ = json.Unmarshal(tags, &l.Tags)
	if l.Tags == nil {
		l.Tags = []string{}
	}
	l.PasswordProtected = len(l.PasswordHash) > 0
	return l, err
}

func (s *Store) hydrateLinks(links []models.Link) ([]models.Link, error) {
	if len(links) == 0 {
		return links, nil
	}
	positions := make(map[int64]int, len(links))
	for index := range links {
		links[index].GeoTargets = []models.GeoTarget{}
		positions[links[index].ID] = index
	}
	const chunkSize = 500
	for start := 0; start < len(links); start += chunkSize {
		end := start + chunkSize
		if end > len(links) {
			end = len(links)
		}
		args := make([]any, 0, end-start)
		for index := start; index < end; index++ {
			args = append(args, links[index].ID)
		}
		rows, err := s.DB.Query(`SELECT link_id,country_code,target_url FROM link_geo_targets WHERE link_id IN (`+placeholders(len(args))+`) ORDER BY link_id,country_code`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var linkID int64
			var target models.GeoTarget
			if err := rows.Scan(&linkID, &target.CountryCode, &target.TargetURL); err != nil {
				rows.Close()
				return nil, err
			}
			if index, ok := positions[linkID]; ok {
				links[index].GeoTargets = append(links[index].GeoTargets, target)
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return links, nil
}

func placeholders(count int) string {
	if count < 1 {
		return "NULL"
	}
	return strings.TrimSuffix(strings.Repeat("?,", count), ",")
}

func replaceGeoTargets(tx *sql.Tx, linkID int64, targets []models.GeoTarget) error {
	if _, err := tx.Exec(`DELETE FROM link_geo_targets WHERE link_id=?`, linkID); err != nil {
		return err
	}
	for _, target := range targets {
		country := strings.ToUpper(strings.TrimSpace(target.CountryCode))
		if _, err := tx.Exec(`INSERT INTO link_geo_targets(link_id,country_code,target_url) VALUES(?,?,?)`, linkID, country, strings.TrimSpace(target.TargetURL)); err != nil {
			return err
		}
	}
	return nil
}

func validateLink(link models.Link) error {
	if err := validHTTPURL(link.TargetURL); err != nil {
		return fmt.Errorf("target_url: %w", err)
	}
	for name, value := range map[string]string{"expired_url": link.ExpiredURL, "ios_url": link.IOSURL, "android_url": link.AndroidURL} {
		if value != "" {
			if err := validHTTPURL(value); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	if link.RedirectCode != 301 && link.RedirectCode != 302 && link.RedirectCode != 307 && link.RedirectCode != 308 {
		return errors.New("redirect_code must be 301, 302, 307 or 308")
	}
	dynamic := link.ExpiresAt != nil || link.MaxClicks != nil || link.IOSURL != "" || link.AndroidURL != "" || link.PasswordProtected || len(link.GeoTargets) > 0 || link.ForwardQuery
	if dynamic && (link.RedirectCode == 301 || link.RedirectCode == 308) {
		return errors.New("permanent redirect codes are not allowed with dynamic routing")
	}
	if link.MaxClicks != nil && *link.MaxClicks < 1 {
		return errors.New("max_clicks must be positive")
	}
	seenCountries := map[string]bool{}
	for _, target := range link.GeoTargets {
		country := strings.ToUpper(strings.TrimSpace(target.CountryCode))
		if !isoCountryCodes[country] || seenCountries[country] {
			return errors.New("geo target countries must be unique ISO-3166 alpha-2 codes")
		}
		seenCountries[country] = true
		if err := validHTTPURL(target.TargetURL); err != nil {
			return fmt.Errorf("geo target %s: %w", country, err)
		}
	}
	if len(normalizeTags(link.Tags)) > 20 {
		return errors.New("a link may have at most 20 tags")
	}
	return nil
}

// ValidateLink exposes the same validation used by persistent writes for CSV dry runs.
func ValidateLink(link models.Link) error {
	if link.Slug != "" && !slugRe.MatchString(link.Slug) {
		return errors.New("slug must be 3-80 letters, numbers, _ or -")
	}
	if link.RedirectCode == 0 {
		link.RedirectCode = 302
	}
	return validateLink(link)
}

func validHTTPURL(raw string) error {
	u, err := url.ParseRequestURI(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("must be an absolute http or https URL")
	}
	return nil
}

func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || len(tag) > 40 || seen[tag] {
			continue
		}
		seen[tag] = true
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
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

var domainRe = regexp.MustCompile(`^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

func cleanDomain(domain string) string {
	domain = strings.ToLower(strings.TrimSpace(domain))
	domain = strings.TrimPrefix(domain, "http://")
	domain = strings.TrimPrefix(domain, "https://")
	return strings.Trim(strings.Split(domain, "/")[0], ".")
}

func (s *Store) CreateTenantDomain(u models.User, tenantID int64, domain string, token string) (models.TenantDomain, error) {
	domain = cleanDomain(domain)
	if !domainRe.MatchString(domain) {
		return models.TenantDomain{}, errors.New("domain invalid")
	}
	if u.Role == "tenant" {
		if u.TenantID == nil || *u.TenantID != tenantID {
			return models.TenantDomain{}, errors.New("tenant scope invalid")
		}
	} else if u.Role != "superadmin" {
		return models.TenantDomain{}, errors.New("tenant or superadmin only")
	}
	_, err := s.DB.Exec(`INSERT INTO tenant_domains(tenant_id,domain,verification_token) VALUES(?,?,?)`, tenantID, domain, token)
	if err != nil {
		return models.TenantDomain{}, err
	}
	return s.TenantDomainByDomain(domain)
}

func (s *Store) ListTenantDomains(u models.User) ([]models.TenantDomain, error) {
	q := `SELECT id,tenant_id,domain,status,verification_token,created_at,verified_at,COALESCE(not_found_url,'') FROM tenant_domains`
	args := []any{}
	if u.Role == "tenant" {
		q += ` WHERE tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role != "superadmin" {
		return []models.TenantDomain{}, nil
	}
	q += ` ORDER BY id DESC`
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []models.TenantDomain
	for rows.Next() {
		d, err := scanTenantDomain(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) TenantDomainByDomain(domain string) (models.TenantDomain, error) {
	row := s.DB.QueryRow(`SELECT id,tenant_id,domain,status,verification_token,created_at,verified_at,COALESCE(not_found_url,'') FROM tenant_domains WHERE domain=?`, cleanDomain(domain))
	return scanTenantDomain(row)
}

func (s *Store) TenantDomainByID(u models.User, id int64) (models.TenantDomain, error) {
	if u.Role != "tenant" && u.Role != "superadmin" {
		return models.TenantDomain{}, errors.New("tenant or superadmin only")
	}
	if u.Role == "tenant" && u.TenantID == nil {
		return models.TenantDomain{}, errors.New("tenant scope invalid")
	}
	q := `SELECT id,tenant_id,domain,status,verification_token,created_at,verified_at,COALESCE(not_found_url,'') FROM tenant_domains WHERE id=?`
	args := []any{id}
	if u.Role == "tenant" {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	}
	row := s.DB.QueryRow(q, args...)
	return scanTenantDomain(row)
}

func (s *Store) SetTenantDomainVerified(id int64, verified bool) error {
	if verified {
		_, err := s.DB.Exec(`UPDATE tenant_domains SET status='active', verified_at=NOW() WHERE id=?`, id)
		return err
	}
	_, err := s.DB.Exec(`UPDATE tenant_domains SET status='pending', verified_at=NULL WHERE id=?`, id)
	return err
}

func (s *Store) DeleteTenantDomain(u models.User, id int64) error {
	if u.Role != "tenant" && u.Role != "superadmin" {
		return errors.New("tenant or superadmin only")
	}
	if u.Role == "tenant" && u.TenantID == nil {
		return errors.New("tenant scope invalid")
	}
	q := `DELETE FROM tenant_domains WHERE id=?`
	args := []any{id}
	if u.Role == "tenant" {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	}
	_, err := s.DB.Exec(q, args...)
	return err
}

func (s *Store) UpdateTenantDomain(u models.User, id int64, notFoundURL string) (models.TenantDomain, error) {
	if notFoundURL != "" {
		if err := validHTTPURL(notFoundURL); err != nil {
			return models.TenantDomain{}, err
		}
	}
	q := `UPDATE tenant_domains SET not_found_url=? WHERE id=?`
	args := []any{notFoundURL, id}
	if u.Role == "tenant" {
		q += ` AND tenant_id=?`
		args = append(args, *u.TenantID)
	} else if u.Role != "superadmin" {
		return models.TenantDomain{}, errors.New("tenant or superadmin only")
	}
	res, err := s.DB.Exec(q, args...)
	if err != nil {
		return models.TenantDomain{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return models.TenantDomain{}, sql.ErrNoRows
	}
	return s.TenantDomainByID(u, id)
}

func (s *Store) ActiveDomainForTenant(tenantID *int64) (string, error) {
	if tenantID == nil {
		return "", sql.ErrNoRows
	}
	var domain string
	err := s.DB.QueryRow(`SELECT domain FROM tenant_domains WHERE tenant_id=? AND status='active' ORDER BY verified_at DESC,id DESC LIMIT 1`, *tenantID).Scan(&domain)
	return domain, err
}

func (s *Store) ActiveDomainsForTenants(tenantIDs []int64) (map[int64]string, error) {
	out := map[int64]string{}
	if len(tenantIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(tenantIDs))
	for _, tenantID := range tenantIDs {
		args = append(args, tenantID)
	}
	rows, err := s.DB.Query(`SELECT tenant_id,SUBSTRING_INDEX(GROUP_CONCAT(domain ORDER BY verified_at DESC,id DESC),',',1) FROM tenant_domains WHERE status='active' AND tenant_id IN (`+placeholders(len(args))+`) GROUP BY tenant_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var tenantID int64
		var domain string
		if err := rows.Scan(&tenantID, &domain); err != nil {
			return nil, err
		}
		out[tenantID] = domain
	}
	return out, rows.Err()
}

func scanTenantDomain(row linkScanner) (models.TenantDomain, error) {
	var d models.TenantDomain
	var verified sql.NullTime
	err := row.Scan(&d.ID, &d.TenantID, &d.Domain, &d.Status, &d.VerificationToken, &d.CreatedAt, &verified, &d.NotFoundURL)
	if verified.Valid {
		d.VerifiedAt = &verified.Time
	}
	return d, err
}
