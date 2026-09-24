package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"

	"shortq/internal/config"
	"shortq/internal/models"
	"shortq/internal/store"
)

func TestAdminRuntimeReportsDatabaseAndRequestMetrics(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(20)

	metrics := &store.DatabaseMetrics{}
	st := store.NewWithMetrics(db, metrics)
	query := `WITH own_activity AS (SELECT pid,state FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user), own_waits AS (SELECT locks.pid,MIN(locks.waitstart) AS wait_started_at FROM pg_locks locks JOIN own_activity activity ON activity.pid=locks.pid WHERE NOT locks.granted GROUP BY locks.pid) SELECT (SELECT COUNT(*) FROM own_waits), COALESCE((SELECT EXTRACT(EPOCH FROM clock_timestamp()-MIN(wait_started_at)) FROM own_waits),0), COUNT(*) FILTER (WHERE state='active' AND pid<>pg_backend_pid()), COUNT(*) FILTER (WHERE state='idle in transaction') FROM own_activity`
	mock.ExpectQuery(regexp.QuoteMeta(query)).WillReturnRows(sqlmock.NewRows([]string{"lock_waiting", "lock_wait_max_seconds", "active", "idle_in_transaction"}).AddRow(1, 0.25, 3, 0))

	h := &Handler{
		C:              config.Config{DatabaseURL: "postgres://user@example.invalid/shortq"},
		S:              st,
		requestMetrics: &RequestMetrics{},
		clickQueue:     true,
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req = req.WithContext(withPrincipal(req.Context(), effectivePrincipal(models.User{ID: 1, Role: "superadmin", Active: true}, "admin", "session")))
	res := httptest.NewRecorder()
	h.adminRuntime(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	database := objectField(t, body, "database")
	pool := objectField(t, database, "pool")
	if pool["max_open_connections"] != float64(20) {
		t.Fatalf("pool = %#v", pool)
	}
	postgres := objectField(t, database, "postgresql")
	if postgres["lock_waiting_connections"] != float64(1) || postgres["lock_wait_max_ms"] != float64(250) {
		t.Fatalf("postgresql = %#v", postgres)
	}
	transactions := objectField(t, database, "transactions")
	if _, ok := transactions["commit_duration_ms_total"]; !ok {
		t.Fatalf("transactions = %#v", transactions)
	}
	requests := objectField(t, body, "requests")
	if _, ok := requests["in_flight"]; !ok {
		t.Fatalf("requests = %#v", requests)
	}
	if body["postgresql_metrics_available"] != true {
		t.Fatalf("postgresql_metrics_available = %#v", body["postgresql_metrics_available"])
	}
	if _, ok := body["queue"]; ok {
		t.Fatalf("unexpected queue snapshot = %#v", body["queue"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestAdminRuntimeKeepsLocalDatabaseMetricsWhenPostgresMetricsFail(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(12)

	st := store.NewWithMetrics(db, &store.DatabaseMetrics{})
	mock.ExpectQuery("WITH own_activity").WillReturnError(errors.New("postgres metrics unavailable"))

	h := &Handler{
		C:              config.Config{DatabaseURL: "postgres://user@example.invalid/shortq"},
		S:              st,
		requestMetrics: &RequestMetrics{},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/runtime", nil)
	req = req.WithContext(withPrincipal(req.Context(), effectivePrincipal(models.User{ID: 1, Role: "superadmin", Active: true}, "admin", "session")))
	res := httptest.NewRecorder()
	h.adminRuntime(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", res.Code, res.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	database := objectField(t, body, "database")
	pool := objectField(t, database, "pool")
	if pool["max_open_connections"] != float64(12) {
		t.Fatalf("pool = %#v", pool)
	}
	transactions := objectField(t, database, "transactions")
	if _, ok := transactions["duration_ms_total"]; !ok {
		t.Fatalf("transactions = %#v", transactions)
	}
	if body["postgresql_metrics_available"] != false {
		t.Fatalf("postgresql_metrics_available = %#v", body["postgresql_metrics_available"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func objectField(t *testing.T, body map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := body[key].(map[string]any)
	if !ok {
		t.Fatalf("%s missing or invalid in %#v", key, body)
	}
	return value
}
