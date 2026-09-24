package store

import (
	"context"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestRuntimeMetricsIncludePoolTransactionsAndPostgresLockWaits(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(20)
	db.SetMaxIdleConns(5)

	metrics := &DatabaseMetrics{}
	store := NewWithMetrics(db, metrics)

	query := `WITH own_activity AS (SELECT pid,state FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user), own_waits AS (SELECT locks.pid,MIN(locks.waitstart) AS wait_started_at FROM pg_locks locks JOIN own_activity activity ON activity.pid=locks.pid WHERE NOT locks.granted GROUP BY locks.pid) SELECT (SELECT COUNT(*) FROM own_waits), COALESCE((SELECT EXTRACT(EPOCH FROM clock_timestamp()-MIN(wait_started_at)) FROM own_waits),0), COUNT(*) FILTER (WHERE state='active' AND pid<>pg_backend_pid()), COUNT(*) FILTER (WHERE state='idle in transaction') FROM own_activity`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(sqlmock.NewRows([]string{"lock_waiting", "lock_wait_max_seconds", "active", "idle_in_transaction"}).AddRow(2, 1.5, 5, 1))

	snapshot, err := store.RuntimeMetrics(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Pool.MaxOpenConnections != 20 {
		t.Fatalf("pool = %#v", snapshot.Pool)
	}
	if snapshot.PostgreSQL.LockWaitingConnections != 2 || snapshot.PostgreSQL.LockWaitMaxMilliseconds != 1500 || snapshot.PostgreSQL.ActiveConnections != 5 || snapshot.PostgreSQL.IdleInTransactionConnections != 1 {
		t.Fatalf("postgresql = %#v", snapshot.PostgreSQL)
	}
	if snapshot.Transactions.Started != 0 {
		t.Fatalf("transactions = %#v", snapshot.Transactions)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
