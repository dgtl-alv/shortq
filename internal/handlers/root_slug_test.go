package handlers

import "testing"

func TestIsReservedRootPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/", true},
		{"/app.js", true},
		{"/style.css", true},
		{"/favicon.ico", true},
		{"/api/v1/me", true},
		{"/nested/slug", true},
		{"/image.png", true},
		{"/abc123", false},
		{"/promo-alva", false},
	}
	for _, tt := range tests {
		if got := isReservedRootPath(tt.path); got != tt.want {
			t.Fatalf("isReservedRootPath(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
