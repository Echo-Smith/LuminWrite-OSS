package server

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/auth"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── JWT (HMAC-SHA256) ──────────────────────────────────

// jwtHeader is the fixed JWT header for HS256.
var jwtHeader = base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

// JWTPayload represents the claims inside a JWT.
type JWTPayload struct {
	Sub          string   `json:"sub"`                     // subject (user ID)
	Role         string   `json:"role"`                    // role: "user", "admin"
	WorkspaceIDs []string `json:"workspace_ids,omitempty"` // explicit tenant memberships
	Jti          string   `json:"jti"`                     // JWT ID (session ID) — unique per token, used for session tracking
	Iat          int64    `json:"iat"`                     // issued at (unix seconds)
	Exp          int64    `json:"exp"`                     // expiration (unix seconds)
}

// GenerateJWT creates a signed JWT token for the given user ID and role.
// The jti (JWT ID) is a unique session identifier used for multi-device session management.
func (s *Server) GenerateJWT(userID, role, jti string) (string, error) {
	now := time.Now()
	payload := JWTPayload{
		Sub:  userID,
		Role: role,
		Jti:  jti,
		Iat:  now.Unix(),
		Exp:  now.Add(s.cfg.JWT.Expiry).Unix(),
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	signingInput := jwtHeader + "." + payloadB64
	mac := hmac.New(sha256.New, []byte(s.cfg.JWT.Secret))
	mac.Write([]byte(signingInput))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	return signingInput + "." + signature, nil
}

// ValidateJWT validates a JWT token and returns the payload if valid.
func (s *Server) ValidateJWT(token string) (*JWTPayload, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid token format")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(s.cfg.JWT.Secret))
	mac.Write([]byte(signingInput))
	expectedSig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(parts[2]), []byte(expectedSig)) {
		return nil, fmt.Errorf("invalid signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}

	var payload JWTPayload
	if err := json.Unmarshal(payloadJSON, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal payload: %w", err)
	}

	if time.Now().Unix() > payload.Exp {
		return nil, fmt.Errorf("token expired")
	}

	return &payload, nil
}

// ─── Context Helpers ────────────────────────────────────

type contextKey string

const userCtxKey contextKey = "user"

func withUser(ctx context.Context, payload *JWTPayload) context.Context {
	return context.WithValue(ctx, userCtxKey, payload)
}

func userFromContext(ctx context.Context) *JWTPayload {
	val := ctx.Value(userCtxKey)
	if val == nil {
		return nil
	}
	return val.(*JWTPayload)
}

// principalFromPayload converts a JWT payload into a shared auth.Principal.
func principalFromPayload(payload *JWTPayload) *auth.Principal {
	return &auth.Principal{
		UserID: payload.Sub,
		Role:   payload.Role,
	}
}

// ─── JWT Middleware ─────────────────────────────────────

// jwtAuthMiddleware validates JWT from the Authorization header and stores
// the user info in the request context (both as *JWTPayload for server internals
// and as *auth.User for cross-package access).
func (s *Server) jwtAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")

		if token == "" {
			response.Err(w, http.StatusUnauthorized, "unauthorized", "authorization token required")
			return
		}

		payload, err := s.ValidateJWT(token)
		if err != nil {
			response.Err(w, http.StatusUnauthorized, "invalid_token", err.Error())
			return
		}

		// Check if session has been revoked (multi-device management)
		if s.isSessionRevoked(payload.Jti) {
			response.Err(w, http.StatusUnauthorized, "session_revoked", "this session has been revoked")
			return
		}

		// Update session activity timestamp (best-effort, non-blocking)
		s.updateSessionActivity(payload.Jti)

		ctx := withUser(r.Context(), payload)
		// Set the shared auth.Principal so editorial and other packages can read identity
		ctx = auth.SetPrincipal(ctx, principalFromPayload(payload))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// rejectGuestMiddleware rejects users with role "guest".
// Must be used after jwtAuthMiddleware.
func (s *Server) rejectGuestMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := userFromContext(r.Context())
		if user == nil {
			response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if user.Role == "guest" {
			response.Err(w, http.StatusForbidden, "forbidden", "guests cannot access this resource")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// jwtOptionalMiddleware works like jwtAuthMiddleware but does not reject
// requests without a token. Useful for endpoints that behave differently
// for authenticated vs anonymous users.
func (s *Server) jwtOptionalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")

		if token != "" {
			if payload, err := s.ValidateJWT(token); err == nil {
				ctx := withUser(r.Context(), payload)
				ctx = auth.SetPrincipal(ctx, principalFromPayload(payload))
				r = r.WithContext(ctx)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireRole wraps jwtAuthMiddleware and additionally checks the user role.
func (s *Server) requireRole(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return s.jwtAuthMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := userFromContext(r.Context())
			if user == nil || user.Role != role {
				response.Err(w, http.StatusForbidden, "forbidden", "insufficient permissions")
				return
			}
			next.ServeHTTP(w, r)
		}))
	}
}

// ─── Rate Limiter (Sliding Window) ──────────────────────

// RateLimiter implements a sliding-window rate limiter per client IP.
type RateLimiter struct {
	mu      sync.Mutex
	window  time.Duration
	maxReqs int
	clients map[string]*clientBucket
}

type clientBucket struct {
	count       int
	windowStart time.Time
}

// NewRateLimiter creates a new RateLimiter.
func NewRateLimiter(maxReqs int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		window:  window,
		maxReqs: maxReqs,
		clients: make(map[string]*clientBucket),
	}
	go rl.cleanup()
	return rl
}

// Allow checks if the given client IP is allowed to make a request.
func (rl *RateLimiter) Allow(clientIP string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	bucket, exists := rl.clients[clientIP]
	if !exists || now.Sub(bucket.windowStart) >= rl.window {
		rl.clients[clientIP] = &clientBucket{count: 1, windowStart: now}
		return true
	}

	if bucket.count >= rl.maxReqs {
		return false
	}

	bucket.count++
	return true
}

// cleanup periodically removes stale client buckets.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for ip, bucket := range rl.clients {
			if now.Sub(bucket.windowStart) >= rl.window*2 {
				delete(rl.clients, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// rateLimitMiddleware applies per-IP rate limiting.
func (s *Server) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.rateLimiter == nil {
			next.ServeHTTP(w, r)
			return
		}

		clientIP := getClientIP(r)
		if !s.rateLimiter.Allow(clientIP) {
			w.Header().Set("Retry-After", "60")
			response.Err(w, http.StatusTooManyRequests, "rate_limited",
				"too many requests, please try again later")
			return
		}

		next.ServeHTTP(w, r)
	})
}

// getClientIP extracts the client IP from the request, respecting X-Forwarded-For.
func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if idx := strings.Index(xff, ","); idx > 0 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx > 0 {
		return addr[:idx]
	}
	return addr
}
