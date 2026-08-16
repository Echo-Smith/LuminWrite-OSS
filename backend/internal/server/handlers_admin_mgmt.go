package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Admin: Model Configs ────────────────────────────────

func (s *Server) handleAdminListModelConfigs(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"configs": []interface{}{}, "total": 0})
		return
	}

	configs, err := s.adminRepo.ListModelConfigs(r.Context())
	if err != nil {
		slog.Warn("failed to list model configs", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list model configs")
		return
	}

	response.OK(w, map[string]interface{}{"configs": configs, "total": len(configs)})
}

func (s *Server) handleAdminCreateModelConfig(w http.ResponseWriter, r *http.Request) {
	var req database.ModelConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Provider == "" || req.ModelName == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "provider and model_name are required")
		return
	}

	if req.DisplayName == "" {
		req.DisplayName = req.ModelName
	}
	if req.MaxTokens == 0 {
		req.MaxTokens = 8192
	}
	if req.Temperature == 0 {
		req.Temperature = 0.7
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	created, err := s.adminRepo.CreateModelConfig(r.Context(), &req)
	if err != nil {
		slog.Warn("failed to create model config", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create model config")
		return
	}

	if s.llmSvc != nil {
		s.llmSvc.InvalidateCache()
	}

	response.Created(w, created)
}

func (s *Server) handleAdminUpdateModelConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req database.ModelConfig
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	updated, err := s.adminRepo.UpdateModelConfig(r.Context(), id, &req)
	if err != nil {
		slog.Warn("failed to update model config", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update model config")
		return
	}

	if s.llmSvc != nil {
		s.llmSvc.InvalidateCache()
	}

	response.OK(w, updated)
}

func (s *Server) handleAdminDeleteModelConfig(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.adminRepo.DeleteModelConfig(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete model config")
		return
	}

	if s.llmSvc != nil {
		s.llmSvc.InvalidateCache()
	}

	response.OK(w, map[string]interface{}{"message": "model config deleted"})
}

// handleAdminDiscoverModels fetches available models from a provider's API.
// Request: { "base_url": "https://api.deepseek.com/v1", "api_key": "sk-..." }
// Response: { "models": [{"id": "deepseek-chat", "owned_by": "deepseek"}, ...] }
func (s *Server) handleAdminDiscoverModels(w http.ResponseWriter, r *http.Request) {
	var req struct {
		BaseURL string `json:"base_url"`
		APIKey  string `json:"api_key"`
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
		req.BaseURL = "https://api.deepseek.com/v1"
	}

	// OpenAI-compatible: GET {base_url}/models
	url := strings.TrimSuffix(req.BaseURL, "/") + "/models"
	httpReq, err := http.NewRequestWithContext(r.Context(), "GET", url, nil)
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create request")
		return
	}
	httpReq.Header.Set("Authorization", "Bearer "+req.APIKey)
	httpReq.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		slog.Warn("discover models: request failed", "base_url", req.BaseURL, "error", err)
		response.Err(w, http.StatusBadGateway, "upstream_error", fmt.Sprintf("failed to connect to %s: %v", req.BaseURL, err))
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		slog.Warn("discover models: upstream returned error", "status", resp.StatusCode, "body", string(body))
		response.Err(w, http.StatusBadGateway, "upstream_error",
			fmt.Sprintf("provider returned status %d: %s", resp.StatusCode, string(body)))
		return
	}

	// Parse OpenAI-compatible response: { "data": [{"id": "...", "owned_by": "..."}, ...] }
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		response.Err(w, http.StatusInternalServerError, "parse_error", "failed to parse response from provider")
		return
	}

	type discoveredModel struct {
		ID      string `json:"id"`
		OwnedBy string `json:"owned_by"`
	}

	models := make([]discoveredModel, 0, len(result.Data))
	for _, m := range result.Data {
		if m.ID != "" {
			models = append(models, discoveredModel{ID: m.ID, OwnedBy: m.OwnedBy})
		}
	}

	response.OK(w, map[string]interface{}{
		"models":   models,
		"base_url": req.BaseURL,
		"total":    len(models),
	})
}

// ─── Admin: API Keys ─────────────────────────────────────

