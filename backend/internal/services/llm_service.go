package services

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/auth"
)

// LLMService is a dynamic LLM client factory that resolves model configs
// and API keys from the database. It caches clients for a short TTL to
// avoid repeated DB lookups. If the database is unavailable or no model
// config is found, it falls back to the static env-based LLMClient.
//
// Resolution is user-aware (BYOK): when a user identity is present (explicit
// userID or the auth principal in ctx), the user's own model keys
// (user_model_keys) take precedence; the global model_configs table is the
// fallback for users without their own configuration; the env-based client
// is the last resort.
type LLMService struct {
	adminRepo *database.AdminRepo
	userKeys  *database.UserModelKeyRepo // optional; nil disables the BYOK layer
	fallback  *tools.LLMClient           // env-based client used when DB is unavailable
	timeout   time.Duration

	mu       sync.RWMutex
	cache    map[string]*cacheEntry // key: userID|model_name (or userID|default)
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

// WithUserKeys wires the per-user BYOK repo. Chainable; nil disables the
// user layer (global-only resolution).
func (s *LLMService) WithUserKeys(repo *database.UserModelKeyRepo) *LLMService {
	s.userKeys = repo
	return s
}

// cacheKey namespaces cached clients by user so one user's key can never be
// served to another. An empty userID means "system / anonymous" and shares
// the global-resolution slot.
func cacheKey(userID, modelName string) string {
	name := modelName
	if name == "" {
		name = "default"
	}
	return userID + "|" + name
}

// GetClient returns an LLM client for the given model name, resolving the
// caller's identity from the request context when present. If modelName is
// empty, returns the default model's client. Falls back to the env-based
// client if DB is unavailable or no config resolves.
func (s *LLMService) GetClient(ctx context.Context, modelName string) *tools.LLMClient {
	if s == nil {
		return nil
	}
	return s.GetClientForUser(ctx, auth.UserIDFromContext(ctx), modelName)
}

// GetClientForUser is the identity-explicit form of GetClient. Resolution
// order: user's exact model match → global exact match → user's default →
// global default → env fallback. Explicit model requests resolve against the
// layer that actually defines that model (a global model picked by name keeps
// using the platform key; a user model uses the user's key); defaults only
// kick in for unspecified requests. userID may be empty (system callers such
// as the governed worker, WABench, and cron — these intentionally resolve
// against the global layer only).
func (s *LLMService) GetClientForUser(ctx context.Context, userID, modelName string) *tools.LLMClient {
	if s == nil {
		return nil
	}

	// If no admin repo, use fallback
	if s.adminRepo == nil {
		return s.fallback
	}

	cacheID := cacheKey(userID, modelName)

	// Check cache
	s.mu.RLock()
	if entry, ok := s.cache[cacheID]; ok && time.Now().Before(entry.expires) {
		s.mu.RUnlock()
		return entry.client
	}
	s.mu.RUnlock()

	// ── Layer 1: the user's exact BYOK model ──
	if userID != "" && s.userKeys != nil && modelName != "" {
		if uk, _ := s.userKeys.GetForUserByName(ctx, userID, modelName); uk != nil {
			if client := s.clientFromUserKey(userID, uk); client != nil {
				s.storeCache(cacheID, client)
				return client
			}
		}
	}

	// ── Layer 2: global model_configs (exact name) ──
	if modelName != "" {
		if cfg, err := s.adminRepo.GetModelConfigByName(ctx, modelName); err == nil && cfg != nil {
			if client := s.clientFromGlobalConfig(ctx, cfg); client != nil {
				s.storeCache(cacheID, client)
				return client
			}
		}
	}

	// ── Layer 3: the user's default BYOK model ──
	if userID != "" && s.userKeys != nil {
		if uk, _ := s.userKeys.GetDefaultForUser(ctx, userID); uk != nil {
			if client := s.clientFromUserKey(userID, uk); client != nil {
				s.storeCache(cacheID, client)
				return client
			}
		}
	}

	// ── Layer 4: global default ──
	cfg, err := s.adminRepo.GetDefaultModelConfig(ctx)
	if err != nil || cfg == nil {
		slog.Debug("LLMService: model config not found, using fallback", "model", modelName, "error", err)
		// Negative caching: store the fallback under this key too, so a
		// deployment without model configs pays one DB lookup per TTL instead
		// of one per node dispatch (the governed pipeline resolves on every
		// dispatch; an uncached miss starves the pool under stress).
		s.storeCache(cacheID, s.fallback)
		return s.fallback
	}
	client := s.clientFromGlobalConfig(ctx, cfg)
	if client == nil {
		s.storeCache(cacheID, s.fallback)
		return s.fallback
	}
	s.storeCache(cacheID, client)
	slog.Debug("LLMService: created client from DB config", "model", cfg.ModelName, "provider", cfg.Provider, "user_id", userID)
	return client
}

// clientFromUserKey assembles a client from one BYOK entry; nil when the
// entry carries no usable key (the caller falls through to the next layer).
func (s *LLMService) clientFromUserKey(userID string, uk *database.UserModelKey) *tools.LLMClient {
	apiKey := s.userKeys.DecryptAPIKey(uk.APIKeyEncrypted)
	if apiKey == "" {
		slog.Debug("LLMService: user model key has no usable api key, falling through",
			"user_id", userID, "model", uk.ModelName)
		return nil
	}
	return s.buildClient(uk.Provider, uk.ModelName, uk.BaseURL, apiKey, uk.MaxTokens, uk.Temperature, uk.ReasoningEffort, uk.CustomHeaders)
}

// clientFromGlobalConfig resolves a global model config's API key through the
// legacy chain (inline → api_key_id → provider lookup) and builds the client;
// nil when no key material resolves.
func (s *LLMService) clientFromGlobalConfig(ctx context.Context, cfg *database.ModelConfig) *tools.LLMClient {
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

	if apiKey == "" {
		slog.Debug("LLMService: no API key resolved for global config", "model", cfg.ModelName, "provider", cfg.Provider)
		return nil
	}
	return s.buildClient(cfg.Provider, cfg.ModelName, cfg.BaseURL, apiKey, cfg.MaxTokens, cfg.Temperature, cfg.ReasoningEffort, cfg.CustomHeaders)
}

// buildClient assembles a tools.LLMClient from resolved config fields,
// applying provider default base URLs, reasoning effort, and custom headers.
func (s *LLMService) buildClient(provider, modelName, baseURL, apiKey string, maxTokens int, temperature float64, reasoningEffort string, customHeaders map[string]string) *tools.LLMClient {
	if baseURL == "" {
		baseURL = defaultBaseURLForProvider(provider)
	}
	client := tools.NewLLMClient(
		baseURL,
		apiKey,
		modelName,
		maxTokens,
		temperature,
		s.timeout,
	)
	// Apply reasoning effort from model config (can be overridden by ChatOption)
	if reasoningEffort != "" {
		client.SetReasoningEffort(reasoningEffort)
	}
	// Apply custom HTTP headers from model config (e.g. X-API-Key, X-Request-Source)
	if len(customHeaders) > 0 {
		client.SetCustomHeaders(customHeaders)
	}
	return client
}

func (s *LLMService) storeCache(key string, client *tools.LLMClient) {
	s.mu.Lock()
	s.cache[key] = &cacheEntry{
		client:  client,
		expires: time.Now().Add(s.cacheTTL),
	}
	s.mu.Unlock()
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

// InvalidateUserCache clears one user's cached clients (after BYOK writes).
// Global entries (empty-userID slot) are preserved so admin updates keep
// their own invalidation semantics.
func (s *LLMService) InvalidateUserCache(userID string) {
	if userID == "" {
		return
	}
	prefix := userID + "|"
	s.mu.Lock()
	for key := range s.cache {
		if strings.HasPrefix(key, prefix) {
			delete(s.cache, key)
		}
	}
	s.mu.Unlock()
}

// DefaultBaseURLForProvider returns the built-in default base URL for known
// providers (exported for handler-layer probes that resolve user-supplied
// endpoint configs).
func DefaultBaseURLForProvider(provider string) string {
	return defaultBaseURLForProvider(provider)
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
