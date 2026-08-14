package store

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/models"
)

func TestDepartmentUserCanListUpdateAndDeleteDepartmentLinks(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	s := &Store{DB: db}
	tenantID := int64(10)
	user := models.User{ID: 7, TenantID: &tenantID, Role: "customer"}
	created := time.Now()

	mock.ExpectQuery("SELECT id,user_id,tenant_id,slug,target_url,title,clicks,created_at FROM links WHERE tenant_id=\\? ORDER BY id DESC").
		WithArgs(tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "tenant_id", "slug", "target_url", "title", "clicks", "created_at"}).
			AddRow(100, 8, tenantID, "dept-link", "https://example.com", "Dept link", 0, created))

	links, err := s.ListLinks(user)
	if err != nil {
		t.Fatalf("ListLinks: %v", err)
	}
	if len(links) != 1 || links[0].UserID != 8 || links[0].TenantID == nil || *links[0].TenantID != tenantID {
		t.Fatalf("ListLinks returned %#v", links)
	}

	mock.ExpectExec("UPDATE links SET slug=\\?, target_url=\\?, title=\\? WHERE id=\\? AND tenant_id=\\?").
		WithArgs("updated-link", "https://example.org", "Updated", int64(100), tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT id,user_id,tenant_id,slug,target_url,title,clicks,created_at FROM links WHERE id=\\? AND tenant_id=\\?").
		WithArgs(int64(100), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "tenant_id", "slug", "target_url", "title", "clicks", "created_at"}).
			AddRow(100, 8, tenantID, "updated-link", "https://example.org", "Updated", 0, created))

	updated, err := s.UpdateLink(user, 100, "updated-link", "https://example.org", "Updated")
	if err != nil {
		t.Fatalf("UpdateLink: %v", err)
	}
	if updated.UserID != 8 || updated.Slug != "updated-link" {
		t.Fatalf("UpdateLink returned %#v", updated)
	}

	mock.ExpectExec("DELETE FROM links WHERE id=\\? AND tenant_id=\\?").
		WithArgs(int64(100), tenantID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	if err := s.DeleteLink(user, 100); err != nil {
		t.Fatalf("DeleteLink: %v", err)
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}

func TestDepartmentUserCannotUpdateOtherDepartmentLink(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	s := &Store{DB: db}
	tenantID := int64(10)
	user := models.User{ID: 7, TenantID: &tenantID, Role: "customer"}

	mock.ExpectExec("UPDATE links SET slug=\\?, target_url=\\?, title=\\? WHERE id=\\? AND tenant_id=\\?").
		WithArgs("other-link", "https://example.org", "Other", int64(200), tenantID).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT id,user_id,tenant_id,slug,target_url,title,clicks,created_at FROM links WHERE id=\\? AND tenant_id=\\?").
		WithArgs(int64(200), tenantID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "tenant_id", "slug", "target_url", "title", "clicks", "created_at"}))

	if _, err := s.UpdateLink(user, 200, "other-link", "https://example.org", "Other"); err == nil {
		t.Fatal("UpdateLink expected error for link outside department")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet expectations: %v", err)
	}
}