func (s *Server) handleAdminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"keys": []interface{}{}, "total": 0})
		return
	}

	// Default to 'mcp' category for the API keys page (LLM keys are now managed in model configs)
	category := r.URL.Query().Get("category")
	if category == "" {
		category = "mcp"
	}

	keys, err := s.adminRepo.ListAPIKeys(r.Context(), category)
	if err != nil {
		slog.Warn("failed to list api keys", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list api keys")
		return
	}

	response.OK(w, map[string]interface{}{"keys": keys, "total": len(keys)})
}

func (s *Server) handleAdminCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	var req database.APIKey
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Name == "" || req.Provider == "" || req.KeyValue == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "name, provider, and key_value are required")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	created, err := s.adminRepo.CreateAPIKey(r.Context(), &req)
	if err != nil {
		slog.Warn("failed to create api key", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create api key")
		return
	}

	// Hot-reload embedding client if dashscope key was changed
	s.reloadEmbeddingFromDB(r.Context(), req.Provider)

	response.Created(w, created)
}

func (s *Server) handleAdminUpdateAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req database.APIKey
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	updated, err := s.adminRepo.UpdateAPIKey(r.Context(), id, &req)
	if err != nil {
		slog.Warn("failed to update api key", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update api key")
		return
	}

	// Hot-reload embedding client if dashscope key was changed
	s.reloadEmbeddingFromDB(r.Context(), req.Provider)

	response.OK(w, updated)
}

func (s *Server) handleAdminDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	// Get provider before deletion for hot-reload check
	keys, err := s.adminRepo.ListAPIKeys(r.Context(), "")
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list api keys")
		return
	}
	var deletedProvider string
	for _, k := range keys {
		if k.ID == id {
			deletedProvider = k.Provider
			break
		}
	}

	if err := s.adminRepo.DeleteAPIKey(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete api key")
		return
	}

	// If the deleted key was for dashscope, log a warning
	if deletedProvider == "dashscope" {
		slog.Warn("dashscope API key deleted — embedding client will use env var until server restart")
	}

	response.OK(w, map[string]interface{}{"message": "api key deleted"})
}

func (s *Server) handleAdminTestAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	// Get the key details
	keys, err := s.adminRepo.ListAPIKeys(r.Context(), "")
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get api key")
		return
	}

	var target *database.APIKey
	for _, k := range keys {
		if k.ID == id {
			target = k
			break
		}
	}
	if target == nil {
		response.Err(w, http.StatusNotFound, "not_found", "api key not found")
		return
	}

	// Get actual key value
	keyValue, baseURL, err := s.adminRepo.GetAPIKeyValue(r.Context(), target.Provider)
	if err != nil {
		s.adminRepo.UpdateAPIKeyStatus(r.Context(), id, "fail", "failed to retrieve key value")
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get key value")
		return
	}

	// Test connectivity based on provider
	status := "ok"
	errMsg := ""
	switch target.Provider {
	case "deepseek", "openai", "qwen":
		err := s.testLLMConnectivity(r.Context(), baseURL, keyValue)
		if err != nil {
			status = "fail"
			errMsg = err.Error()
		}
	case "tavily":
		err := s.testTavilyConnectivity(r.Context(), keyValue)
		if err != nil {
			status = "fail"
			errMsg = err.Error()
		}
	case "anysearch":
		err := s.testAnySearchConnectivity(r.Context(), baseURL, keyValue)
		if err != nil {
			status = "fail"
			errMsg = err.Error()
		}
	case "dashscope":
		err := s.testEmbeddingConnectivity(r.Context(), baseURL, keyValue)
		if err != nil {
			status = "fail"
			errMsg = err.Error()
		}
	default:
		// Generic check — just verify key is non-empty
		if keyValue == "" {
			status = "fail"
			errMsg = "empty key value"
		}
	}

	s.adminRepo.UpdateAPIKeyStatus(r.Context(), id, status, errMsg)

	response.OK(w, map[string]interface{}{
		"id":          id,
		"provider":    target.Provider,
		"status":      status,
		"error":       errMsg,
		"tested_at":   true,
	})
}

