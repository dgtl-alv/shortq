package store

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestCreateAPIKeyReturnsInsertedID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectExec("INSERT INTO api_keys").WithArgs(int64(7), "smoke", "hash", "sq_live_prefix", "user").WillReturnResult(sqlmock.NewResult(42, 1))
	id, err := New(db).CreateAPIKey(7, "smoke", "hash", "sq_live_prefix", "user")
	if err != nil {
		t.Fatal(err)
	}
	if id != 42 {
		t.Fatalf("id = %d, want 42", id)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
