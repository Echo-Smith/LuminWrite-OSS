package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── RBAC: Permission-based access control ──────────────

// userPermissions fetches the effective permission keys for a user from the database.
// It unions all permissions from all roles assigned to the user, plus the legacy
// role column: if role == "admin", all permissions are granted.
func (s *Server) userPermissions(ctx context.Context, userID, legacyRole string) ([]string, error) {
	// Admin role from users.role gets all permissions (backward compat)
	if legacyRole == "admin" {
		return []string{"*"}, nil
	}

	if s.db == nil {
		return nil, nil
	}

	// Fetch permissions from all roles assigned to the user
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT p.key
		FROM user_roles ur
		JOIN role_permissions rp ON rp.role_id = ur.role_id
		JOIN permissions p ON p.id = rp.permission_id
		WHERE ur.user_id = $1::uuid
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var perms []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		perms = append(perms, key)
	}
	return perms, rows.Err()
}

// hasPermission checks if the user has the required permission.
// Supports wildcard: "*" matches all permissions (admin).
func hasPermission(perms []string, required string) bool {
	for _, p := range perms {
		if p == "*" || p == required {
			return true
		}
	}
	return false
}

// requirePermission is an HTTP middleware that checks if the authenticated user
// has the specified permission. Must be used after jwtAuthMiddleware.
func (s *Server) requirePermission(perm string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := userFromContext(r.Context())
			if user == nil {
				response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}

			// Fetch permissions with a short timeout
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
			defer cancel()

			perms, err := s.userPermissions(ctx, user.Sub, user.Role)
			if err != nil {
				response.Err(w, http.StatusInternalServerError, "internal_error", "failed to check permissions")
				return
			}

			if !hasPermission(perms, perm) {
				response.Err(w, http.StatusForbidden, "forbidden", "insufficient permissions: "+perm)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ─── RBAC: Admin API handlers ───────────────────────────

// handleAdminListRoles returns all roles with their permission counts.
func (s *Server) handleAdminListRoles(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT r.id::text, r.name, r.description, r.is_system, r.created_at::text, r.updated_at::text,
		       (SELECT COUNT(*) FROM role_permissions rp WHERE rp.role_id = r.id) AS perm_count,
		       (SELECT COUNT(*) FROM user_roles ur WHERE ur.role_id = r.id) AS user_count
		FROM roles r
		ORDER BY r.name
	`)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	type RoleWithCounts struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		IsSystem    bool   `json:"is_system"`
		PermCount   int    `json:"perm_count"`
		UserCount   int    `json:"user_count"`
		CreatedAt   string `json:"created_at"`
		UpdatedAt   string `json:"updated_at"`
	}

	var roles []RoleWithCounts
	for rows.Next() {
		var rwc RoleWithCounts
		if err := rows.Scan(&rwc.ID, &rwc.Name, &rwc.Description, &rwc.IsSystem,
			&rwc.CreatedAt, &rwc.UpdatedAt, &rwc.PermCount, &rwc.UserCount); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		roles = append(roles, rwc)
	}

	response.OK(w, map[string]interface{}{"roles": roles})
}

// handleAdminListPermissions returns all registered permissions.
func (s *Server) handleAdminListPermissions(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT id::text, key, description, created_at::text
		FROM permissions
		ORDER BY key
	`)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	var perms []map[string]interface{}
	for rows.Next() {
		var id, key, desc, createdAt string
		if err := rows.Scan(&id, &key, &desc, &createdAt); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		perms = append(perms, map[string]interface{}{
			"id": id, "key": key, "description": desc, "created_at": createdAt,
		})
	}

	response.OK(w, map[string]interface{}{"permissions": perms})
}

// handleAdminGetRolePermissions returns all permissions assigned to a role.
func (s *Server) handleAdminGetRolePermissions(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	roleID := chi.URLParam(r, "id")

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id::text, p.key, p.description
		FROM role_permissions rp
		JOIN permissions p ON p.id = rp.permission_id
		WHERE rp.role_id = $1::uuid
		ORDER BY p.key
	`, roleID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	var perms []map[string]interface{}
	for rows.Next() {
		var id, key, desc string
		if err := rows.Scan(&id, &key, &desc); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		perms = append(perms, map[string]interface{}{
			"id": id, "key": key, "description": desc,
		})
	}

	response.OK(w, map[string]interface{}{"permissions": perms})
}

// handleAdminUpdateRolePermissions replaces the permission set for a role.
func (s *Server) handleAdminUpdateRolePermissions(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	roleID := chi.URLParam(r, "id")

	var req struct {
		PermissionKeys []string `json:"permission_keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer tx.Rollback()

	// Clear existing permissions
	if _, err := tx.ExecContext(r.Context(), `
		DELETE FROM role_permissions WHERE role_id = $1::uuid
	`, roleID); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	// Insert new permissions
	for _, key := range req.PermissionKeys {
		if _, err := tx.ExecContext(r.Context(), `
			INSERT INTO role_permissions (role_id, permission_id)
			SELECT $1::uuid, id FROM permissions WHERE key = $2
			ON CONFLICT DO NOTHING
		`, roleID, key); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	response.OK(w, map[string]string{"status": "ok"})
}

// handleAdminListUserRoles returns all roles assigned to a user.
func (s *Server) handleAdminListUserRoles(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	userID := chi.URLParam(r, "userId")

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT r.id::text, r.name, r.description, r.is_system, ur.assigned_at::text
		FROM user_roles ur
		JOIN roles r ON r.id = ur.role_id
		WHERE ur.user_id = $1::uuid
		ORDER BY r.name
	`, userID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	type RoleAssignment struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		Description string `json:"description"`
		IsSystem    bool   `json:"is_system"`
		AssignedAt  string `json:"assigned_at"`
	}

	var roles []RoleAssignment
	for rows.Next() {
		var ra RoleAssignment
		if err := rows.Scan(&ra.ID, &ra.Name, &ra.Description, &ra.IsSystem, &ra.AssignedAt); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		roles = append(roles, ra)
	}

	// Also fetch the effective permissions
	perms, _ := s.userPermissions(r.Context(), userID, "")

	response.OK(w, map[string]interface{}{
		"roles":       roles,
		"permissions": perms,
	})
}

// handleAdminAssignUserRole assigns a role to a user.
func (s *Server) handleAdminAssignUserRole(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	userID := chi.URLParam(r, "userId")

	var req struct {
		RoleID     string `json:"role_id"`
		AssignedBy string `json:"assigned_by"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	adminUser := userFromContext(r.Context())
	assignedBy := req.AssignedBy
	if assignedBy == "" && adminUser != nil {
		assignedBy = adminUser.Sub
	}

	_, err := s.db.ExecContext(r.Context(), `
		INSERT INTO user_roles (user_id, role_id, assigned_by)
		VALUES ($1::uuid, $2::uuid, $3)
		ON CONFLICT (user_id, role_id) DO UPDATE SET assigned_by = EXCLUDED.assigned_by, assigned_at = NOW()
	`, userID, req.RoleID, assignedBy)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	slog.Info("role assigned", "user_id", userID, "role_id", req.RoleID, "by", assignedBy)

	response.OK(w, map[string]string{"status": "ok"})
}

// handleAdminRemoveUserRole removes a role from a user.
func (s *Server) handleAdminRemoveUserRole(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	userID := chi.URLParam(r, "userId")
	roleID := chi.URLParam(r, "roleId")

	// Prevent removing system admin role from the config admin user
	var roleName string
	if err := s.db.QueryRowContext(r.Context(), `SELECT name FROM roles WHERE id = $1::uuid`, roleID).Scan(&roleName); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "role not found")
		return
	}
	if roleName == "admin" && userID == AdminUserID {
		response.Err(w, http.StatusForbidden, "forbidden", "cannot remove admin role from system admin user")
		return
	}

	_, err := s.db.ExecContext(r.Context(), `
		DELETE FROM user_roles WHERE user_id = $1::uuid AND role_id = $2::uuid
	`, userID, roleID)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	response.OK(w, map[string]string{"status": "ok"})
}

// handleAdminListUsersWithRoles returns all users with their role assignments.
func (s *Server) handleAdminListUsersWithRoles(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	rows, err := s.db.QueryContext(r.Context(), `
		SELECT u.id::text, COALESCE(u.uid, ''), COALESCE(u.name, ''), COALESCE(u.role, 'user'), u.created_at::text,
		       COALESCE(
		         (SELECT json_agg(json_build_object('id', r.id::text, 'name', r.name))
		          FROM user_roles ur JOIN roles r ON r.id = ur.role_id
		          WHERE ur.user_id = u.id),
		         '[]'::json
		       ) AS roles
		FROM users u
		ORDER BY u.created_at DESC
		LIMIT 100
	`)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	defer rows.Close()

	var users []map[string]interface{}
	for rows.Next() {
		var id, uid, name, role, createdAt string
		var rolesJSON []byte
		if err := rows.Scan(&id, &uid, &name, &role, &createdAt, &rolesJSON); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		users = append(users, map[string]interface{}{
			"id":         id,
			"uid":        uid,
			"name":       name,
			"role":       role,
			"created_at": createdAt,
			"roles":      json.RawMessage(rolesJSON),
		})
	}

	response.OK(w, map[string]interface{}{"users": users})
}
