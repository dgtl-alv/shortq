package db_test

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"shortq/internal/db"
	"shortq/internal/models"
	"shortq/internal/store"
)

func TestPostgresMigrationAndCoreDataPath(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(database); err != nil {
		t.Fatalf("migration is not repeatable: %v", err)
	}
	st := store.New(database)
	tenant, err := st.EnsureTenant("CI Tenant", "ci-tenant")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateUser("ci-user@example.test", "CI User", "customer", &tenant.ID, []byte("hash")); err != nil {
		t.Fatal(err)
	}
	user, _, err := st.UserByEmail("ci-user@example.test")
	if err != nil {
		t.Fatal(err)
	}
	link, err := st.CreateLink(user, models.Link{Slug: "ci-postgresql", TargetURL: "https://example.com", RedirectCode: 302, ForwardQuery: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := st.RecordClick(models.ClickEvent{LinkID: link.ID, IP: "127.0.0.1", Method: "GET", StatusCode: 302, RouteType: "default"}, true, nil)
	if err != nil || !ok {
		t.Fatalf("record click: ok=%v err=%v", ok, err)
	}
	got, err := st.LinkBySlug("ci-postgresql")
	if err != nil || got.Clicks != 1 {
		t.Fatalf("link clicks=%d err=%v", got.Clicks, err)
	}
}

func TestPostgresClickIdempotencyAndBatchPersistence(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	database, err := db.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := db.Migrate(database); err != nil {
		t.Fatal(err)
	}

	var eventIDNullable, eventIDType string
	if err := database.QueryRow(`SELECT is_nullable,data_type FROM information_schema.columns WHERE table_name='clicks' AND column_name='event_id'`).Scan(&eventIDNullable, &eventIDType); err != nil {
		t.Fatalf("event_id migration missing: %v", err)
	}
	if eventIDNullable != "YES" || eventIDType != "uuid" {
		t.Fatalf("event_id schema: nullable=%s type=%s", eventIDNullable, eventIDType)
	}
	var indexDefinition string
	if err := database.QueryRow(`SELECT indexdef FROM pg_indexes WHERE tablename='clicks' AND indexname='idx_clicks_event_id_unique'`).Scan(&indexDefinition); err != nil {
		t.Fatalf("event_id unique index missing: %v", err)
	}
	if !strings.Contains(indexDefinition, "WHERE (event_id IS NOT NULL)") {
		t.Fatalf("event_id index is not partial: %s", indexDefinition)
	}

	st := store.New(database)
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	tenant, err := st.EnsureTenant("Analytics Tenant "+suffix, "analytics-"+suffix)
	if err != nil {
		t.Fatal(err)
	}
	email := "analytics-" + suffix + "@example.test"
	if err := st.CreateUser(email, "Analytics User", "customer", &tenant.ID, []byte("hash")); err != nil {
		t.Fatal(err)
	}
	user, _, err := st.UserByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	maxClicks := int64(1)
	link, err := st.CreateLink(user, models.Link{Slug: "analytics-" + suffix, TargetURL: "https://example.com", RedirectCode: 302, ForwardQuery: true, MaxClicks: &maxClicks}, nil)
	if err != nil {
		t.Fatal(err)
	}
	batchLink, err := st.CreateLink(user, models.Link{Slug: "batch-" + suffix, TargetURL: "https://example.com/batch", RedirectCode: 302, ForwardQuery: true}, nil)
	if err != nil {
		t.Fatal(err)
	}

	dayOne := time.Date(2026, 9, 17, 16, 30, 0, 0, time.FixedZone("UTC-7", -7*60*60))
	duplicate := models.ClickEvent{EventID: "11111111-1111-4111-8111-111111111111", LinkID: link.ID, Method: "GET", StatusCode: 302, RouteType: "default", CountryCode: "ID", OccurredAt: dayOne}
	for attempt := 0; attempt < 2; attempt++ {
		ok, err := st.RecordClick(duplicate, true, &maxClicks)
		if err != nil || !ok {
			t.Fatalf("duplicate attempt %d: ok=%v err=%v", attempt+1, ok, err)
		}
	}

	newEvent := models.ClickEvent{EventID: "22222222-2222-4222-8222-222222222222", LinkID: batchLink.ID, Method: "GET", StatusCode: 302, RouteType: "default", CountryCode: "SG", OccurredAt: dayOne.Add(2 * time.Hour), Increment: true}
	inserted, err := st.RecordClicks([]models.ClickEvent{duplicate, newEvent})
	if err != nil || inserted != 1 {
		t.Fatalf("mixed batch: inserted=%d err=%v", inserted, err)
	}

	var rawClicks, linkClicks, rollupClicks int64
	if err := database.QueryRow(`SELECT COUNT(*) FROM clicks WHERE link_id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&rawClicks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COALESCE(SUM(clicks),0) FROM links WHERE id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&linkClicks); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COALESCE(SUM(clicks),0) FROM click_rollups_daily WHERE link_id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&rollupClicks); err != nil {
		t.Fatal(err)
	}
	if rawClicks != 2 || linkClicks != 2 || rollupClicks != 2 {
		t.Fatalf("idempotent totals: raw=%d link=%d rollup=%d", rawClicks, linkClicks, rollupClicks)
	}
	var rollupDays int
	if err := database.QueryRow(`SELECT COUNT(DISTINCT day) FROM click_rollups_daily WHERE link_id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&rollupDays); err != nil {
		t.Fatal(err)
	}
	if rollupDays != 2 {
		t.Fatalf("rollups must use occurred_at days, got %d days", rollupDays)
	}

	before := rawClicks
	_, err = st.RecordClicks([]models.ClickEvent{
		{EventID: "33333333-3333-4333-8333-333333333333", LinkID: batchLink.ID, Method: "GET", StatusCode: 302, RouteType: "default", OccurredAt: dayOne, Increment: true},
		{EventID: "44444444-4444-4444-8444-444444444444", LinkID: -1, Method: "GET", StatusCode: 302, RouteType: "default", OccurredAt: dayOne, Increment: true},
	})
	if err == nil {
		t.Fatal("batch with invalid link must roll back")
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM clicks WHERE link_id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&rawClicks); err != nil {
		t.Fatal(err)
	}
	if rawClicks != before {
		t.Fatalf("failed batch partially persisted: before=%d after=%d", before, rawClicks)
	}
	if err := database.QueryRow(`SELECT clicks FROM links WHERE id=$1`, batchLink.ID).Scan(&linkClicks); err != nil || linkClicks != 1 {
		t.Fatalf("failed batch changed counter: clicks=%d err=%v", linkClicks, err)
	}
	if err := database.QueryRow(`SELECT COALESCE(SUM(clicks),0) FROM click_rollups_daily WHERE link_id IN ($1,$2)`, link.ID, batchLink.ID).Scan(&rollupClicks); err != nil || rollupClicks != 2 {
		t.Fatalf("failed batch changed rollups: clicks=%d err=%v", rollupClicks, err)
	}

	ok, err := st.RecordClick(models.ClickEvent{EventID: "55555555-5555-4555-8555-555555555555", LinkID: link.ID, Method: "GET", StatusCode: 302, RouteType: "default", OccurredAt: dayOne}, true, &maxClicks)
	if err != nil || ok {
		t.Fatalf("max_clicks enforcement: ok=%v err=%v", ok, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM clicks WHERE event_id=$1`, "55555555-5555-4555-8555-555555555555").Scan(&rawClicks); err != nil || rawClicks != 0 {
		t.Fatalf("max_clicks raw event persisted: count=%d err=%v", rawClicks, err)
	}

	expired := models.ClickEvent{EventID: "66666666-6666-4666-8666-666666666666", LinkID: link.ID, Method: "GET", StatusCode: 410, RouteType: "expired", OccurredAt: dayOne}
	ok, err = st.RecordClick(expired, false, nil)
	if err != nil || !ok {
		t.Fatalf("expired event: ok=%v err=%v", ok, err)
	}
	if err := database.QueryRow(`SELECT clicks FROM links WHERE id=$1`, link.ID).Scan(&linkClicks); err != nil || linkClicks != 1 {
		t.Fatalf("expired event changed counter: clicks=%d err=%v", linkClicks, err)
	}

	legacy := models.ClickEvent{LinkID: link.ID, Method: "GET", StatusCode: 410, RouteType: "expired"}
	if ok, err = st.RecordClick(legacy, false, nil); err != nil || !ok {
		t.Fatalf("legacy null event ID: ok=%v err=%v", ok, err)
	}
	if ok, err = st.RecordClick(legacy, false, nil); err != nil || !ok {
		t.Fatalf("second legacy null event ID: ok=%v err=%v", ok, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM clicks WHERE link_id=$1 AND event_id IS NULL`, link.ID).Scan(&rawClicks); err != nil || rawClicks != 2 {
		t.Fatalf("legacy events deduplicated: count=%d err=%v", rawClicks, err)
	}
}
