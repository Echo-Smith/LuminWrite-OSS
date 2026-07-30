package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── WeKnora Client ─────────────────────────────────────
// WeKnoraClient connects to a WeKnora instance via its REST API.
// It provides hybrid search (BM25 + Dense + GraphRAG), document upload,
// and knowledge management capabilities.
//
// Key endpoints used (verified against WeKnora v0.x API):
//   - POST /api/v1/auth/login                     — JWT login (email + password)
//   - GET  /api/v1/knowledge-bases                 — list knowledge bases
//   - POST /api/v1/knowledge-bases                 — create knowledge base
//   - DELETE /api/v1/knowledge-bases/{kb_id}       — delete knowledge base
//   - POST /api/v1/knowledge-bases/{kb_id}/hybrid-search — hybrid search
//   - GET  /api/v1/knowledge-bases/{kb_id}/knowledge    — list knowledge entries
//   - POST /api/v1/knowledge-bases/{kb_id}/knowledge    — create knowledge (text/markdown)
//   - DELETE /api/v1/knowledge-bases/{kb_id}/knowledge/{kid} — delete knowledge
//   - POST /api/v1/knowledge-search                — global search across KBs
//
// Auth: JWT Bearer token (obtained via /auth/login, refreshed via /auth/refresh)
// Note: WeKnora API paths use kebab-case (knowledge-bases), NOT snake_case.

const (
	weknoraMaxResponseSize = 10 * 1024 * 1024 // 10MB
	weknoraMaxSyncPages    = 20               // safety limit for batch fetch
)

type WeKnoraClient struct {
	baseURL  string
	apiKey   string // JWT token (or API key if available)
	kbID     string
	timeout  time.Duration
	client   *http.Client
}

// NewWeKnoraClient creates a new WeKnora API client.
// apiKey should be a JWT token obtained via Login(), or a long-lived API key if available.
func NewWeKnoraClient(baseURL, apiKey, kbID string, timeout time.Duration) *WeKnoraClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &WeKnoraClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		apiKey:  apiKey,
		kbID:    kbID,
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// IsConfigured returns true if all required fields are set and look like real values.
func (c *WeKnoraClient) IsConfigured() bool {
	if c == nil {
		return false
	}
	if c.baseURL == "" || c.apiKey == "" || c.kbID == "" {
		return false
	}
	for _, v := range []string{c.baseURL, c.apiKey, c.kbID} {
		if isPlaceholderKey(v) {
			return false
		}
	}
	return true
}

// ─── Auth ───────────────────────────────────────────────

