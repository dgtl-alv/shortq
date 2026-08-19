package handlers

import (
	"context"
	"shortq/internal/models"
)

type userKey struct{}
type principalKey struct{}

type principal struct {
	Actual    models.User
	Effective models.User
	Mode      string
	AuthType  string
}

func withPrincipal(ctx context.Context, p principal) context.Context {
	ctx = context.WithValue(ctx, userKey{}, p.Effective)
	return context.WithValue(ctx, principalKey{}, p)
}
func userFrom(ctx context.Context) (models.User, bool) {
	u, ok := ctx.Value(userKey{}).(models.User)
	return u, ok
}
func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok
}

func canSwitchMode(u models.User) bool {
	return u.Role == "superadmin" || u.Role == "tenant"
}

func canDelete(p principal) bool { return p.Actual.Role == "superadmin" || p.Actual.DeletionAccess }

func defaultDashboardMode(u models.User) string {
	if canSwitchMode(u) {
		return "admin"
	}
	return "user"
}

func effectivePrincipal(actual models.User, requestedMode, authType string) principal {
	mode := requestedMode
	if mode != "admin" && mode != "user" {
		mode = defaultDashboardMode(actual)
	}
	if mode == "admin" && !canSwitchMode(actual) {
		mode = "user"
	}
	effective := actual
	if mode == "user" {
		effective.Role = "customer"
	}
	return principal{Actual: actual, Effective: effective, Mode: mode, AuthType: authType}
}
