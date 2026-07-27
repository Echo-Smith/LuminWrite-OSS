package auth

import "context"

// contextKey is a private type used for context keys in this package.
type contextKey string

const userKey contextKey = "auth_user"

// User represents the authenticated user stored in context.
// Both server and editorial packages use this to share identity across middleware boundaries.
type User struct {
	ID   string
	Role string
}

// SetUser stores the authenticated user in the context.
func SetUser(ctx context.Context, user *User) context.Context {
	return context.WithValue(ctx, userKey, user)
}

// UserFromContext retrieves the authenticated user from the context.
// Returns nil if no user is set (unauthenticated request).
func UserFromContext(ctx context.Context) *User {
	if v := ctx.Value(userKey); v != nil {
		if u, ok := v.(*User); ok {
			return u
		}
	}
	return nil
}

// UserIDFromContext is a convenience helper that returns the user ID string,
// or empty string if no authenticated user is in context.
func UserIDFromContext(ctx context.Context) string {
	if u := UserFromContext(ctx); u != nil {
		return u.ID
	}
	return ""
}

// IsAdminFromContext returns true if the user in context has the "admin" role.
func IsAdminFromContext(ctx context.Context) bool {
	if u := UserFromContext(ctx); u != nil {
		return u.Role == "admin"
	}
	return false
}