func (s *Server) testLLMConnectivity(ctx context.Context, baseURL, apiKey string) error {
	if baseURL == "" {
		baseURL = "https://api.deepseek.com/v1"
	}
	// Simple models list request
	url := strings.TrimSuffix(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

func (s *Server) testTavilyConnectivity(ctx context.Context, apiKey string) error {
	// Tavily doesn't have a simple health endpoint, so we do a minimal search
	body := `{"query":"test","max_results":1}`
	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.tavily.com/search", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Tavily API returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) testAnySearchConnectivity(ctx context.Context, baseURL, apiKey string) error {
	if baseURL == "" {
		baseURL = "https://api.anysearch.com/v1/search"
	}
	body := `{"query":"test","max_results":1}`
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("AnySearch API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// testEmbeddingConnectivity tests DashScope/OpenAI-compatible embedding API connectivity.
// Sends a minimal embedding request and checks for a valid response.
func (s *Server) testEmbeddingConnectivity(ctx context.Context, baseURL, apiKey string) error {
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	baseURL = strings.TrimRight(baseURL, "/")

	// Send a minimal embedding request
	body := `{"model":"text-embedding-v3","input":["test"]}`
	req, err := http.NewRequestWithContext(ctx, "POST", baseURL+"/embeddings", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("Embedding API returned status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// reloadEmbeddingFromDB checks if the provider is dashscope and hot-reloads
// the embedding client with the updated API key from the database.
// This is called after Create/Update/Delete API key operations.
func (s *Server) reloadEmbeddingFromDB(ctx context.Context, provider string) {
	if provider != "dashscope" || s.embedding == nil || s.adminRepo == nil {
		return
	}

	key, baseURL, err := s.adminRepo.GetAPIKeyValue(ctx, "dashscope")
	if err != nil || key == "" {
		slog.Debug("reloadEmbeddingFromDB: no dashscope key in DB")
		return
	}

	model := s.cfg.Dashscope.Model
	dimension := s.cfg.Dashscope.Dimension
	if baseURL == "" {
		baseURL = s.cfg.Dashscope.BaseURL
	}

	s.embedding.Reconfigure(key, baseURL, model, dimension)
	slog.Info("embedding client hot-reloaded from DB",
		"model", model,
		"dimension", dimension,
		"base_url", baseURL,
	)
}

// ─── Admin: Token Usage Stats ────────────────────────────

func (s *Server) handleAdminTokenUsage(w http.ResponseWriter, r *http.Request) {
	days := parseIntDefault(r.URL.Query().Get("days"), 30)

	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{
			"total_tokens":          0,
			"total_prompt_tokens":   0,
			"total_completion_tokens": 0,
			"total_estimated_cost":  0,
			"today_tokens":          0,
			"by_model":              []interface{}{},
			"by_provider":           []interface{}{},
			"daily_tokens":          []interface{}{},
		})
		return
	}

	stats, err := s.adminRepo.GetTokenUsageStats(r.Context(), days)
	if err != nil {
		slog.Warn("failed to get token usage stats", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get token usage stats")
		return
	}

	response.OK(w, stats)
}

// handleAdminABMetrics returns A/B test metrics comparing Chat Completions vs Responses API.
func (s *Server) handleAdminABMetrics(w http.ResponseWriter, r *http.Request) {
	if s.llm == nil {
		response.OK(w, map[string]interface{}{
			"enabled": false,
			"message": "LLM client not configured",
		})
		return
	}

	snapshot := s.llm.GetABMetrics()
	if snapshot == nil {
		response.OK(w, map[string]interface{}{
			"enabled": false,
			"message": "A/B testing not enabled. Set DEEPSEEK_RESPONSES_API_RATIO > 0 to enable.",
		})
		return
	}

	// Calculate cache hit rates
	chatCacheRate := 0.0
	if snapshot.ChatCompletions.PromptTokens > 0 {
		chatCacheRate = float64(snapshot.ChatCompletions.CacheHitTokens) / float64(snapshot.ChatCompletions.PromptTokens)
	}
	respCacheRate := 0.0
	if snapshot.ResponsesAPI.PromptTokens > 0 {
		respCacheRate = float64(snapshot.ResponsesAPI.CacheHitTokens) / float64(snapshot.ResponsesAPI.PromptTokens)
	}

	// Calculate average latencies
	chatAvgLatency := 0.0
	if snapshot.ChatCompletions.RequestCount > 0 {
		chatAvgLatency = float64(snapshot.ChatCompletions.TotalLatencyMs) / float64(snapshot.ChatCompletions.RequestCount)
	}
	respAvgLatency := 0.0
	if snapshot.ResponsesAPI.RequestCount > 0 {
		respAvgLatency = float64(snapshot.ResponsesAPI.TotalLatencyMs) / float64(snapshot.ResponsesAPI.RequestCount)
	}

	response.OK(w, map[string]interface{}{
		"enabled": true,
		"chat_completions": map[string]interface{}{
			"request_count":     snapshot.ChatCompletions.RequestCount,
			"prompt_tokens":     snapshot.ChatCompletions.PromptTokens,
			"cache_hit_tokens":  snapshot.ChatCompletions.CacheHitTokens,
			"completion_tokens": snapshot.ChatCompletions.CompletionTokens,
			"cache_hit_rate":    chatCacheRate,
			"avg_latency_ms":    chatAvgLatency,
		},
		"responses_api": map[string]interface{}{
			"request_count":     snapshot.ResponsesAPI.RequestCount,
			"prompt_tokens":     snapshot.ResponsesAPI.PromptTokens,
			"cache_hit_tokens":  snapshot.ResponsesAPI.CacheHitTokens,
			"completion_tokens": snapshot.ResponsesAPI.CompletionTokens,
			"cache_hit_rate":    respCacheRate,
			"avg_latency_ms":    respAvgLatency,
		},
	})
}

// ─── Admin: Cron Jobs ────────────────────────────────────

func (s *Server) handleAdminListCronJobs(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"jobs": []interface{}{}, "total": 0})
		return
	}

	jobs, err := s.adminRepo.ListCronJobs(r.Context())
	if err != nil {
		slog.Warn("failed to list cron jobs", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list cron jobs")
		return
	}

	response.OK(w, map[string]interface{}{"jobs": jobs, "total": len(jobs)})
}

func (s *Server) handleAdminCreateCronJob(w http.ResponseWriter, r *http.Request) {
	var req database.CronJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Name == "" || req.Schedule == "" || req.TaskType == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "name, schedule, and task_type are required")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	created, err := s.adminRepo.CreateCronJob(r.Context(), &req)
	if err != nil {
		slog.Warn("failed to create cron job", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to create cron job")
		return
	}

	response.Created(w, created)
}

func (s *Server) handleAdminUpdateCronJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req database.CronJob
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	updated, err := s.adminRepo.UpdateCronJob(r.Context(), id, &req)
	if err != nil {
		slog.Warn("failed to update cron job", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to update cron job")
		return
	}

	response.OK(w, updated)
}

func (s *Server) handleAdminDeleteCronJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.adminRepo.DeleteCronJob(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete cron job")
		return
	}

	response.OK(w, map[string]interface{}{"message": "cron job deleted"})
}

func (s *Server) handleAdminRunCronJob(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	jobs, err := s.adminRepo.ListCronJobs(r.Context())
	if err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to get cron jobs")
		return
	}

	var target *database.CronJob
	for _, j := range jobs {
		if j.ID == id {
			target = j
			break
		}
	}
	if target == nil {
		response.Err(w, http.StatusNotFound, "not_found", "cron job not found")
		return
	}

	// Execute the job asynchronously
	go func() {
		s.adminRepo.UpdateCronJobStatus(context.Background(), id, "running", "")
		err := s.executeCronJob(target)
		if err != nil {
			slog.Warn("cron job execution failed", "job", target.Name, "error", err)
			s.adminRepo.UpdateCronJobStatus(context.Background(), id, "failed", err.Error())
		} else {
			s.adminRepo.UpdateCronJobStatus(context.Background(), id, "success", "")
		}
	}()

	response.OK(w, map[string]interface{}{
		"id":      id,
		"name":    target.Name,
		"message": "cron job execution started",
	})
}

// executeCronJob runs the actual task based on task_type.
func (s *Server) executeCronJob(job *database.CronJob) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	switch job.TaskType {
	case "topic_fetch":
		return s.cronTopicFetch(ctx, job)
	case "topic_trend_record":
		return s.cronRecordTopicTrends(ctx)
	case "feedback_aggregate":
		return s.cronFeedbackAggregate(ctx, job)
	case "cleanup":
		return s.cronCleanup(ctx, job)
	case "weknora_sync":
		return s.cronWeKnoraSync(ctx, job)
	case "kb_auto_import":
		return s.cronKbAutoImport(ctx, job)
	default:
		slog.Info("cron: unknown task type", "job", job.Name, "type", job.TaskType)
		return nil
	}
}

// cronTopicFetch fetches hot topics from configured sources and saves them to DB.
func (s *Server) cronTopicFetch(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: topic_fetch triggered", "job", job.Name)

	if s.search == nil {
		return fmt.Errorf("search client not configured")
	}

	limit := 20
	if job.TaskConfig != nil {
		if v, ok := job.TaskConfig["limit"].(float64); ok && v > 0 {
			limit = int(v)
		}
	}

	topics := s.search.FetchHotTopics(ctx, limit)
	slog.Info("cron: topics fetched", "count", len(topics), "job", job.Name)

	if s.traces == nil {
		slog.Warn("cron: topic_fetch — traces repo not available, skipping save")
		return nil
	}

	saved := 0
	for _, topic := range topics {
		title, _ := topic["title"].(string)
		description, _ := topic["description"].(string)
		if title == "" {
			continue
		}

		_, err := s.traces.CreateTopic(ctx, title, description, "")
		if err != nil {
			slog.Debug("cron: topic insert skipped (likely duplicate)", "title", title, "error", err)
			continue
		}
		saved++
	}

	slog.Info("cron: topic_fetch completed", "fetched", len(topics), "saved", saved, "job", job.Name)
	return nil
}

// cronFeedbackAggregate aggregates feedback for all published styles.
func (s *Server) cronFeedbackAggregate(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: feedback_aggregate triggered", "job", job.Name)

	if s.feedback == nil {
		return fmt.Errorf("feedback repo not available")
	}

	// Get all published style profiles
	styles := s.profiles.List()
	if len(styles) == 0 {
		slog.Warn("cron: no styles found for feedback aggregation")
		return nil
	}

	for _, style := range styles {
		_, err := s.feedback.AggregateFeedback(ctx, style.Slug, style.Version)
		if err != nil {
			slog.Warn("cron: feedback aggregation failed for style",
				"style", style.Slug, "version", style.Version, "error", err)
			continue
		}
		slog.Info("cron: feedback aggregated for style",
			"style", style.Slug, "version", style.Version)
	}

	slog.Info("cron: feedback_aggregate completed", "styles", len(styles), "job", job.Name)
	return nil
}

// cronCleanup removes old token_usage records and expired sessions.
func (s *Server) cronCleanup(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: cleanup triggered", "job", job.Name)

	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		return fmt.Errorf("database not available")
	}

	// Delete token_usage records older than 90 days
	result, err := s.adminRepo.DB().ExecContext(ctx, `
		DELETE FROM token_usage WHERE created_at < NOW() - INTERVAL '90 days'
	`)
	if err != nil {
		slog.Warn("cron: cleanup — token_usage cleanup failed", "error", err)
	} else {
		if rows, _ := result.RowsAffected(); rows > 0 {
			slog.Info("cron: cleanup — old token_usage records deleted", "count", rows)
		}
	}

	// Delete agent_traces older than 180 days (keep recent history)
	result, err = s.adminRepo.DB().ExecContext(ctx, `
		DELETE FROM agent_traces WHERE created_at < NOW() - INTERVAL '180 days' AND status = 'completed'
	`)
	if err != nil {
		slog.Warn("cron: cleanup — old traces cleanup failed", "error", err)
	} else {
		if rows, _ := result.RowsAffected(); rows > 0 {
			slog.Info("cron: cleanup — old traces deleted", "count", rows)
		}
	}

	// Delete topics older than 30 days (keep recent hot topics)
	result, err = s.adminRepo.DB().ExecContext(ctx, `
		DELETE FROM topics WHERE created_at < NOW() - INTERVAL '30 days' AND source != 'user'
	`)
	if err != nil {
		slog.Warn("cron: cleanup — old topics cleanup failed", "error", err)
	} else {
		if rows, _ := result.RowsAffected(); rows > 0 {
			slog.Info("cron: cleanup — old topics deleted", "count", rows)
		}
	}

	slog.Info("cron: cleanup completed", "job", job.Name)
	return nil
}
