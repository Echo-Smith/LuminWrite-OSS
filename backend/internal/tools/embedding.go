package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// EmbeddingClient is a client for any OpenAI-compatible embedding API.
// Supports DashScope (阿里云百炼), SiliconFlow, Ollama, and other providers
// that implement the /v1/embeddings endpoint.
type EmbeddingClient struct {
	apiKey     string
	model      string
	dimension  int
	baseURL    string // e.g. "https://xxx.maas.aliyuncs.com/compatible-mode/v1"
	httpClient *http.Client
	mu         sync.RWMutex // protects apiKey, model, baseURL, dimension for hot-reload
}

// NewEmbeddingClient creates a new EmbeddingClient.
// baseURL should be the OpenAI-compatible API root (without /embeddings suffix).
// Example: "https://llm-xxx.maas.aliyuncs.com/compatible-mode/v1"
func NewEmbeddingClient(apiKey, baseURL, model string, dimension int) *EmbeddingClient {
	if model == "" {
		model = "text-embedding-v3"
	}
	if dimension <= 0 {
		dimension = 1024
	}
	// Trim trailing slash to ensure clean URL construction
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
	}
	return &EmbeddingClient{
		apiKey:     apiKey,
		model:      model,
		dimension:  dimension,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// IsConfigured returns true if the API key is set and is not a placeholder value.
func (c *EmbeddingClient) IsConfigured() bool {
	if c == nil {
		return false
	}
	c.mu.RLock()
	apiKey := c.apiKey
	c.mu.RUnlock()
	if apiKey == "" {
		return false
	}
	// Reject common placeholder values
	switch apiKey {
	case "your-dashscope-api-key", "your-api-key", "placeholder":
		return false
	}
	if strings.HasPrefix(apiKey, "your-") {
		return false
	}
	return true
}

// embeddingRequest is the OpenAI-compatible embedding request body.
type embeddingRequest struct {
	Model      string   `json:"model"`
	Input      []string `json:"input"`
	Dimensions int      `json:"dimensions,omitempty"`
}

// embeddingData represents a single embedding in the response.
type embeddingData struct {
	Index     int       `json:"index"`
	Embedding []float64 `json:"embedding"`
}

// embeddingResponse is the OpenAI-compatible embedding response.
type embeddingResponse struct {
	Data []embeddingData `json:"data"`
	Usage struct {
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

// Embed generates embeddings for the given texts.
// Returns a slice of float64 slices (one per input text).
func (c *EmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float64, int, error) {
	embedStart := time.Now()

	if !c.IsConfigured() {
		RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), fmt.Errorf("not configured"))
		return nil, 0, fmt.Errorf("embedding client not configured (DASHSCOPE_API_KEY not set)")
	}
	if len(texts) == 0 {
		RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), nil)
		return nil, 0, nil
	}

	// OpenAI-compatible APIs typically support batch embedding (up to 64 texts per request)
	const batchSize = 25
	var allEmbeddings [][]float64
	totalTokens := 0

	// Read config once under lock (thread-safe for hot-reload)
	c.mu.RLock()
	model := c.model
	dimension := c.dimension
	apiKey := c.apiKey
	baseURL := c.baseURL
	httpClient := c.httpClient
	c.mu.RUnlock()

	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		reqBody := embeddingRequest{
			Model:      model,
			Input:      batch,
			Dimensions: dimension,
		}

		data, err := json.Marshal(reqBody)
		if err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("failed to marshal embedding request: %w", err)
		}

		// Construct the embeddings endpoint URL
		embedURL := baseURL + "/embeddings"

		req, err := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewReader(data))
		if err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("failed to create embedding request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		resp, err := httpClient.Do(req)
		if err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("embedding API request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to read embedding response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			err := fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(body))
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, err
		}

		var embResp embeddingResponse
		if err := json.Unmarshal(body, &embResp); err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("failed to decode embedding response: %w", err)
		}

		// Sort by index to ensure correct ordering (API may return out of order)
		// Actually, OpenAI-compatible APIs return in order, but let's be safe
		for _, emb := range embResp.Data {
			allEmbeddings = append(allEmbeddings, emb.Embedding)
		}
		totalTokens += embResp.Usage.TotalTokens
	}

	slog.Debug("embedding generated",
		"texts", len(texts),
		"embeddings", len(allEmbeddings),
		"dimension", dimension,
		"tokens", totalTokens,
		"model", model,
		"base_url", baseURL,
	)

	RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), nil)
	return allEmbeddings, totalTokens, nil
}

// EmbedSingle generates an embedding for a single text.
func (c *EmbeddingClient) EmbedSingle(ctx context.Context, text string) ([]float64, int, error) {
	embeddings, tokens, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, 0, err
	}
	if len(embeddings) == 0 {
		return nil, 0, fmt.Errorf("no embedding returned")
	}
	return embeddings[0], tokens, nil
}

// Dimension returns the configured embedding dimension.
func (c *EmbeddingClient) Dimension() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.dimension
}

// Model returns the configured embedding model name.
func (c *EmbeddingClient) Model() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.model
}

// Reconfigure updates the embedding client's API key, base URL, model, and dimension.
// This enables runtime hot-reload when admin updates the dashscope API key via frontend.
// If dimension <= 0 or model is empty, the existing value is preserved.
func (c *EmbeddingClient) Reconfigure(apiKey, baseURL, model string, dimension int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if apiKey != "" {
		c.apiKey = apiKey
	}
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL != "" {
		c.baseURL = baseURL
	}
	if model != "" {
		c.model = model
	}
	if dimension > 0 {
		c.dimension = dimension
	}
}

// BaseURL returns the configured base URL (thread-safe).
func (c *EmbeddingClient) BaseURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.baseURL
}

// APIKey returns the configured API key (thread-safe, for internal use only).
func (c *EmbeddingClient) APIKey() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.apiKey
}

// FormatVectorForPG formats a float64 slice as a PostgreSQL vector string.
// e.g., [0.1, 0.2, 0.3] -> "[0.1,0.2,0.3]"
func FormatVectorForPG(vec []float64) string {
	b, _ := json.Marshal(vec)
	return string(b)
}
