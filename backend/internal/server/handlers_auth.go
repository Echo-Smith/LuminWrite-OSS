package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin User ID ───────────────────────────────────────

// AdminUserID is the fixed UUID for the config-based admin account.
// It is seeded into the users table by migration 019 so that admin
// traces are properly associated and all UUID-based queries work.
const AdminUserID = "00000000-0000-0000-0000-000000000001"

// ─── Auth Handlers ──────────────────────────────────────

// handleLogin authenticates a user and returns a JWT token.
// Supports both username/password and API key authentication.
//
// POST /api/v2/auth/login
// Body: {"username": "...", "password": "..."} or {"api_key": "..."}
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		APIKey   string `json:"api_key"`
		UserID   string `json:"user_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Mode 1: API key authentication
	if req.APIKey != "" {
		userID, role, ok := s.authenticateAPIKey(req.APIKey)
		if !ok {
			response.Err(w, http.StatusUnauthorized, "invalid_credentials", "invalid API key")
			return
		}
		username := s.lookupUsername(r.Context(), userID)
		s.issueToken(w, userID, role, username)
		return
	}

	// Mode 2: Username/password authentication
	if req.Username != "" && req.Password != "" {
		userID, role, username, ok := s.authenticatePassword(req.Username, req.Password)
		if !ok {
			response.Err(w, http.StatusUnauthorized, "invalid_credentials", "invalid username or password")
			return
		}
		s.issueToken(w, userID, role, username)
		return
	}

	// Mode 3: Anonymous/guest login (for public API access with rate limits)
	if req.UserID != "" {
		username := s.lookupUsername(r.Context(), req.UserID)
		s.issueToken(w, req.UserID, "user", username)
		return
	}

	response.Err(w, http.StatusBadRequest, "bad_request",
		"provide username/password, api_key, or user_id")
}

// handleRefreshToken refreshes an existing valid JWT token.
//
// POST /api/v2/auth/refresh
// Header: Authorization: Bearer <existing-token>
func (s *Server) handleRefreshToken(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("Authorization")
	if len(token) > 7 && token[:7] == "Bearer " {
		token = token[7:]
	} else {
		token = ""
	}

	if token == "" {
		// Also accept token in body
		var req struct {
			Token string `json:"token"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token = req.Token
	}

	if token == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "token required")
		return
	}

	payload, err := s.ValidateJWT(token)
	if err != nil {
		response.Err(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}

	// Look up username for the user
	username := s.lookupUsername(r.Context(), payload.Sub)

	// Issue a new token with fresh expiry
	s.issueToken(w, payload.Sub, payload.Role, username)
}

// handleVerifyToken returns the user info from a valid JWT.
//
// GET /api/v2/auth/verify
// Header: Authorization: Bearer <token>
func (s *Server) handleVerifyToken(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "no user in context")
		return
	}

	username := s.lookupUsername(r.Context(), user.Sub)

	response.OK(w, map[string]interface{}{
		"user_id":    user.Sub,
		"username":   username,
		"role":       user.Role,
		"issued_at":  user.Iat,
		"expires_at": user.Exp,
		"expires_in": max(0, user.Exp-time.Now().Unix()),
	})
}

// ─── Auth Helpers ───────────────────────────────────────

// issueToken generates a JWT and sends it in the response.
func (s *Server) issueToken(w http.ResponseWriter, userID, role, username string) {
	token, err := s.GenerateJWT(userID, role)
	if err != nil {
		slog.Error("failed to generate JWT", "error", err, "user_id", userID)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to generate token")
		return
	}

	response.OK(w, map[string]interface{}{
		"token":      token,
		"token_type": "Bearer",
		"expires_in": int(s.cfg.JWT.Expiry.Seconds()),
		"user_id":    userID,
		"username":   username,
		"role":       role,
		"issued_at":  time.Now().Format(time.RFC3339),
	})
}

// lookupUsername retrieves the display name (uid) for a given user ID from the database.
// Returns "" if the database is unavailable or the user is not found.
func (s *Server) lookupUsername(ctx context.Context, userID string) string {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		return ""
	}
	var name string
	err := s.adminRepo.DB().QueryRowContext(ctx, `
		SELECT COALESCE(uid, '') FROM users WHERE id = $1
	`, userID).Scan(&name)
	if err != nil {
		return ""
	}
	return name
}

// authenticateAPIKey validates an API key against the database.
func (s *Server) authenticateAPIKey(apiKey string) (userID, role string, ok bool) {
	// Check admin token first
	if apiKey == s.cfg.Admin.Token && s.cfg.Admin.Token != "" {
		return AdminUserID, "admin", true
	}

	if s.adminRepo == nil {
		return "", "", false
	}

	// Look up API key in database
	id, _, valid, err := s.adminRepo.ValidateAPIKey(context.Background(), apiKey)
	if err != nil {
		slog.Warn("failed to validate API key", "error", err)
		return "", "", false
	}
	if valid {
		return id, "user", true
	}

	return "", "", false
}

