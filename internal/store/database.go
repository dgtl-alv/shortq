package store

import (
	"database/sql"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type database struct {
	*sql.DB
	metrics *DatabaseMetrics
}
type transaction struct {
	*sql.Tx
	metrics  *DatabaseMetrics
	started  time.Time
	finished sync.Once
}
type statement struct{ *sql.Stmt }

type DatabaseMetrics struct {
	transactionsStarted                 atomic.Uint64
	transactionsCommitted               atomic.Uint64
	transactionsRolledBack              atomic.Uint64
	transactionDurationNanosecondsTotal atomic.Uint64
	transactionDurationNanosecondsMax   atomic.Uint64
	commitDurationNanosecondsTotal      atomic.Uint64
	commitDurationNanosecondsMax        atomic.Uint64
	commitFailures                      atomic.Uint64
}

type TransactionMetricsSnapshot struct {
	Started                        uint64
	Committed                      uint64
	RolledBack                     uint64
	DurationNanosecondsTotal       uint64
	DurationNanosecondsMax         uint64
	CommitDurationNanosecondsTotal uint64
	CommitDurationNanosecondsMax   uint64
	CommitFailures                 uint64
}

func wrapDatabase(db *sql.DB, metrics *DatabaseMetrics) *database {
	return &database{DB: db, metrics: metrics}
}
func (db *database) Exec(q string, args ...any) (sql.Result, error) {
	return db.DB.Exec(rebind(q), args...)
}
func (db *database) Query(q string, args ...any) (*sql.Rows, error) {
	return db.DB.Query(rebind(q), args...)
}
func (db *database) QueryRow(q string, args ...any) *sql.Row {
	return db.DB.QueryRow(rebind(q), args...)
}
func (db *database) Begin() (*transaction, error) {
	tx, err := db.DB.Begin()
	if err != nil {
		return nil, err
	}
	if db.metrics != nil {
		db.metrics.transactionsStarted.Add(1)
	}
	return &transaction{Tx: tx, metrics: db.metrics, started: time.Now()}, nil
}
func (tx *transaction) Commit() error {
	started := time.Now()
	err := tx.Tx.Commit()
	if tx.metrics != nil {
		commitDuration := time.Since(started)
		tx.metrics.commitDurationNanosecondsTotal.Add(uint64(commitDuration))
		updateAtomicMax(&tx.metrics.commitDurationNanosecondsMax, uint64(commitDuration))
		if err == nil {
			tx.metrics.transactionsCommitted.Add(1)
		} else {
			tx.metrics.commitFailures.Add(1)
		}
		tx.finishDuration()
	}
	return err
}
func (tx *transaction) Rollback() error {
	err := tx.Tx.Rollback()
	if tx.metrics != nil {
		tx.finished.Do(func() {
			tx.metrics.transactionsRolledBack.Add(1)
			tx.metrics.observeTransactionDuration(time.Since(tx.started))
		})
	}
	return err
}
func (tx *transaction) finishDuration() {
	tx.finished.Do(func() {
		tx.metrics.observeTransactionDuration(time.Since(tx.started))
	})
}
func (tx *transaction) Exec(q string, args ...any) (sql.Result, error) {
	return tx.Tx.Exec(rebind(q), args...)
}
func (tx *transaction) Query(q string, args ...any) (*sql.Rows, error) {
	return tx.Tx.Query(rebind(q), args...)
}
func (tx *transaction) QueryRow(q string, args ...any) *sql.Row {
	return tx.Tx.QueryRow(rebind(q), args...)
}
func (tx *transaction) Prepare(q string) (*statement, error) {
	stmt, err := tx.Tx.Prepare(rebind(q))
	if err != nil {
		return nil, err
	}
	return &statement{Stmt: stmt}, nil
}

func rebind(query string) string {
	var b strings.Builder
	arg := 1
	var quote byte
	for i := 0; i < len(query); i++ {
		c := query[i]
		if quote != 0 {
			b.WriteByte(c)
			if c == quote {
				if i+1 < len(query) && query[i+1] == quote {
					b.WriteByte(query[i+1])
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			b.WriteByte(c)
			continue
		}
		if c == '?' {
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(arg))
			arg++
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func jsonArgument(value []byte) any {
	if value == nil {
		return nil
	}
	return string(value)
}

func (m *DatabaseMetrics) observeTransactionDuration(duration time.Duration) {
	nanoseconds := uint64(duration)
	m.transactionDurationNanosecondsTotal.Add(nanoseconds)
	updateAtomicMax(&m.transactionDurationNanosecondsMax, nanoseconds)
}

func updateAtomicMax(value *atomic.Uint64, candidate uint64) {
	for current := value.Load(); candidate > current && !value.CompareAndSwap(current, candidate); current = value.Load() {
	}
}

func (m *DatabaseMetrics) TransactionSnapshot() TransactionMetricsSnapshot {
	if m == nil {
		return TransactionMetricsSnapshot{}
	}
	return TransactionMetricsSnapshot{
		Started:                        m.transactionsStarted.Load(),
		Committed:                      m.transactionsCommitted.Load(),
		RolledBack:                     m.transactionsRolledBack.Load(),
		DurationNanosecondsTotal:       m.transactionDurationNanosecondsTotal.Load(),
		DurationNanosecondsMax:         m.transactionDurationNanosecondsMax.Load(),
		CommitDurationNanosecondsTotal: m.commitDurationNanosecondsTotal.Load(),
		CommitDurationNanosecondsMax:   m.commitDurationNanosecondsMax.Load(),
		CommitFailures:                 m.commitFailures.Load(),
	}
}
