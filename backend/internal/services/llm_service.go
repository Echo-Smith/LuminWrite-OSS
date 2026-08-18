package services

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// LLMService is a dynamic LLM client factory that resolves model configs
// and API keys from the database. It caches clients for a short TTL to
// avoid repeated DB lookups. If the database is unavailable or no model
// config is found, it falls back to the static env-based LLMClient.
type LLMService struct {
	adminRepo *database.AdminRepo
	fallback  *tools.LLMClient // env-based client used when DB is unavailable
	timeout   time.Duration

	mu       sync.RWMutex
	cache    map[string]*cacheEntry // key: model_name (or "default")
	cacheTTL time.Duration
}

type cacheEntry struct {
	client  *tools.LLMClient
	expires time.Time
}

// NewLLMService creates a new LLM service.
func NewLLMService(adminRepo *database.AdminRepo, fallback *tools.LLMClient, timeout time.Duration) *LLMService {
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	return &LLMService{
		adminRepo: adminRepo,
		fallback:  fallback,
		timeout:   timeout,
		cache:     make(map[string]*cacheEntry),
		cacheTTL:  30 * time.Second,
	}
}

// GetClient returns an LLM client for the given model name.
// If modelName is empty, returns the default model's client.
// Falls back to the env-based client if DB is unavailable or model not found.
func (s *LLMService) GetClient(ctx context.Context, modelName string) *tools.LLMClient {
	if s == nil {
		return nil
	}

	// If no admin repo, use fallback
	if s.adminRepo == nil {
		return s.fallback
	}

	key := modelName
	if key == "" {
		key = "default"
	}

	// Check cache
	s.mu.RLock()
	if entry, ok := s.cache[key]; ok && time.Now().Before(entry.expires) {
		s.mu.RUnlock()
		return entry.client
	}
	s.mu.RUnlock()

	// Resolve from DB
	var cfg *database.ModelConfig
	var err error
	if modelName == "" {
		cfg, err = s.adminRepo.GetDefaultModelConfig(ctx)
	} else {
		cfg, err = s.adminRepo.GetModelConfigByName(ctx, modelName)
	}
	if err != nil || cfg == nil {
		// Try default as fallback
		if modelName != "" {
			cfg, err = s.adminRepo.GetDefaultModelConfig(ctx)
		}
		if err != nil || cfg == nil {
			slog.Debug("LLMService: model config not found, using fallback", "model", modelName, "error", err)
			return s.fallback
		}
	}

	// Resolve API key from inline encrypted field
	apiKey := s.adminRepo.DecryptModelAPIKey(cfg.APIKeyEncrypted)

	// If no inline key, try legacy api_key_id lookup (backward compat)
	if apiKey == "" && cfg.APIKeyID != nil && *cfg.APIKeyID != "" {
		_, key, err := s.adminRepo.GetAPIKeyByID(ctx, *cfg.APIKeyID)
		if err != nil {
			slog.Warn("LLMService: failed to get API key by ID", "api_key_id", *cfg.APIKeyID, "error", err)
		} else {
			apiKey = key
		}
	}

	// If still no API key, try by provider (legacy api_keys table)
	if apiKey == "" && cfg.Provider != "" {
		key, baseURL, err := s.adminRepo.GetAPIKeyValue(ctx, cfg.Provider)
		if err == nil {
			apiKey = key
			if cfg.BaseURL == "" {
				cfg.BaseURL = baseURL
			}
		}
	}

	// If still no API key, use fallback client
	if apiKey == "" {
		slog.Debug("LLMService: no API key resolved, using fallback", "model", cfg.ModelName, "provider", cfg.Provider)
		return s.fallback
	}

	// Determine base URL
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURLForProvider(cfg.Provider)
	}

	client := tools.NewLLMClient(
		baseURL,
		apiKey,
		cfg.ModelName,
		cfg.MaxTokens,
		cfg.Temperature,
		s.timeout,
	)

	// Cache the client
	s.mu.Lock()
	s.cache[key] = &cacheEntry{
		client:  client,
		expires: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()

	slog.Debug("LLMService: created client from DB config", "model", cfg.ModelName, "provider", cfg.Provider, "base_url", baseURL)
	return client
}

// GetDefaultClient returns the default model's client (convenience method).
func (s *LLMService) GetDefaultClient(ctx context.Context) *tools.LLMClient {
	return s.GetClient(ctx, "")
}

// InvalidateCache clears all cached clients (e.g., after admin updates model config).
func (s *LLMService) InvalidateCache() {
	s.mu.Lock()
	s.cache = make(map[string]*cacheEntry)
	s.mu.Unlock()
}

// defaultBaseURLForProvider returns a sensible default base URL for known providers.
func defaultBaseURLForProvider(provider string) string {
	switch provider {
	case "deepseek":
		return "https://api.deepseek.com" // 不带 /v1 — 代码自动拼接 /chat/completions 和 /responses
	case "openai":
		return "https://api.openai.com/v1"
	case "qwen":
		return "https://dashscope.aliyuncs.com/compatible-mode/v1"
	case "kimi":
		return "https://api.moonshot.cn/v1"
	case "claude":
		return "https://api.anthropic.com/v1"
	default:
		return "https://api.deepseek.com" // 不带 /v1 — 代码自动拼接 /chat/completions 和 /responses
	}
}
