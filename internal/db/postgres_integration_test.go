package db_test

import (
	"os"
	"testing"

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