// authenticatePassword validates username/password credentials using bcrypt.
// Returns userID, role, display name, and success flag.
func (s *Server) authenticatePassword(username, password string) (userID, role, displayName string, ok bool) {
	// Check admin credentials from config
	if username == "admin" && password == s.cfg.Admin.Token {
		return AdminUserID, "admin", "admin", true
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		return "", "", "", false
	}

	// Look up user by name (uid) in users table
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		id       string
		roleVal  string
		hashStr  string
		nameVal  string
	)
	err := s.adminRepo.DB().QueryRowContext(ctx, `
		SELECT id::text, COALESCE(role, 'user'), password_hash, COALESCE(uid, '')
		FROM users
		WHERE name = $1 AND password_hash IS NOT NULL
		LIMIT 1
	`, username).Scan(&id, &roleVal, &hashStr, &nameVal)
	if err != nil {
		slog.Debug("password auth: user not found or no password set", "username", username, "error", err)
		return "", "", "", false
	}

	// Verify bcrypt hash
	if bcrypt.CompareHashAndPassword([]byte(hashStr), []byte(password)) != nil {
		slog.Warn("password auth: invalid password", "username", username)
		return "", "", "", false
	}

	return id, roleVal, nameVal, true
}

// handleGuestLogin creates a new guest user and returns a JWT with role='guest'.
//
// POST /api/v2/auth/guest
func (s *Server) handleGuestLogin(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Generate unique guest UID
	guestUID := fmt.Sprintf("guest-%d", time.Now().UnixNano())

	var userID string
	err := s.adminRepo.DB().QueryRowContext(ctx, `
		INSERT INTO users (uid, name, role)
		VALUES ($1, $1, 'guest')
		RETURNING id::text
	`, guestUID).Scan(&userID)
	if err != nil {
		slog.Error("failed to create guest user", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create guest user")
		return
	}

	slog.Info("guest user created", "uid", guestUID, "user_id", userID)
	s.issueToken(w, userID, "guest", guestUID)
}

// handleRegister creates a new user account or upgrades a guest account.
//
// POST /api/v2/auth/register
// Body: {"username": "...", "password": "...", "guest_token": "<optional>"}
//
// If guest_token is provided, the existing guest user record is upgraded
// (keeping the same user_id, preserving traces and feedback).
// Otherwise, a new user record is created.
func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username   string `json:"username"`
		Password   string `json:"password"`
		GuestToken string `json:"guest_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if len(req.Username) < 2 || len(req.Username) > 64 {
		response.Err(w, http.StatusBadRequest, "bad_request", "username must be 2-64 characters")
		return
	}
	if len(req.Password) < 6 {
		response.Err(w, http.StatusBadRequest, "bad_request", "password must be at least 6 characters")
		return
	}

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	// Hash the password with bcrypt
	hashed, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		slog.Error("failed to hash password", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to hash password")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	// Mode 1: Upgrade existing guest account
	if req.GuestToken != "" {
		payload, err := s.ValidateJWT(req.GuestToken)
		if err != nil {
			response.Err(w, http.StatusUnauthorized, "invalid_token", "invalid guest token")
			return
		}

		// Verify the user is currently a guest
		var currentRole string
		err = s.adminRepo.DB().QueryRowContext(ctx, `
			SELECT COALESCE(role, 'user') FROM users WHERE id = $1
		`, payload.Sub).Scan(&currentRole)
		if err != nil {
			response.Err(w, http.StatusNotFound, "not_found", "guest user not found")
			return
		}
		if currentRole != "guest" {
			response.Err(w, http.StatusBadRequest, "not_guest", "user is not a guest account")
			return
		}

		// Check username uniqueness
		var exists bool
		s.adminRepo.DB().QueryRowContext(ctx, `
			SELECT EXISTS(SELECT 1 FROM users WHERE name = $1 AND id != $2)
		`, req.Username, payload.Sub).Scan(&exists)
		if exists {
			response.Err(w, http.StatusConflict, "username_taken", "username already exists")
			return
		}

		// Upgrade: UPDATE the existing guest row
		_, err = s.adminRepo.DB().ExecContext(ctx, `
			UPDATE users
			SET uid = $1, name = $1, password_hash = $2, role = 'user', updated_at = NOW()
			WHERE id = $3
		`, req.Username, string(hashed), payload.Sub)
		if err != nil {
			slog.Error("failed to upgrade guest user", "error", err)
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to upgrade account")
			return
		}

		slog.Info("guest upgraded to registered user", "username", req.Username, "user_id", payload.Sub)
		s.issueToken(w, payload.Sub, "user", req.Username)
		return
	}

	// Mode 2: Create new user
	var userID string
	err = s.adminRepo.DB().QueryRowContext(ctx, `
		INSERT INTO users (uid, name, password_hash, role)
		VALUES ($1, $2, $3, 'user')
		ON CONFLICT (uid) DO NOTHING
		RETURNING id::text
	`, req.Username, req.Username, string(hashed)).Scan(&userID)
	if err != nil {
		response.Err(w, http.StatusConflict, "username_taken", "username already exists")
		return
	}

	slog.Info("user registered", "username", req.Username, "user_id", userID)
	s.issueToken(w, userID, "user", req.Username)
}


