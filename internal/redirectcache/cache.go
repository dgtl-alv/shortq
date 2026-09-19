package redirectcache

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"shortq/internal/models"
)

const (
	SchemaVersion = 1
	DefaultTTL    = 24 * time.Hour
)

type Cache interface {
	Get(ctx context.Context, hostname, slug string) (CachedRedirect, bool, error)
	Set(ctx context.Context, hostname, slug string, value CachedRedirect, ttl time.Duration) error
	Delete(ctx context.Context, hostname, slug string) error
}

type Observer interface {
	Hit()
	Miss()
	Error()
	Invalidation()
}

type CachedRedirect struct {
	Version      int                `json:"version"`
	ID           int64              `json:"id"`
	TenantID     *int64             `json:"tenant_id,omitempty"`
	Slug         string             `json:"slug"`
	TargetURL    string             `json:"target_url"`
	RedirectCode int                `json:"redirect_code"`
	ExpiresAt    *time.Time         `json:"expires_at,omitempty"`
	ExpiredURL   string             `json:"expired_url,omitempty"`
	IOSURL       string             `json:"ios_url,omitempty"`
	AndroidURL   string             `json:"android_url,omitempty"`
	ForwardQuery bool               `json:"forward_query"`
	UTMSource    string             `json:"utm_source,omitempty"`
	UTMMedium    string             `json:"utm_medium,omitempty"`
	UTMCampaign  string             `json:"utm_campaign,omitempty"`
	UTMTerm      string             `json:"utm_term,omitempty"`
	UTMContent   string             `json:"utm_content,omitempty"`
	GeoTargets   []models.GeoTarget `json:"geo_targets,omitempty"`
}

func FromLink(link models.Link) (CachedRedirect, bool) {
	if link.PasswordProtected || len(link.PasswordHash) != 0 || link.MaxClicks != nil {
		return CachedRedirect{}, false
	}
	return CachedRedirect{
		Version: SchemaVersion, ID: link.ID, TenantID: link.TenantID,
		Slug: link.Slug, TargetURL: link.TargetURL, RedirectCode: link.RedirectCode,
		ExpiresAt: link.ExpiresAt, ExpiredURL: link.ExpiredURL, IOSURL: link.IOSURL, AndroidURL: link.AndroidURL,
		ForwardQuery: link.ForwardQuery, UTMSource: link.UTMSource, UTMMedium: link.UTMMedium,
		UTMCampaign: link.UTMCampaign, UTMTerm: link.UTMTerm, UTMContent: link.UTMContent,
		GeoTargets: append([]models.GeoTarget(nil), link.GeoTargets...),
	}, true
}

func (cached CachedRedirect) Link() (models.Link, error) {
	if cached.Version != SchemaVersion {
		return models.Link{}, fmt.Errorf("unsupported redirect cache version %d", cached.Version)
	}
	return models.Link{
		ID: cached.ID, TenantID: cached.TenantID, Slug: cached.Slug,
		TargetURL: cached.TargetURL, RedirectCode: cached.RedirectCode,
		ExpiresAt: cached.ExpiresAt, ExpiredURL: cached.ExpiredURL, IOSURL: cached.IOSURL,
		AndroidURL: cached.AndroidURL, ForwardQuery: cached.ForwardQuery, UTMSource: cached.UTMSource,
		UTMMedium: cached.UTMMedium, UTMCampaign: cached.UTMCampaign, UTMTerm: cached.UTMTerm,
		UTMContent: cached.UTMContent, GeoTargets: append([]models.GeoTarget(nil), cached.GeoTargets...),
	}, nil
}

func Key(hostname, slug string) string {
	return "shortq:redirect:v1:" + NormalizeHostname(hostname) + ":" + slug
}

func NormalizeHostname(hostname string) string {
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	if host, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = host
	} else if strings.Count(hostname, ":") == 1 {
		parts := strings.SplitN(hostname, ":", 2)
		if _, err := strconv.Atoi(parts[1]); err == nil {
			hostname = parts[0]
		}
	}
	return strings.Trim(hostname, ".")
}

type Resolver struct {
	cache    Cache
	observer Observer
}

func NewResolver(cache Cache, observer Observer) *Resolver {
	return &Resolver{cache: cache, observer: observer}
}

func (r *Resolver) Resolve(ctx context.Context, hostname, slug string, load func() (models.Link, error)) (models.Link, error) {
	if r == nil || r.cache == nil {
		return load()
	}
	cached, found, err := r.cache.Get(ctx, hostname, slug)
	if err != nil {
		r.error()
	} else if found {
		link, decodeErr := cached.Link()
		if decodeErr == nil {
			r.hit()
			return link, nil
		}
		r.error()
	} else {
		r.miss()
	}
	link, err := load()
	if err != nil {
		return models.Link{}, err
	}
	cached, eligible := FromLink(link)
	if !eligible {
		return link, nil
	}
	if err := r.cache.Set(ctx, hostname, slug, cached, DefaultTTL); err != nil {
		r.error()
	}
	return link, nil
}

func (r *Resolver) Invalidate(ctx context.Context, hostnames []string, slug string) error {
	if r == nil || r.cache == nil {
		return nil
	}
	seen := map[string]struct{}{}
	var errs []error
	for _, hostname := range hostnames {
		hostname = NormalizeHostname(hostname)
		if hostname == "" {
			continue
		}
		key := Key(hostname, slug)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		if err := r.cache.Delete(ctx, hostname, slug); err != nil {
			r.error()
			errs = append(errs, err)
			continue
		}
		if r.observer != nil {
			r.observer.Invalidation()
		}
	}
	return errors.Join(errs...)
}

func InvalidateAfter(ctx context.Context, resolver *Resolver, hostnames []string, slug string, mutate func() error) error {
	if err := mutate(); err != nil {
		return err
	}
	return resolver.Invalidate(ctx, hostnames, slug)
}

func (r *Resolver) hit() {
	if r.observer != nil {
		r.observer.Hit()
	}
}
func (r *Resolver) miss() {
	if r.observer != nil {
		r.observer.Miss()
	}
}
func (r *Resolver) error() {
	if r.observer != nil {
		r.observer.Error()
	}
}

type Metrics struct {
	hits          atomic.Uint64
	misses        atomic.Uint64
	errors        atomic.Uint64
	invalidations atomic.Uint64
}

func (m *Metrics) Hit()          { m.hits.Add(1) }
func (m *Metrics) Miss()         { m.misses.Add(1) }
func (m *Metrics) Error()        { m.errors.Add(1) }
func (m *Metrics) Invalidation() { m.invalidations.Add(1) }
func (m *Metrics) Snapshot() map[string]uint64 {
	return map[string]uint64{"hits": m.hits.Load(), "misses": m.misses.Load(), "errors": m.errors.Load(), "invalidations": m.invalidations.Load()}
}
