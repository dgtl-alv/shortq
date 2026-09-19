package redirectcache

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"shortq/internal/models"
)

type fakeCache struct {
	value     CachedRedirect
	getErr    error
	setErr    error
	deleteErr error
	gets      int
	sets      int
	deletes   []string
	setTTL    time.Duration
}

func (f *fakeCache) Get(context.Context, string, string) (CachedRedirect, bool, error) {
	f.gets++
	if f.getErr != nil {
		return CachedRedirect{}, false, f.getErr
	}
	if f.value.Version == 0 {
		return CachedRedirect{}, false, nil
	}
	return f.value, true, nil
}

func (f *fakeCache) Set(_ context.Context, _ string, _ string, value CachedRedirect, ttl time.Duration) error {
	f.sets++
	f.setTTL = ttl
	if f.setErr != nil {
		return f.setErr
	}
	f.value = value
	return nil
}

func (f *fakeCache) Delete(_ context.Context, hostname, slug string) error {
	f.deletes = append(f.deletes, Key(hostname, slug))
	return f.deleteErr
}

type fakeObserver struct{ hits, misses, errors, invalidations int }

func (o *fakeObserver) Hit()          { o.hits++ }
func (o *fakeObserver) Miss()         { o.misses++ }
func (o *fakeObserver) Error()        { o.errors++ }
func (o *fakeObserver) Invalidation() { o.invalidations++ }

func testLink() models.Link {
	return models.Link{ID: 7, Slug: "GoNow", TargetURL: "https://example.com/path", RedirectCode: 302, ForwardQuery: true, GeoTargets: []models.GeoTarget{{CountryCode: "ID", TargetURL: "https://id.example.com"}}}
}

func TestResolverMissThenHit(t *testing.T) {
	cache := &fakeCache{}
	observer := &fakeObserver{}
	resolver := NewResolver(cache, observer)
	loads := 0
	loader := func() (models.Link, error) { loads++; return testLink(), nil }

	first, err := resolver.Resolve(context.Background(), " ExAmPlE.COM. ", "GoNow", loader)
	if err != nil || first.TargetURL != testLink().TargetURL {
		t.Fatalf("first resolve = %#v, %v", first, err)
	}
	second, err := resolver.Resolve(context.Background(), "example.com", "GoNow", loader)
	if err != nil || second.TargetURL != testLink().TargetURL {
		t.Fatalf("second resolve = %#v, %v", second, err)
	}
	if loads != 1 || cache.sets != 1 || cache.setTTL != DefaultTTL || observer.misses != 1 || observer.hits != 1 {
		t.Fatalf("loads=%d sets=%d ttl=%s stats=%+v", loads, cache.sets, cache.setTTL, observer)
	}
}

func TestResolverRedisFailureFallsBack(t *testing.T) {
	cache := &fakeCache{getErr: errors.New("redis unavailable")}
	observer := &fakeObserver{}
	resolver := NewResolver(cache, observer)
	loads := 0
	link, err := resolver.Resolve(context.Background(), "example.com", "safe", func() (models.Link, error) {
		loads++
		return testLink(), nil
	})
	if err != nil || link.ID != 7 || loads != 1 || observer.errors != 1 {
		t.Fatalf("link=%#v err=%v loads=%d stats=%+v", link, err, loads, observer)
	}
}

func TestResolverRedisWriteFailureFallsBackOnLaterRequests(t *testing.T) {
	cache := &fakeCache{setErr: errors.New("redis write unavailable")}
	observer := &fakeObserver{}
	resolver := NewResolver(cache, observer)
	loads := 0
	loader := func() (models.Link, error) {
		loads++
		return testLink(), nil
	}
	for range 2 {
		link, err := resolver.Resolve(context.Background(), "example.com", "safe", loader)
		if err != nil || link.ID != 7 {
			t.Fatalf("link=%#v err=%v", link, err)
		}
	}
	if loads != 2 || cache.sets != 2 || observer.errors != 2 {
		t.Fatalf("loads=%d sets=%d stats=%+v", loads, cache.sets, observer)
	}
}

