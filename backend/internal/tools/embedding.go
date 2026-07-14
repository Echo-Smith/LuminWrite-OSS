package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// EmbeddingClient is a client for the Dashscope (Alibaba Cloud) embedding API.
type EmbeddingClient struct {
	apiKey     string
	model      string
	dimension  int
	baseURL    string
	httpClient *http.Client
}

// NewEmbeddingClient creates a new EmbeddingClient.
func NewEmbeddingClient(apiKey, model string, dimension int) *EmbeddingClient {
	if model == "" {
		model = "text-embedding-v3"
	}
	if dimension <= 0 {
		dimension = 1024
	}
	return &EmbeddingClient{
		apiKey:     apiKey,
		model:      model,
		dimension:  dimension,
		baseURL:    "https://dashscope.aliyuncs.com/api/v1/services/embeddings/multimodal-embedding/multimodal-embedding",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// IsConfigured returns true if the API key is set.
func (c *EmbeddingClient) IsConfigured() bool {
	return c.apiKey != ""
}

// EmbeddingRequest is the request body for the Dashscope embedding API.
type EmbeddingRequest struct {
	Model      string   `json:"model"`
	Input      struct {
		Contents []string `json:"contents"`
	} `json:"input"`
	Parameters struct {
		Dimension int `json:"dimension"`
	} `json:"parameters"`
}

// EmbeddingResponse is the response from the Dashscope embedding API.
type EmbeddingResponse struct {
	Output struct {
		Embeddings []struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		} `json:"embeddings"`
	} `json:"output"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
	RequestID string `json:"request_id"`
}

// Embed generates embeddings for the given texts.
// Returns a slice of float64 slices (one per input text).
func (c *EmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float64, int, error) {
	if !c.IsConfigured() {
		return nil, 0, fmt.Errorf("embedding client not configured (DASHSCOPE_API_KEY not set)")
	}
	if len(texts) == 0 {
		return nil, 0, nil
	}

	// Dashscope supports batch embedding (up to 25 texts per request)
	const batchSize = 25
	var allEmbeddings [][]float64
	totalTokens := 0

	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batch := texts[start:end]

		reqBody := EmbeddingRequest{}
		reqBody.Model = c.model
		reqBody.Input.Contents = batch
		reqBody.Parameters.Dimension = c.dimension

		data, err := json.Marshal(reqBody)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to marshal embedding request: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL, bytes.NewReader(data))
		if err != nil {
			return nil, 0, fmt.Errorf("failed to create embedding request: %w", err)
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+c.apiKey)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("embedding API request failed: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to read embedding response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			return nil, 0, fmt.Errorf("embedding API returned status %d: %s", resp.StatusCode, string(body))
		}

		var embResp EmbeddingResponse
		if err := json.Unmarshal(body, &embResp); err != nil {
			return nil, 0, fmt.Errorf("failed to decode embedding response: %w", err)
		}

		for _, emb := range embResp.Output.Embeddings {
			allEmbeddings = append(allEmbeddings, emb.Embedding)
		}
		totalTokens += embResp.Usage.TotalTokens
	}

	slog.Debug("embedding generated",
		"texts", len(texts),
		"embeddings", len(allEmbeddings),
		"dimension", c.dimension,
		"tokens", totalTokens,
	)

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