// Login authenticates with WeKnora using email+password and returns a JWT token.
// The token can be used as the apiKey in NewWeKnoraClient().
func WeKnoraLogin(baseURL, email, password string, timeout time.Duration) (token string, tenantID int, err error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	client := &http.Client{Timeout: timeout}

	body := map[string]string{"email": email, "password": password}
	data, _ := json.Marshal(body)

	url := strings.TrimSuffix(baseURL, "/") + "/auth/login"
	req, err := http.NewRequest("POST", url, bytes.NewReader(data))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("WeKnora login request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, weknoraMaxResponseSize))
	if err != nil {
		return "", 0, fmt.Errorf("failed to read login response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("WeKnora login failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result weknoraLoginResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", 0, fmt.Errorf("failed to decode login response: %w", err)
	}
	if !result.Success || result.Token == "" {
		return "", 0, fmt.Errorf("WeKnora login unsuccessful: %s", string(respBody))
	}

	return result.Token, result.ActiveTenant.ID, nil
}

// ─── Internal HTTP helpers ──────────────────────────────

// doJSON sends a JSON request and decodes the WeKnora API envelope.
func (c *WeKnoraClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	respBody, statusCode, err := c.doRequest(ctx, method, path, body)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusCreated {
		return fmt.Errorf("WeKnora API %s %s failed (HTTP %d): %s", method, path, statusCode, string(respBody))
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return fmt.Errorf("failed to decode WeKnora response: %w", err)
	}
	return nil
}

// doRequest sends an HTTP request with the JWT token header and returns raw response body.
func (c *WeKnoraClient) doRequest(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	url := c.baseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("WeKnora API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, weknoraMaxResponseSize))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("failed to read response body: %w", err)
	}

	return respBody, resp.StatusCode, nil
}

// checkAPIError validates the WeKnora envelope error field.
func checkAPIError(err *weknoraAPIError) error {
	if err != nil {
		return fmt.Errorf("WeKnora API error code %d: %s", err.Code, err.Message)
	}
	return nil
}

// ─── Hybrid Search ──────────────────────────────────────

// Search performs a hybrid search (BM25 + Dense + optional GraphRAG) on the WeKnora knowledge base.
// Results are converted to engine.SearchResult for compatibility with the search pipeline.
func (c *WeKnoraClient) Search(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("WeKnora client not configured")
	}
	if limit <= 0 {
		limit = 5
	}

	body := map[string]any{
		"query_text": query,
		"top_k":      limit,
	}

	var resp weknoraAPIResponse[weknoraSearchData]
	if err := c.doJSON(ctx, "POST", fmt.Sprintf("/knowledge-bases/%s/hybrid-search", c.kbID), body, &resp); err != nil {
		return nil, err
	}
	if err := checkAPIError(resp.Error); err != nil {
		return nil, err
	}

	results := make([]engine.SearchResult, 0, len(resp.Data.Results))
	for _, item := range resp.Data.Results {
		title := item.Title
		if title == "" {
			title = "WeKnora知识条目"
		}
		results = append(results, engine.SearchResult{
			Title:   title,
			Snippet: title + "：" + truncateContent(item.Content, 500),
			URL:     item.Source,
			Source:  "weknora",
			Score:   item.Score,
		})
	}

	slog.Debug("weknora search done", "query", query, "count", len(results))
	return results, nil
}

// SearchRaw performs a hybrid search and returns the raw WeKnoraSearchResult items
// (with ID, Content, Score, Title, Source, Knowledge fields).
// This is used by the user material service which needs the document IDs.
func (c *WeKnoraClient) SearchRaw(ctx context.Context, query string, limit int) ([]WeKnoraSearchResult, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("WeKnora client not configured")
	}
	if limit <= 0 {
		limit = 5
	}

	body := map[string]any{
		"query_text": query,
		"top_k":      limit,
	}

	var resp weknoraAPIResponse[weknoraSearchData]
	if err := c.doJSON(ctx, "POST", fmt.Sprintf("/knowledge-bases/%s/hybrid-search", c.kbID), body, &resp); err != nil {
		return nil, err
	}
	if err := checkAPIError(resp.Error); err != nil {
		return nil, err
	}

	slog.Debug("weknora raw search done", "query", query, "count", len(resp.Data.Results))
	return resp.Data.Results, nil
}

// ─── Knowledge Management ────────────────────────────────

// CreateKnowledge creates a new knowledge entry from text/markdown content.
func (c *WeKnoraClient) CreateKnowledge(ctx context.Context, title, content string, metadata map[string]any) (string, error) {
	if !c.IsConfigured() {
		return "", fmt.Errorf("WeKnora client not configured")
	}

	body := map[string]any{"title": title, "content": content}
	if metadata != nil {
		body["metadata"] = metadata
	}

	var resp weknoraAPIResponse[weknoraCreateData]
	if err := c.doJSON(ctx, "POST", fmt.Sprintf("/knowledge-bases/%s/knowledge", c.kbID), body, &resp); err != nil {
		return "", err
	}
	if err := checkAPIError(resp.Error); err != nil {
		return "", err
	}

	slog.Info("weknora knowledge created", "id", resp.Data.ID, "title", title)
	return resp.Data.ID, nil
}

