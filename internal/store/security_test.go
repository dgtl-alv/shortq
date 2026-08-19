package store

import (
	"strings"
	"testing"
)

func TestTruncateAuditFieldIsRuneSafe(t *testing.T) {
	got := truncateAuditField(strings.Repeat("界", 5), 3)
	if got != "界界界" {
		t.Fatalf("got %q", got)
	}
}
