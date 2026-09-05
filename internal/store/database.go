package store

import (
	"database/sql"
	"strconv"
	"strings"
)

type database struct{ *sql.DB }
type transaction struct{ *sql.Tx }
type statement struct{ *sql.Stmt }

func wrapDatabase(db *sql.DB) *database { return &database{DB: db} }
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
	return &transaction{Tx: tx}, nil
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
