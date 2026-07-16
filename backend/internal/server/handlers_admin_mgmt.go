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

// ─── Admin: API Keys ─────────────────────────────────────

func (s *Server) handleAdminListAPIKeys(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"keys": []interface{}{}, "total": 0})
		return
	}

	keys, err := s.adminRepo.ListAPIKeys(r.Context())
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

	response.OK(w, updated)
}

func (s *Server) handleAdminDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if s.adminRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "db_unavailable", "database not available")
		return
	}

	if err := s.adminRepo.DeleteAPIKey(r.Context(), id); err != nil {
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to delete api key")
		return
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
	keys, err := s.adminRepo.ListAPIKeys(r.Context())
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
	case "ima_sync":
		return s.cronIMASync(ctx, job)
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

// cronIMASync syncs IMA knowledge base entries into the local pgvector store.
// It fetches all documents from the IMA knowledge base API and upserts them
// into the local knowledge_base table, then generates embeddings for any new entries.
func (s *Server) cronIMASync(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: ima_sync triggered", "job", job.Name)

	if s.search == nil {
		return fmt.Errorf("search client not configured")
	}

	imaClient := s.search.IMAClient()
	if imaClient == nil {
		slog.Warn("cron: ima_sync — IMA client not configured, skipping")
		return nil
	}
	if !imaClient.IsConfigured() {
		slog.Warn("cron: ima_sync — IMA client not fully configured (placeholder keys), skipping")
		return nil
	}

	if s.kbRepo == nil {
		return fmt.Errorf("knowledge base repo not available")
	}

	// Step 1: Fetch all documents from IMA knowledge base
	docs, err := imaClient.FetchDocuments(ctx, 50)
	if err != nil {
		slog.Warn("cron: ima_sync — failed to fetch documents from IMA", "error", err)
		// Don't return error — still try to generate embeddings for existing entries
	} else {
		slog.Info("cron: ima_sync — fetched documents from IMA", "count", len(docs))

		// Step 2: Upsert each document into the local knowledge_base table
		newCount := 0
		skipCount := 0
		for _, doc := range docs {
			// Use AddEntry which handles deduplication via content_hash
			metadata := map[string]interface{}{
				"source":   "ima",
				"doc_id":   doc.DocID,
				"category": doc.Category,
				"url":      doc.URL,
			}
			entry, err := s.kbRepo.AddEntry(ctx, "ima", doc.DocID, doc.Title, doc.Content, metadata)
			if err != nil {
				slog.Debug("cron: ima_sync — failed to upsert doc", "doc_id", doc.DocID, "error", err)
				skipCount++
				continue
			}
			if entry != nil {
				newCount++
			}
		}
		slog.Info("cron: ima_sync — upsert completed",
			"new_or_updated", newCount, "skipped", skipCount, "total_fetched", len(docs))
	}

	// Step 3: Generate embeddings for KB entries that don't have one yet
	count, err := s.kbRepo.GenerateMissingEmbeddings(ctx, 25)
	if err != nil {
		slog.Warn("cron: ima_sync — embedding generation failed", "error", err)
	} else {
		slog.Info("cron: ima_sync — missing embeddings generated", "count", count)
	}

	slog.Info("cron: ima_sync completed", "job", job.Name)
	return nil
}