// CreateKnowledgeFromURL imports a web page into WeKnora by URL.
func (c *WeKnoraClient) CreateKnowledgeFromURL(ctx context.Context, url, title string) (string, error) {
	if !c.IsConfigured() {
		return "", fmt.Errorf("WeKnora client not configured")
	}

	body := map[string]any{"url": url}
	if title != "" {
		body["title"] = title
	}

	var resp weknoraAPIResponse[weknoraCreateData]
	if err := c.doJSON(ctx, "POST", fmt.Sprintf("/knowledge-bases/%s/knowledge", c.kbID), body, &resp); err != nil {
		return "", err
	}
	if err := checkAPIError(resp.Error); err != nil {
		return "", err
	}

	slog.Info("weknora knowledge created from URL", "id", resp.Data.ID, "url", url)
	return resp.Data.ID, nil
}

// ListKnowledge lists knowledge entries in the WeKnora knowledge base with pagination.
func (c *WeKnoraClient) ListKnowledge(ctx context.Context, page, pageSize int) ([]WeKnoraKnowledge, int, error) {
	if !c.IsConfigured() {
		return nil, 0, fmt.Errorf("WeKnora client not configured")
	}
	page, pageSize = clampPagination(page, pageSize, 20, 100)

	path := fmt.Sprintf("/knowledge-bases/%s/knowledge?page=%d&page_size=%d", c.kbID, page, pageSize)

	// WeKnora returns {"data": [...], "total": N, "page": P, "page_size": S, "success": true}
	// We need to parse data as a raw array and total separately.
	var raw struct {
		Data    []WeKnoraKnowledge `json:"data"`
		Total   int                `json:"total"`
		Success bool               `json:"success"`
		Error   *weknoraAPIError   `json:"error,omitempty"`
	}
	if err := c.doJSON(ctx, "GET", path, nil, &raw); err != nil {
		return nil, 0, err
	}
	if err := checkAPIError(raw.Error); err != nil {
		return nil, 0, err
	}

	return raw.Data, raw.Total, nil
}

// DeleteKnowledge deletes a knowledge entry from the WeKnora knowledge base.
func (c *WeKnoraClient) DeleteKnowledge(ctx context.Context, knowledgeID string) error {
	if !c.IsConfigured() {
		return fmt.Errorf("WeKnora client not configured")
	}

	respBody, statusCode, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/knowledge-bases/%s/knowledge/%s", c.kbID, knowledgeID), nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusNoContent {
		return fmt.Errorf("WeKnora delete knowledge failed (HTTP %d): %s", statusCode, string(respBody))
	}

	slog.Info("weknora knowledge deleted", "id", knowledgeID)
	return nil
}

// ─── File Upload ────────────────────────────────────────

