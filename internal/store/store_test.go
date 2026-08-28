package store

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/models"
)

func TestListLinksPageUsesBatchedGeoHydration(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	tenantID := int64(46)
	user := models.User{ID: 1, TenantID: &tenantID, Role: "tenant"}
	columns := []string{"id", "user_id", "tenant_id", "slug", "target_url", "title", "visibility", "clicks", "redirect_code", "expires_at", "max_clicks", "expired_url", "ios_url", "android_url", "forward_query", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "tags_json", "password_hash", "created_at", "deleted_at"}
	rows := sqlmock.NewRows(columns)
	for _, id := range []int64{10, 9, 8} {
		rows.AddRow(id, 1, tenantID, "slug-test", "https://example.org", "", "private", 0, 302, nil, nil, "", "", "", true, "", "", "", "", "", []byte(`[]`), nil, time.Now(), nil)
	}
	query := `SELECT ` + linkColumns + ` FROM links WHERE deleted_at IS NULL AND links.tenant_id=? ORDER BY id DESC LIMIT ?`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(tenantID, 3).WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT link_id,country_code,target_url FROM link_geo_targets WHERE link_id IN (?,?,?) ORDER BY link_id,country_code`)).
		WithArgs(int64(10), int64(9), int64(8)).
		WillReturnRows(sqlmock.NewRows([]string{"link_id", "country_code", "target_url"}).
			AddRow(10, "ID", "https://id.example.org").
			AddRow(9, "SG", "https://sg.example.org"))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id,name,email FROM users WHERE id IN (?)`)).
		WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"id", "name", "email"}).AddRow(1, "Link Owner", "owner@alvaauto.com"))

	page, err := store.ListLinksPage(user, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.NextCursor != 9 || len(page.Items[0].GeoTargets) != 1 || page.Items[0].CreatorEmail != "owner@alvaauto.com" {
		t.Fatalf("unexpected page: %#v", page)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("query count/regression: %v", err)
	}
}

func TestListLinksPageDoesNotExposeCreatorToCustomer(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := New(db)
	tenantID := int64(46)
	user := models.User{ID: 1, TenantID: &tenantID, Role: "customer"}
	columns := []string{"id", "user_id", "tenant_id", "slug", "target_url", "title", "visibility", "clicks", "redirect_code", "expires_at", "max_clicks", "expired_url", "ios_url", "android_url", "forward_query", "utm_source", "utm_medium", "utm_campaign", "utm_term", "utm_content", "tags_json", "password_hash", "created_at", "deleted_at"}
	rows := sqlmock.NewRows(columns).AddRow(10, 1, tenantID, "slug-test", "https://example.org", "", "private", 0, 302, nil, nil, "", "", "", true, "", "", "", "", "", []byte(`[]`), nil, time.Now(), nil)
	query := `SELECT ` + linkColumns + ` FROM links WHERE deleted_at IS NULL AND (links.user_id=? OR (links.tenant_id=? AND links.visibility='department' AND links.deleted_at IS NULL)) ORDER BY id DESC LIMIT ?`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WithArgs(int64(1), tenantID, 2).WillReturnRows(rows)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT link_id,country_code,target_url FROM link_geo_targets WHERE link_id IN (?) ORDER BY link_id,country_code`)).
		WithArgs(int64(10)).
		WillReturnRows(sqlmock.NewRows([]string{"link_id", "country_code", "target_url"}))

	page, err := store.ListLinksPage(user, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].CreatorName != "" || page.Items[0].CreatorEmail != "" {
		t.Fatalf("creator identity exposed to customer: %#v", page.Items)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unexpected creator query: %v", err)
	}
}

func TestValidateLinkAllowsEditableTargetWithImmutableSlug(t *testing.T) {
	link := models.Link{Slug: "fixed-slug", TargetURL: "https://example.org/new-target", RedirectCode: 302, ForwardQuery: true}
	if err := ValidateLink(link); err != nil {
		t.Fatalf("valid link rejected: %v", err)
	}
	if link.Slug != "fixed-slug" {
		t.Fatal("validation changed the immutable slug")
	}
}

func TestValidateLinkRejectsPermanentDynamicRedirect(t *testing.T) {
	link := models.Link{TargetURL: "https://example.org", RedirectCode: 301, IOSURL: "https://apps.apple.com/example"}
	err := ValidateLink(link)
	if err == nil || !strings.Contains(err.Error(), "permanent") {
		t.Fatalf("expected permanent dynamic redirect rejection, got %v", err)
	}
}

func TestValidateLinkRejectsDuplicateGeoCountry(t *testing.T) {
	link := models.Link{TargetURL: "https://example.org", RedirectCode: 302, GeoTargets: []models.GeoTarget{{CountryCode: "ID", TargetURL: "https://id.example.org"}, {CountryCode: "id", TargetURL: "https://another.example.org"}}}
	if err := ValidateLink(link); err == nil {
		t.Fatal("expected duplicate country rejection")
	}
}

func TestValidateLinkRejectsNonISOCountry(t *testing.T) {
	link := models.Link{TargetURL: "https://example.org", RedirectCode: 302, GeoTargets: []models.GeoTarget{{CountryCode: "ZZ", TargetURL: "https://zz.example.org"}}}
	if err := ValidateLink(link); err == nil || !strings.Contains(err.Error(), "ISO-3166") {
		t.Fatalf("expected ISO country rejection, got %v", err)
	}
}

func TestValidateLinkRejectsPermanentQueryForwarding(t *testing.T) {
	link := models.Link{TargetURL: "https://example.org", RedirectCode: 308, ForwardQuery: true}
	if err := ValidateLink(link); err == nil || !strings.Contains(err.Error(), "permanent") {
		t.Fatalf("expected permanent query-forwarding rejection, got %v", err)
	}
}

func TestValidateLinkAllowsStaticPermanentRedirect(t *testing.T) {
	link := models.Link{TargetURL: "https://example.org", RedirectCode: 308, ForwardQuery: false}
	if err := ValidateLink(link); err != nil {
		t.Fatalf("static permanent redirect rejected: %v", err)
	}
}
