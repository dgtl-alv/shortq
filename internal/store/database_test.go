package store

import "testing"

func TestRebind(t *testing.T) {
	got := rebind(`SELECT '?' AS literal WHERE id=? AND slug=?`)
	want := `SELECT '?' AS literal WHERE id=$1 AND slug=$2`
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
