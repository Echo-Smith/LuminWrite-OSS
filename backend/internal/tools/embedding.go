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
	if c.apiKey == "" {
		return false
	}
	// Reject common placeholder values
	switch c.apiKey {
	case "your-dashscope-api-key", "your-api-key", "placeholder":
		return false
	}
	if strings.HasPrefix(c.apiKey, "your-") {
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

	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		reqBody := embeddingRequest{
			Model:      c.model,
			Input:      batch,
			Dimensions: c.dimension,
		}

		data, err := json.Marshal(reqBody)
		if err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("failed to marshal embedding request: %w", err)
		}

		// Construct the embeddings endpoint URL
		embedURL := c.baseURL + "/embeddings"

		req, err := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewReader(data))
		if err != nil {
			RecordEmbeddingCall(time.Since(embedStart).Nanoseconds(), err)
			return nil, 0, fmt.Errorf("failed to create embedding request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(req)
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
		"dimension", c.dimension,
		"tokens", totalTokens,
		"model", c.model,
		"base_url", c.baseURL,
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
	return c.dimension
}

// Model returns the configured embedding model name.
func (c *EmbeddingClient) Model() string {
	return c.model
}

// FormatVectorForPG formats a float64 slice as a PostgreSQL vector string.
// e.g., [0.1, 0.2, 0.3] -> "[0.1,0.2,0.3]"
func FormatVectorForPG(vec []float64) string {
	b, _ := json.Marshal(vec)
	return string(b)
}
