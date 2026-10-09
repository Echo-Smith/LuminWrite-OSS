package server

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services/packages"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Package Install Handlers (docs/34) ──────────────────
//
// Download-install surface for the style market: catalog listing, install
// (builtin or SSRF-guarded url), installed listing, and uninstall. All
// endpoints are per-user (jwtAuth + rejectGuest); ownership is enforced in
// SQL, and installs never carry credentials — service packages register an
// MCP server whose keys the user supplies in the MCP UI afterwards.

// handleListPackageCatalog returns the builtin catalog.
//
// GET /api/v2/packages/catalog
func (s *Server) handleListPackageCatalog(w http.ResponseWriter, r *http.Request) {
	catalog, err := packages.BuiltinCatalog()
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to load catalog")
		return
	}
	response.OK(w, map[string]interface{}{"packages": catalog.Packages})
}

// handleListInstalledPackages lists the caller's installed packages.
//
// GET /api/v2/packages?kind=style|skill|service
func (s *Server) handleListInstalledPackages(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.packageRepo == nil {
		response.OK(w, map[string]interface{}{"packages": []interface{}{}, "total": 0})
		return
	}
	installed, err := s.packageRepo.ListForUser(r.Context(), user.Sub, r.URL.Query().Get("kind"))
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list packages")
		return
	}
	response.OK(w, map[string]interface{}{"packages": installed, "total": len(installed)})
}

// handleInstallPackage installs a package (builtin or url).
//
// POST /api/v2/packages/install
// Body: {"source": "builtin", "slug": "..."} or {"source": "url", "url": "https://..."}
func (s *Server) handleInstallPackage(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.packageInstaller == nil {
		response.Err(w, http.StatusServiceUnavailable, "packages_unavailable", "package installation is not configured on this deployment")
		return
	}

	var req struct {
		Source string `json:"source"`
		Slug   string `json:"slug"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	result, err := s.packageInstaller.Install(r.Context(), user.Sub, packages.InstallRequest{
		Source: req.Source,
		Slug:   req.Slug,
		URL:    req.URL,
	})
	if err != nil {
		code, status := "install_failed", http.StatusBadRequest
		switch {
		case errors.Is(err, packages.ErrAlreadyInstalled):
			code, status = "already_installed", http.StatusConflict
		case isPackageUpstreamError(err):
			code, status = "upstream_error", http.StatusBadGateway
		}
		slog.Warn("package install failed", "user_id", user.Sub, "source", req.Source, "slug", req.Slug, "error", err)
		response.Err(w, status, code, err.Error())
		return
	}
	response.Created(w, result)
}

// handleUninstallPackage removes a package and everything it registered.
//
// DELETE /api/v2/packages/{id}
func (s *Server) handleUninstallPackage(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}
	if s.packageInstaller == nil {
		response.Err(w, http.StatusServiceUnavailable, "packages_unavailable", "package installation is not configured on this deployment")
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "package id is required")
		return
	}
	if err := s.packageInstaller.Uninstall(r.Context(), user.Sub, id); err != nil {
		if errors.Is(err, database.ErrInstalledPackageNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "package not found")
			return
		}
		slog.Warn("package uninstall failed", "user_id", user.Sub, "id", id, "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	response.OK(w, map[string]interface{}{"ok": true})
}

// isPackageUpstreamError reports whether the failure came from fetching the
// remote package (SSRF rejection, download failure, host error).
func isPackageUpstreamError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{"package url", "download package", "resolve package host", "package host"} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}
