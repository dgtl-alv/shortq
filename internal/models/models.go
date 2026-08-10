package models

import "time"

type Tenant struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Slug      string    `json:"slug"`
	CreatedAt time.Time `json:"created_at"`
}

type User struct {
	ID        int64     `json:"id"`
	TenantID  *int64    `json:"tenant_id"`
	Email     string    `json:"email"`
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"created_at"`
}

type Link struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"user_id"`
	TenantID  *int64    `json:"tenant_id"`
	Slug      string    `json:"slug"`
	TargetURL string    `json:"target_url"`
	Title     string    `json:"title"`
	ShortURL  string    `json:"short_url"`
	Clicks    int64     `json:"clicks"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKey struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
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
