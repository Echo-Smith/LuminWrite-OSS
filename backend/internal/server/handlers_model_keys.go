package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── User Model Keys (BYOK) Handlers ─────────────────────
//
// BYOK lets a registered user bring their own model endpoint and API key.
// Ownership is enforced in SQL (user_id = $1) per the repo convention; no
// RBAC permission is attached. Keys are stored encrypted; plaintext storage
// is refused (EncryptionEnabled red line), and responses only ever carry a
// masked key preview, never the plaintext or ciphertext.

// modelKeyAccess resolves the authenticated non-guest caller. Rejecting
// guests happens at the router (rejectGuestMiddleware); this is the
// defense-in-depth second half.
func (s *Server) modelKeyAccess(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := userFromContext(r.Context())
	if user == nil || user.Sub == "" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return "", false
	}
	return user.Sub, true
}

// userModelKeyGate returns the BYOK repo, or writes a 503 explaining why
// BYOK is unavailable (no database, or no encryption key configured —
// plaintext key storage is never accepted).
func (s *Server) userModelKeyGate(w http.ResponseWriter) *database.UserModelKeyRepo {
	if s.userKeyRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "byok_unavailable",
			"BYOK storage unavailable: API_KEY_ENCRYPTION_KEY must be configured on this deployment")
		return nil
	}
	return s.userKeyRepo
}

// sanitizeUserModelKey strips key material and adds a masked preview for UI.
func sanitizeUserModelKey(k *database.UserModelKey, repo *database.UserModelKeyRepo) map[string]interface{} {
	masked := ""
	if k.HasAPIKey && repo != nil {
		masked = maskAPIKey(repo.DecryptAPIKey(k.APIKeyEncrypted))
	}
	return map[string]interface{}{
		"id":               k.ID,
		"name":             k.Name,
		"provider":         k.Provider,
		"model_name":       k.ModelName,
		"base_url":         k.BaseURL,
		"has_api_key":      k.HasAPIKey,
		"api_key_masked":   masked,
		"max_tokens":       k.MaxTokens,
		"temperature":      k.Temperature,
		"reasoning_effort": k.ReasoningEffort,
		"purpose":          k.Purpose,
		"is_default":       k.IsDefault,
		"is_active":        k.IsActive,
		"custom_headers":   k.CustomHeaders,
		"created_at":       k.CreatedAt,
		"updated_at":       k.UpdatedAt,
	}
}

// handleListUserModelKeys lists the caller's own model keys.
// GET /api/v2/model-keys
func (s *Server) handleListUserModelKeys(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	keys, err := repo.ListForUser(r.Context(), userID)
	if err != nil {
		slog.Warn("list user model keys failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list model keys")
		return
	}
	items := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		items = append(items, sanitizeUserModelKey(k, repo))
	}
	response.OK(w, map[string]interface{}{"model_keys": items, "total": len(items)})
}

// handleCreateUserModelKey creates a BYOK entry for the caller.
// POST /api/v2/model-keys
func (s *Server) handleCreateUserModelKey(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}

	var req database.UserModelKey
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Provider == "" || req.ModelName == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "provider and model_name are required")
		return
	}
	if req.APIKeyPlain == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "api_key is required")
		return
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}
	if req.Temperature == 0 {
		req.Temperature = 0.7
	}
	req.UserID = userID

	created, err := repo.Create(r.Context(), &req)
	if err != nil {
		if strings.Contains(err.Error(), "API_KEY_ENCRYPTION_KEY") {
			response.Err(w, http.StatusServiceUnavailable, "byok_unavailable",
				"BYOK storage unavailable: API_KEY_ENCRYPTION_KEY must be configured on this deployment")
			return
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Err(w, http.StatusConflict, "model_key_exists", "a key for this model already exists")
			return
		}
		slog.Warn("create user model key failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create model key")
		return
	}
	if s.llmSvc != nil {
		s.llmSvc.InvalidateUserCache(userID)
	}
	slog.Info("user model key created", "user_id", userID, "provider", created.Provider, "model", created.ModelName)
	response.Created(w, sanitizeUserModelKey(created, repo))
}

// handleUpdateUserModelKey updates one owned key. Empty api_key preserves
// the stored key.
// PUT /api/v2/model-keys/{keyID}
func (s *Server) handleUpdateUserModelKey(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "keyID")

	var req database.UserModelKey
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Provider == "" || req.ModelName == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "provider and model_name are required")
		return
	}

	updated, err := repo.Update(r.Context(), userID, keyID, &req)
	if err != nil {
		if errors.Is(err, database.ErrUserModelKeyNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "model key not found")
			return
		}
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			response.Err(w, http.StatusConflict, "model_key_exists", "a key for this model already exists")
			return
		}
		slog.Warn("update user model key failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update model key")
		return
	}
	if s.llmSvc != nil {
		s.llmSvc.InvalidateUserCache(userID)
	}
	slog.Info("user model key updated", "user_id", userID, "key_id", keyID)
	response.OK(w, sanitizeUserModelKey(updated, repo))
}

// handleSetDefaultUserModelKey marks one owned key as the user's default.
// PUT /api/v2/model-keys/{keyID}/default
func (s *Server) handleSetDefaultUserModelKey(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "keyID")
	if err := repo.SetDefault(r.Context(), userID, keyID); err != nil {
		if errors.Is(err, database.ErrUserModelKeyNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "model key not found")
			return
		}
		slog.Warn("set default user model key failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to set default model key")
		return
	}
	if s.llmSvc != nil {
		s.llmSvc.InvalidateUserCache(userID)
	}
	response.OK(w, map[string]interface{}{"ok": true, "default_key_id": keyID})
}

