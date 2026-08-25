package store

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/models"
)

func TestCustomerVisibilityAndManagementScopes(t *testing.T) {
	tenantID := int64(46)
	user := models.User{ID: 9, TenantID: &tenantID, Role: "customer"}
	viewQ, viewArgs := addLinkViewScope("SELECT * FROM links WHERE deleted_at IS NULL", nil, user, "links", false)
	if !regexp.MustCompile(`user_id=\? OR .*visibility='department'`).MatchString(viewQ) || len(viewArgs) != 2 {
		t.Fatalf("unexpected view scope: %s %#v", viewQ, viewArgs)
	}
	manageQ, manageArgs := addLinkManageScope("UPDATE links SET title=?", nil, user, "links")
	if regexp.MustCompile("visibility").MatchString(manageQ) || !regexp.MustCompile(`user_id=\?`).MatchString(manageQ) || len(manageArgs) != 1 {
		t.Fatalf("unexpected manage scope: %s %#v", manageQ, manageArgs)
	}
}

func TestDeleteLinkSoftDeletesOwnedLink(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db)
	user := models.User{ID: 9, Role: "customer"}
	query := `UPDATE links SET deleted_at=UTC_TIMESTAMP() WHERE id=? AND deleted_at IS NULL AND links.user_id=?`
	mock.ExpectExec(regexp.QuoteMeta(query)).WithArgs(int64(22), int64(9)).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.DeleteLink(user, 22); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestSortedPointsGroupsLongTail(t *testing.T) {
	points := sortedPoints(map[string]int64{"a": 10, "b": 8, "c": 6, "d": 4}, 3)
	if len(points) != 3 || points[2].Key != "Other" || points[2].Clicks != 10 {
		t.Fatalf("unexpected points: %#v", points)
	}
}

func TestSuccessfulRedirectDefinition(t *testing.T) {
	if !isSuccessfulRedirect(302, "default") || isSuccessfulRedirect(410, "expired") || isSuccessfulRedirect(302, "expired") {
		t.Fatal("successful redirect definition changed")
	}
}
