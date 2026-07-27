package auth

import "context"

// contextKey is a private type used for context keys in this package.
type contextKey string

const principalKey contextKey = "auth_principal"

// Principal represents the authenticated identity shared across all packages.
// It is set by JWT middleware and read by editorial, websocket, and other handlers.
// No package should access server-internal JWTPayload directly.
type Principal struct {
	UserID string
	OrgID  string
	Role   string
}

// SetPrincipal stores the authenticated principal in the context.
func SetPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFromContext retrieves the authenticated principal from the context.
// Returns nil if no principal is set (unauthenticated request).
func PrincipalFromContext(ctx context.Context) *Principal {
	if v := ctx.Value(principalKey); v != nil {
		if p, ok := v.(*Principal); ok {
			return p
		}
	}
	return nil
}

// UserIDFromContext is a convenience helper that returns the user ID string,
// or empty string if no authenticated principal is in context.
func UserIDFromContext(ctx context.Context) string {
	if p := PrincipalFromContext(ctx); p != nil {
		return p.UserID
	}
	return ""
}

// OrgIDFromContext returns the organization ID from context, or empty string.
func OrgIDFromContext(ctx context.Context) string {
	if p := PrincipalFromContext(ctx); p != nil {
		return p.OrgID
	}
	return ""
}

// IsAdminFromContext returns true if the principal in context has the "admin" role.
func IsAdminFromContext(ctx context.Context) bool {
	if p := PrincipalFromContext(ctx); p != nil {
		return p.Role == "admin"
	}
	return false
}

// ─── Backward compatibility ──────────────────────────────
// User is retained as an alias for Principal to avoid breaking existing
// code that references auth.User. New code should use Principal directly.
type User = Principal

// SetUser stores the authenticated user in the context.
// Deprecated: use SetPrincipal instead.
func SetUser(ctx context.Context, user *User) context.Context {
	return SetPrincipal(ctx, user)
}

// UserFromContext retrieves the authenticated user from the context.
// Deprecated: use PrincipalFromContext instead.
func UserFromContext(ctx context.Context) *User {
	return PrincipalFromContext(ctx)
}
