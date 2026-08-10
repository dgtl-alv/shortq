package handlers

import (
	"context"
	"shortq/internal/models"
)

type userKey struct{}

func withUser(ctx context.Context, u models.User) context.Context {
	return context.WithValue(ctx, userKey{}, u)
}
func userFrom(ctx context.Context) (models.User, bool) {
	u, ok := ctx.Value(userKey{}).(models.User)
	return u, ok
}
