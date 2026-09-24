package db

import (
	"strings"
	"testing"
)

func TestMigrationIncludesClicksCreatedIDIndex(t *testing.T) {
	const wanted = `CREATE INDEX IF NOT EXISTS idx_clicks_created_id ON clicks(created_at,id)`
	for _, statement := range clickRetentionIndexStatements() {
		if statement == wanted {
			return
		}
	}
	t.Fatalf("migration is missing %q", wanted)
}

func TestMigrationDoesNotKeepRedundantCreatedAtOnlyIndex(t *testing.T) {
	for _, statement := range clickRetentionIndexStatements() {
		if strings.Contains(statement, "idx_clicks_created_at ON clicks(created_at)") {
			t.Fatalf("redundant created_at-only index remains: %q", statement)
		}
	}
}
