package auth

import (
	"context"

	"github.com/google/uuid"
)

// ctxKey is the unexported context key type. Use it via the helpers below.
type ctxKey string

// UserIDKey is the request-scoped key under which the authenticated user's UUID is stored.
// Set by the api package's requireAuth middleware; read by any package that needs to scope
// a query to the authenticated user.
const UserIDKey ctxKey = "userID"

// UserIDFromContext returns the authenticated user's UUID from ctx, or uuid.Nil when absent.
// Callers that observe uuid.Nil should treat the request as unauthenticated.
func UserIDFromContext(ctx context.Context) uuid.UUID {
	if id, ok := ctx.Value(UserIDKey).(uuid.UUID); ok {
		return id
	}
	return uuid.Nil
}