func TestResolverPasswordAndMaxClicksBypassCache(t *testing.T) {
	for _, mutate := range []func(*models.Link){
		func(link *models.Link) { link.PasswordProtected = true; link.PasswordHash = []byte("secret-hash") },
		func(link *models.Link) { max := int64(2); link.MaxClicks = &max },
	} {
		cache := &fakeCache{}
		resolver := NewResolver(cache, &fakeObserver{})
		loads := 0
		loader := func() (models.Link, error) {
			loads++
			link := testLink()
			mutate(&link)
			return link, nil
		}
		if _, err := resolver.Resolve(context.Background(), "example.com", "safe", loader); err != nil {
			t.Fatal(err)
		}
		if _, err := resolver.Resolve(context.Background(), "example.com", "safe", loader); err != nil {
			t.Fatal(err)
		}
		if loads != 2 || cache.sets != 0 {
			t.Fatalf("loads=%d sets=%d", loads, cache.sets)
		}
	}
}

func TestCachedRedirectExcludesPasswordAndAnalytics(t *testing.T) {
	link := testLink()
	link.PasswordHash = []byte("secret")
	link.PasswordProtected = true
	link.Clicks = 99
	cached, ok := FromLink(link)
	if ok {
		t.Fatal("password-protected link must not be cacheable")
	}
	link.PasswordHash = nil
	link.PasswordProtected = false
	cached, ok = FromLink(link)
	if !ok || cached.Version != SchemaVersion {
		t.Fatalf("cached=%#v ok=%v", cached, ok)
	}
	restored, err := cached.Link()
	if err != nil {
		t.Fatal(err)
	}
	if restored.PasswordHash != nil || restored.PasswordProtected || restored.Clicks != 0 {
		t.Fatalf("sensitive or analytics data leaked: %#v", restored)
	}
	payload, err := json.Marshal(cached)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "password_hash", "password_protected", "clicks", "ip", "user_agent", "referrer", "event_id", "occurred_at", "user_id", "title"} {
		if _, exists := fields[forbidden]; exists {
			t.Fatalf("cache JSON contains forbidden analytics/sensitive field %q: %s", forbidden, payload)
		}
	}
}

func TestInvalidateAliasesUsesNormalizedUniqueKeys(t *testing.T) {
	cache := &fakeCache{}
	observer := &fakeObserver{}
	resolver := NewResolver(cache, observer)
	err := resolver.Invalidate(context.Background(), []string{"Base.Example.", "base.example", "custom.example"}, "Slug")
	if err != nil {
		t.Fatal(err)
	}
	if len(cache.deletes) != 2 || observer.invalidations != 2 {
		t.Fatalf("deletes=%v invalidations=%d", cache.deletes, observer.invalidations)
	}
}

func TestCreateEditDeleteInvalidateOnlyAfterCommit(t *testing.T) {
	for _, operation := range []string{"create", "edit-routing", "delete"} {
		t.Run(operation+"-rollback", func(t *testing.T) {
			cache := &fakeCache{}
			resolver := NewResolver(cache, &fakeObserver{})
			mutate := func() error { return errors.New("transaction rollback") }
			if err := InvalidateAfter(context.Background(), resolver, []string{"base.example", "alias.example"}, "slug", mutate); err == nil {
				t.Fatal("expected mutation error")
			}
			if len(cache.deletes) != 0 {
				t.Fatalf("invalidated after failed transaction: %v", cache.deletes)
			}
		})
		t.Run(operation+"-commit", func(t *testing.T) {
			cache := &fakeCache{}
			resolver := NewResolver(cache, &fakeObserver{})
			committed := false
			mutate := func() error { committed = true; return nil }
			if err := InvalidateAfter(context.Background(), resolver, []string{"base.example", "alias.example"}, "slug", mutate); err != nil {
				t.Fatal(err)
			}
			if !committed || len(cache.deletes) != 2 {
				t.Fatalf("committed=%v deletes=%v", committed, cache.deletes)
			}
		})
	}
}

func TestKeyIncludesNormalizedHostnameAndSlug(t *testing.T) {
	got := Key(" EXAMPLE.COM.:443 ", "Ab_C")
	want := "shortq:redirect:v1:example.com:Ab_C"
	if got != want {
		t.Fatalf("key=%q want=%q", got, want)
	}
}