// UploadFile uploads a file to the WeKnora knowledge base via multipart form.
// Supported formats: PDF, Word, Txt, Markdown, HTML, EPUB, images, CSV, Excel, PPT, JSON.
func (c *WeKnoraClient) UploadFile(ctx context.Context, filename string, fileContent io.Reader, title string) (string, error) {
	if !c.IsConfigured() {
		return "", fmt.Errorf("WeKnora client not configured")
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}
	if _, err := io.Copy(part, fileContent); err != nil {
		return "", fmt.Errorf("failed to copy file content: %w", err)
	}
	if title != "" {
		if err := writer.WriteField("title", title); err != nil {
			return "", fmt.Errorf("failed to write title field: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	url := c.baseURL + fmt.Sprintf("/knowledge-bases/%s/knowledge", c.kbID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, &buf)
	if err != nil {
		return "", fmt.Errorf("failed to create upload request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("WeKnora upload request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, weknoraMaxResponseSize))
	if err != nil {
		return "", fmt.Errorf("failed to read upload response: %w", err)
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("WeKnora upload failed (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result weknoraAPIResponse[weknoraCreateData]
	if err := json.Unmarshal(respBody, &result); err != nil {
		return "", fmt.Errorf("failed to decode WeKnora upload response: %w", err)
	}
	if err := checkAPIError(result.Error); err != nil {
		return "", err
	}

	slog.Info("weknora file uploaded", "id", result.Data.ID, "filename", filename)
	return result.Data.ID, nil
}

// ─── Knowledge Base Management ──────────────────────────

// ListKnowledgeBases lists all knowledge bases accessible to the authenticated user.
func (c *WeKnoraClient) ListKnowledgeBases(ctx context.Context) ([]WeKnoraKBInfo, error) {
	if c.apiKey == "" || c.baseURL == "" {
		return nil, fmt.Errorf("WeKnora client not configured")
	}

	// WeKnora returns {"data": [...KB objects...], "success": true}
	var raw struct {
		Data    []WeKnoraKBInfo  `json:"data"`
		Success bool             `json:"success"`
		Error   *weknoraAPIError `json:"error,omitempty"`
	}
	if err := c.doJSON(ctx, "GET", "/knowledge-bases", nil, &raw); err != nil {
		return nil, err
	}
	if err := checkAPIError(raw.Error); err != nil {
		return nil, err
	}

	return raw.Data, nil
}

// CreateKnowledgeBase creates a new knowledge base in WeKnora.
// Returns the KB ID of the newly created knowledge base.
func (c *WeKnoraClient) CreateKnowledgeBase(ctx context.Context, name, description string) (string, error) {
	if c.apiKey == "" || c.baseURL == "" {
		return "", fmt.Errorf("WeKnora client not configured")
	}

	body := CreateKBRequest{
		Name:        name,
		Description: description,
	}

	var resp weknoraAPIResponse[CreateKBResponse]
	if err := c.doJSON(ctx, "POST", "/knowledge-bases", body, &resp); err != nil {
		return "", err
	}
	if err := checkAPIError(resp.Error); err != nil {
		return "", err
	}

	slog.Info("weknora knowledge base created", "id", resp.Data.ID, "name", name)
	return resp.Data.ID, nil
}

// DeleteKnowledgeBase deletes a knowledge base from WeKnora.
func (c *WeKnoraClient) DeleteKnowledgeBase(ctx context.Context, kbID string) error {
	if c.apiKey == "" || c.baseURL == "" {
		return fmt.Errorf("WeKnora client not configured")
	}

	respBody, statusCode, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/knowledge-bases/%s", kbID), nil)
	if err != nil {
		return err
	}
	if statusCode != http.StatusOK && statusCode != http.StatusNoContent {
		return fmt.Errorf("WeKnora delete KB failed (HTTP %d): %s", statusCode, string(respBody))
	}

	slog.Info("weknora knowledge base deleted", "id", kbID)
	return nil
}

// ─── Batch Fetch (for Cron Sync) ────────────────────────

// FetchAllKnowledge fetches all knowledge entries from the WeKnora KB by paginating.
// Used by the cron sync job to pull content into the local pgvector store.
func (c *WeKnoraClient) FetchAllKnowledge(ctx context.Context, pageSize int) ([]WeKnoraKnowledge, error) {
	if !c.IsConfigured() {
		return nil, fmt.Errorf("WeKnora client not configured")
	}
	_, pageSize = clampPagination(1, pageSize, 50, 100)

	var allDocs []WeKnoraKnowledge
	for page := 1; page <= weknoraMaxSyncPages; page++ {
		docs, total, err := c.ListKnowledge(ctx, page, pageSize)
		if err != nil {
			return nil, fmt.Errorf("failed to fetch knowledge page %d: %w", page, err)
		}
		allDocs = append(allDocs, docs...)
		if page*pageSize >= total || len(docs) == 0 {
			break
		}
	}

	slog.Info("weknora fetch all knowledge completed", "total", len(allDocs))
	return allDocs, nil
}

// ─── Helpers ─────────────────────────────────────────────

// truncateContent truncates content to maxChars (rune-safe).
func truncateContent(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}

// clampPagination normalizes page/pageSize to valid ranges.
func clampPagination(page, pageSize, defaultSize, max int) (int, int) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > max {
		pageSize = defaultSize
	}
	return page, pageSize
}
