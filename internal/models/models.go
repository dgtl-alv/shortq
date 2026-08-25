package models

import "time"

type Tenant struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type TenantDomain struct {
	ID                int64      `json:"id"`
	TenantID          int64      `json:"tenant_id"`
	Domain            string     `json:"domain"`
	Status            string     `json:"status"`
	VerificationToken string     `json:"verification_token"`
	CreatedAt         time.Time  `json:"created_at"`
	VerifiedAt        *time.Time `json:"verified_at"`
	NotFoundURL       string     `json:"not_found_url"`
}

type User struct {
	ID             int64     `json:"id"`
	TenantID       *int64    `json:"tenant_id"`
	Email          string    `json:"email"`
	Name           string    `json:"name"`
	Role           string    `json:"role"`
	DeletionAccess bool      `json:"deletion_access"`
	Active         bool      `json:"active"`
	CreatedAt      time.Time `json:"created_at"`
}

type AuditEvent struct {
	ID          int64          `json:"id"`
	ActorUserID *int64         `json:"actor_user_id,omitempty"`
	ActorEmail  string         `json:"actor_email"`
	AuthType    string         `json:"auth_type"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Outcome     string         `json:"outcome"`
	Before      map[string]any `json:"before,omitempty"`
	After       map[string]any `json:"after,omitempty"`
	IPAddress   string         `json:"ip_address"`
	UserAgent   string         `json:"user_agent"`
	CreatedAt   time.Time      `json:"created_at"`
}

type AuditEventPage struct {
	Items      []AuditEvent `json:"items"`
	NextCursor int64        `json:"next_cursor,omitempty"`
}

type Link struct {
	ID                int64       `json:"id"`
	UserID            int64       `json:"user_id"`
	TenantID          *int64      `json:"tenant_id"`
	Slug              string      `json:"slug"`
	TargetURL         string      `json:"target_url"`
	Title             string      `json:"title"`
	Visibility        string      `json:"visibility"`
	ShortURL          string      `json:"short_url"`
	Clicks            int64       `json:"clicks"`
	RedirectCode      int         `json:"redirect_code"`
	ExpiresAt         *time.Time  `json:"expires_at,omitempty"`
	MaxClicks         *int64      `json:"max_clicks,omitempty"`
	ExpiredURL        string      `json:"expired_url,omitempty"`
	IOSURL            string      `json:"ios_url,omitempty"`
	AndroidURL        string      `json:"android_url,omitempty"`
	ForwardQuery      bool        `json:"forward_query"`
	UTMSource         string      `json:"utm_source,omitempty"`
	UTMMedium         string      `json:"utm_medium,omitempty"`
	UTMCampaign       string      `json:"utm_campaign,omitempty"`
	UTMTerm           string      `json:"utm_term,omitempty"`
	UTMContent        string      `json:"utm_content,omitempty"`
	Tags              []string    `json:"tags"`
	GeoTargets        []GeoTarget `json:"geo_targets"`
	PasswordProtected bool        `json:"password_protected"`
	PasswordHash      []byte      `json:"-"`
	CreatedAt         time.Time   `json:"created_at"`
	DeletedAt         *time.Time  `json:"deleted_at,omitempty"`
}

type LinkPage struct {
	Items      []Link `json:"items"`
	NextCursor int64  `json:"next_cursor,omitempty"`
}

type GeoTarget struct {
	CountryCode string `json:"country_code"`
	TargetURL   string `json:"target_url"`
}

type ClickEvent struct {
	ID           int64     `json:"id"`
	LinkID       int64     `json:"link_id"`
	Slug         string    `json:"slug"`
	IP           string    `json:"ip"`
	CountryCode  string    `json:"country_code"`
	Method       string    `json:"method"`
	StatusCode   int       `json:"status_code"`
	ResolvedURL  string    `json:"resolved_url"`
	RouteType    string    `json:"route_type"`
	UserAgent    string    `json:"user_agent"`
	Browser      string    `json:"browser"`
	OS           string    `json:"os"`
	Device       string    `json:"device"`
	IsBot        bool      `json:"is_bot"`
	Referrer     string    `json:"referrer"`
	ReferrerHost string    `json:"referrer_host"`
	UTMSource    string    `json:"utm_source"`
	UTMMedium    string    `json:"utm_medium"`
	UTMCampaign  string    `json:"utm_campaign"`
	CreatedAt    time.Time `json:"created_at"`
}

type ClickPage struct {
	Items      []ClickEvent `json:"items"`
	NextCursor int64        `json:"next_cursor,omitempty"`
}

type AnalyticsPoint struct {
	Key    string `json:"key"`
	Clicks int64  `json:"clicks"`
}

type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scope      string     `json:"scope"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

type Analytics struct {
	TotalLinks   int64 `json:"total_links"`
	TotalClicks  int64 `json:"total_clicks"`
	TodayClicks  int64 `json:"today_clicks"`
	TotalTenants int64 `json:"total_tenants,omitempty"`
	TotalUsers   int64 `json:"total_users,omitempty"`
}

type ReportSummary struct {
	AllTimeClicks           int64    `json:"all_time_clicks"`
	PeriodClicks            int64    `json:"period_clicks"`
	HumanClicks             int64    `json:"human_clicks"`
	BotClicks               int64    `json:"bot_clicks"`
	EstimatedUniqueVisitors int64    `json:"estimated_unique_visitors"`
	ExpiredAttempts         int64    `json:"expired_attempts"`
	AverageClicksPerDay     float64  `json:"average_clicks_per_day"`
	PeakDay                 string   `json:"peak_day,omitempty"`
	PeakDayClicks           int64    `json:"peak_day_clicks"`
	PreviousPeriodClicks    *int64   `json:"previous_period_clicks,omitempty"`
	ChangePercent           *float64 `json:"change_percent,omitempty"`
	ActiveLinks             int64    `json:"active_links,omitempty"`
	DeletedLinks            int64    `json:"deleted_links,omitempty"`
	PrivateLinks            int64    `json:"private_links,omitempty"`
	SharedLinks             int64    `json:"shared_links,omitempty"`
}

type ReportDailyPoint struct {
	Day    string `json:"day"`
	Clicks int64  `json:"clicks"`
	Human  int64  `json:"human_clicks"`
	Bots   int64  `json:"bot_clicks"`
}

type ReportLinkRank struct {
	ID            int64      `json:"id"`
	Slug          string     `json:"slug"`
	Title         string     `json:"title"`
	Visibility    string     `json:"visibility"`
	PeriodClicks  int64      `json:"period_clicks"`
	AllTimeClicks int64      `json:"all_time_clicks"`
	DeletedAt     *time.Time `json:"deleted_at,omitempty"`
}

type SafeClickEvent struct {
	ID           int64     `json:"id"`
	LinkID       int64     `json:"link_id"`
	Slug         string    `json:"slug"`
	CountryCode  string    `json:"country_code"`
	StatusCode   int       `json:"status_code"`
	RouteType    string    `json:"route_type"`
	Browser      string    `json:"browser"`
	OS           string    `json:"os"`
	Device       string    `json:"device"`
	IsBot        bool      `json:"is_bot"`
	ReferrerHost string    `json:"referrer_host"`
	UTMSource    string    `json:"utm_source"`
	UTMMedium    string    `json:"utm_medium"`
	UTMCampaign  string    `json:"utm_campaign"`
	CreatedAt    time.Time `json:"created_at"`
}

type AnalyticsReport struct {
	Subject            map[string]any              `json:"subject"`
	From               string                      `json:"from"`
	To                 string                      `json:"to"`
	Timezone           string                      `json:"timezone"`
	DetailRetainedDays int                         `json:"detail_retained_days"`
	UniqueIsEstimate   bool                        `json:"unique_is_estimate"`
	Summary            ReportSummary               `json:"summary"`
	Daily              []ReportDailyPoint          `json:"daily"`
	Breakdowns         map[string][]AnalyticsPoint `json:"breakdowns"`
	TopLinks           []ReportLinkRank            `json:"top_links,omitempty"`
	RecentEvents       []SafeClickEvent            `json:"recent_events"`
}
