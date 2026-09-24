package store

import (
	"context"
	"time"
)

const postgresRuntimeMetricsQuery = `WITH own_activity AS (SELECT pid,state FROM pg_stat_activity WHERE datname=current_database() AND usename=current_user), own_waits AS (SELECT locks.pid,MIN(locks.waitstart) AS wait_started_at FROM pg_locks locks JOIN own_activity activity ON activity.pid=locks.pid WHERE NOT locks.granted GROUP BY locks.pid) SELECT (SELECT COUNT(*) FROM own_waits), COALESCE((SELECT EXTRACT(EPOCH FROM clock_timestamp()-MIN(wait_started_at)) FROM own_waits),0), COUNT(*) FILTER (WHERE state='active' AND pid<>pg_backend_pid()), COUNT(*) FILTER (WHERE state='idle in transaction') FROM own_activity`

type PoolMetricsSnapshot struct {
	MaxOpenConnections int     `json:"max_open_connections"`
	OpenConnections    int     `json:"open_connections"`
	InUse              int     `json:"in_use"`
	Idle               int     `json:"idle"`
	WaitCount          int64   `json:"wait_count"`
	WaitDurationMillis float64 `json:"wait_duration_ms"`
	MaxIdleClosed      int64   `json:"max_idle_closed"`
	MaxIdleTimeClosed  int64   `json:"max_idle_time_closed"`
	MaxLifetimeClosed  int64   `json:"max_lifetime_closed"`
}

type PostgreSQLMetricsSnapshot struct {
	LockWaitingConnections       int64   `json:"lock_waiting_connections"`
	LockWaitMaxMilliseconds      float64 `json:"lock_wait_max_ms"`
	ActiveConnections            int64   `json:"active_connections"`
	IdleInTransactionConnections int64   `json:"idle_in_transaction_connections"`
}

type TransactionRuntimeMetricsSnapshot struct {
	Started                   uint64  `json:"started"`
	Committed                 uint64  `json:"committed"`
	RolledBack                uint64  `json:"rolled_back"`
	DurationMillisecondsTotal float64 `json:"duration_ms_total"`
	DurationMillisecondsMax   float64 `json:"duration_ms_max"`
	CommitDurationMillisTotal float64 `json:"commit_duration_ms_total"`
	CommitDurationMillisMax   float64 `json:"commit_duration_ms_max"`
	CommitFailures            uint64  `json:"commit_failures"`
}

type DatabaseRuntimeMetricsSnapshot struct {
	Pool         PoolMetricsSnapshot               `json:"pool"`
	Transactions TransactionRuntimeMetricsSnapshot `json:"transactions"`
	PostgreSQL   PostgreSQLMetricsSnapshot         `json:"postgresql"`
}

func (s *Store) RuntimeMetrics(ctx context.Context) (DatabaseRuntimeMetricsSnapshot, error) {
	stats := s.DB.DB.Stats()
	snapshot := DatabaseRuntimeMetricsSnapshot{
		Pool: PoolMetricsSnapshot{
			MaxOpenConnections: stats.MaxOpenConnections,
			OpenConnections:    stats.OpenConnections,
			InUse:              stats.InUse,
			Idle:               stats.Idle,
			WaitCount:          stats.WaitCount,
			WaitDurationMillis: durationMilliseconds(stats.WaitDuration),
			MaxIdleClosed:      stats.MaxIdleClosed,
			MaxIdleTimeClosed:  stats.MaxIdleTimeClosed,
			MaxLifetimeClosed:  stats.MaxLifetimeClosed,
		},
	}
	if s.DB.metrics != nil {
		transactions := s.DB.metrics.TransactionSnapshot()
		snapshot.Transactions = TransactionRuntimeMetricsSnapshot{
			Started:                   transactions.Started,
			Committed:                 transactions.Committed,
			RolledBack:                transactions.RolledBack,
			DurationMillisecondsTotal: nanosecondsMilliseconds(transactions.DurationNanosecondsTotal),
			DurationMillisecondsMax:   nanosecondsMilliseconds(transactions.DurationNanosecondsMax),
			CommitDurationMillisTotal: nanosecondsMilliseconds(transactions.CommitDurationNanosecondsTotal),
			CommitDurationMillisMax:   nanosecondsMilliseconds(transactions.CommitDurationNanosecondsMax),
			CommitFailures:            transactions.CommitFailures,
		}
	}
	var lockWaitMaxSeconds float64
	if err := s.DB.DB.QueryRowContext(ctx, postgresRuntimeMetricsQuery).Scan(
		&snapshot.PostgreSQL.LockWaitingConnections,
		&lockWaitMaxSeconds,
		&snapshot.PostgreSQL.ActiveConnections,
		&snapshot.PostgreSQL.IdleInTransactionConnections,
	); err != nil {
		return snapshot, err
	}
	snapshot.PostgreSQL.LockWaitMaxMilliseconds = lockWaitMaxSeconds * 1000
	return snapshot, nil
}

func durationMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func nanosecondsMilliseconds(nanoseconds uint64) float64 {
	return float64(nanoseconds) / float64(time.Millisecond)
}
