package editorial

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// LLMResolver resolves an LLM client dynamically from a database-backed
// service (e.g. services.LLMService). This allows admin panel model
// configuration changes to take effect without restarting the server.
//
// Implementations:
//   - *services.LLMService (primary — resolves from model_configs table)
//   - *StaticLLMResolver (fallback — wraps a single *tools.LLMClient)
type LLMResolver interface {
	GetClient(ctx context.Context, modelName string) *tools.LLMClient
}

// StaticLLMResolver wraps a single *tools.LLMClient to satisfy the LLMResolver
// interface. Used when no dynamic LLM service is available (e.g. DB not connected).
type StaticLLMResolver struct {
	Client *tools.LLMClient
}

// NewStaticLLMResolver creates a static LLM resolver from a single client.
func NewStaticLLMResolver(llm *tools.LLMClient) *StaticLLMResolver {
	return &StaticLLMResolver{Client: llm}
}

// GetClient returns the wrapped client, ignoring modelName.
func (s *StaticLLMResolver) GetClient(_ context.Context, _ string) *tools.LLMClient {
	return s.Client
}