// handleDeleteUserModelKey removes one owned key.
// DELETE /api/v2/model-keys/{keyID}
func (s *Server) handleDeleteUserModelKey(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "keyID")
	if err := repo.Delete(r.Context(), userID, keyID); err != nil {
		if errors.Is(err, database.ErrUserModelKeyNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "model key not found")
			return
		}
		slog.Warn("delete user model key failed", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete model key")
		return
	}
	if s.llmSvc != nil {
		s.llmSvc.InvalidateUserCache(userID)
	}
	slog.Info("user model key deleted", "user_id", userID, "key_id", keyID)
	response.OK(w, map[string]interface{}{"ok": true})
}

// handleTestUserModelKey probes connectivity for one stored key by fetching
// the provider's OpenAI-compatible /models list. Error classification
// mirrors the provider preflight vocabulary.
// POST /api/v2/model-keys/{keyID}/test
func (s *Server) handleTestUserModelKey(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	keyID := chi.URLParam(r, "keyID")

	key, err := repo.GetForUser(r.Context(), userID, keyID)
	if err != nil {
		if errors.Is(err, database.ErrUserModelKeyNotFound) {
			response.Err(w, http.StatusNotFound, "not_found", "model key not found")
			return
		}
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to load model key")
		return
	}
	apiKey := repo.DecryptAPIKey(key.APIKeyEncrypted)
	if apiKey == "" {
		response.Err(w, http.StatusBadRequest, "no_api_key", "this entry has no API key configured")
		return
	}

	result := probeProviderModels(r.Context(), key.Provider, key.BaseURL, apiKey, key.CustomHeaders)
	status := http.StatusOK
	if !result["ok"].(bool) {
		status = http.StatusBadGateway
	}
	response.JSON(w, status, result)
}

// handleDiscoverUserModels lists models available under a user-supplied
// baseURL + key, without storing anything. Mirrors the admin discover flow
// so the personal-center form can offer "fetch model list".
// POST /api/v2/model-keys/discover
func (s *Server) handleDiscoverUserModels(w http.ResponseWriter, r *http.Request) {
	repo := s.userModelKeyGate(w)
	if repo == nil {
		return
	}
	userID, ok := s.modelKeyAccess(w, r)
	if !ok {
		return
	}
	_ = userID

	var req struct {
		Provider      string            `json:"provider"`
		BaseURL       string            `json:"base_url"`
		APIKey        string            `json:"api_key"`
		CustomHeaders map[string]string `json:"custom_headers"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.APIKey == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "api_key is required")
		return
	}
	if req.BaseURL == "" {
		req.BaseURL = services.DefaultBaseURLForProvider(req.Provider)
	}
	result := probeProviderModels(r.Context(), req.Provider, req.BaseURL, req.APIKey, req.CustomHeaders)
	result["base_url"] = req.BaseURL
	status := http.StatusOK
	if !result["ok"].(bool) {
		status = http.StatusBadGateway
	}
	response.JSON(w, status, result)
}

// probeProviderModels performs the GET {base_url}/models probe with
// preflight-style classification. Only stable codes and counts leave the
// function — key material and upstream bodies are logged, not returned.
func probeProviderModels(ctx context.Context, provider, baseURL, apiKey string, customHeaders map[string]string) map[string]interface{} {
	if baseURL == "" {
		baseURL = services.DefaultBaseURLForProvider(provider)
	}
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_UNAVAILABLE", "message": "invalid base_url"}
	}
	httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	httpReq.Header.Set("Accept", "application/json")
	for k, v := range customHeaders {
		httpReq.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	started := time.Now()
	resp, err := client.Do(httpReq)
	latencyMS := time.Since(started).Milliseconds()
	if err != nil {
		if ctx.Err() != nil || strings.Contains(err.Error(), "context deadline") || strings.Contains(err.Error(), "Client.Timeout") {
			return map[string]interface{}{"ok": false, "error_code": "PROVIDER_TIMEOUT", "message": "provider did not respond in time", "latency_ms": latencyMS}
		}
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_UNAVAILABLE", "message": "failed to connect to provider", "latency_ms": latencyMS}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_AUTH_REJECTED", "message": "provider rejected the API key", "latency_ms": latencyMS}
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_RATE_LIMITED", "message": "provider rate limit hit", "latency_ms": latencyMS}
	}
	if resp.StatusCode != http.StatusOK {
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_UNAVAILABLE",
			"message": "provider returned an unexpected status", "status": resp.StatusCode, "latency_ms": latencyMS}
	}

	var result struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return map[string]interface{}{"ok": false, "error_code": "PROVIDER_MALFORMED_RESPONSE", "message": "provider response was not parseable", "latency_ms": latencyMS}
	}
	models := make([]string, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, m.ID)
		}
	}
	return map[string]interface{}{
		"ok":         true,
		"models":     models,
		"total":      len(models),
		"latency_ms": latencyMS,
	}
}

// maskAPIKey renders a key preview safe to show its owner: prefix + last 4.
func maskAPIKey(plain string) string {
	plain = strings.TrimSpace(plain)
	if plain == "" {
		return ""
	}
	if len(plain) <= 8 {
		return "***"
	}
	return plain[:3] + "***" + plain[len(plain)-4:]
}
