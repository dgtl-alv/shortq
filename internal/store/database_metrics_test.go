package store

import (
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestTransactionMetricsMeasureCommitAndRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	metrics := &DatabaseMetrics{}
	store := NewWithMetrics(db, metrics)

	mock.ExpectBegin()
	mock.ExpectCommit()
	committed, err := store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := committed.Commit(); err != nil {
		t.Fatal(err)
	}
	// Deferred rollback after a successful commit must not be counted twice.
	_ = committed.Rollback()

	mock.ExpectBegin()
	mock.ExpectRollback()
	rolledBack, err := store.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := rolledBack.Rollback(); err != nil {
		t.Fatal(err)
	}

	snapshot := metrics.TransactionSnapshot()
	if snapshot.Started != 2 || snapshot.Committed != 1 || snapshot.RolledBack != 1 {
		t.Fatalf("transaction counts = %#v", snapshot)
	}
	if snapshot.DurationNanosecondsTotal == 0 || snapshot.DurationNanosecondsMax == 0 {
		t.Fatalf("transaction duration missing: %#v", snapshot)
	}
	if snapshot.CommitDurationNanosecondsTotal == 0 || snapshot.CommitDurationNanosecondsMax == 0 {
		t.Fatalf("commit duration missing: %#v", snapshot)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
