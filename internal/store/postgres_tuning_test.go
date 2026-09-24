package store

import (
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/models"
)

func TestAnalyticsTodayUsesSargableCreatedAtRange(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*),COALESCE(SUM(clicks),0) FROM links WHERE deleted_at IS NULL`)).
		WillReturnRows(sqlmock.NewRows([]string{"count", "sum"}).AddRow(1, 2))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM clicks c JOIN links l ON l.id=c.link_id WHERE l.deleted_at IS NULL AND c.created_at>=CURRENT_DATE AND c.created_at<CURRENT_DATE+INTERVAL '1 day'`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(2))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM tenants`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM users`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

	analytics, err := New(database).Analytics(models.User{Role: "superadmin"})
	if err != nil {
		t.Fatal(err)
	}
	if analytics.TodayClicks != 2 {
		t.Fatalf("today clicks=%d want 2", analytics.TodayClicks)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeOldClicksOrdersByIndexedRetentionKey(t *testing.T) {
	database, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	query := `DELETE FROM clicks WHERE id IN (SELECT id FROM clicks WHERE created_at < CURRENT_TIMESTAMP - INTERVAL '90 days' ORDER BY created_at,id LIMIT 10000)`
	mock.ExpectExec(regexp.QuoteMeta(query)).WillReturnResult(sqlmock.NewResult(0, 0))

	if err := New(database).PurgeOldClicks(); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
