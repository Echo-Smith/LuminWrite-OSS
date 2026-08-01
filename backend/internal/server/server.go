package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	ws "github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/mcp"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Server holds all application dependencies.
type Server struct {
	cfg           *config.Config
	hub           *websocket.Hub
	sseHub        *SSEHub
	rateLimiter   *RateLimiter
	llm           *tools.LLMClient
	llmSvc        *services.LLMService
	search        *tools.SearchClient
	embedding     *tools.EmbeddingClient
	profiles      *profile.Loader
	traces        *database.TraceRepo
	feedback      *database.FeedbackRepo
	evalRepo      *database.EvaluationRepo
	adminRepo     *database.AdminRepo
	kbRepo        *database.KnowledgeBaseRepo
	evalSvc       *services.EvaluationService
	reputationSvc *services.ReputationService
	dbAvail       bool
	sessions      sync.Map // traceID → *engine.ExecutionContext
	cronScheduler *services.CronScheduler
	metrics       *MetricsRegistry
	webauthn      *WebAuthnService
	passkeyChallenges *passkeyChallengeStore
	sensitiveSvc  *services.SensitiveCheckService
	jiaozhen      *tools.JiaozhenClient
	memorySvc     *memsvc.Service
	mcpRegistry   *mcp.Registry
	toolRegistry  *engine.ToolRegistry
	editorialSvc  *editorial.Service
	editorialHdlr *editorial.Handlers

	userStyleStore  *database.UserStyleStore
	styleBuilder   *services.StyleBuilderService

	// Knowledge Manager (operates directly on local PG)
	kbMgr *services.KbManager
}

// New creates a new Server.
func New(cfg *config.Config) (*Server, error) {
	var llm *tools.LLMClient
	if cfg.DeepSeek.APIKey != "" {
		llm = tools.NewLLMClient(
			cfg.DeepSeek.BaseURL,
			cfg.DeepSeek.APIKey,
			cfg.DeepSeek.DefaultModel,
			cfg.DeepSeek.MaxTokens,
			cfg.DeepSeek.Temperature,
			cfg.DeepSeek.Timeout,
		)
	} else {
		slog.Warn("AI_API_KEY not set, LLM features will be limited")
	}

	// Create embedding client (OpenAI-compatible — supports DashScope MaaS, SiliconFlow, Ollama, etc.)
	embeddingClient := tools.NewEmbeddingClient(
		cfg.Dashscope.APIKey,
		cfg.Dashscope.BaseURL,
		cfg.Dashscope.Model,
		cfg.Dashscope.Dimension,
	)
	if embeddingClient.IsConfigured() {
		slog.Info("embedding client configured",
			"model", cfg.Dashscope.Model,
			"dimension", cfg.Dashscope.Dimension,
			"base_url", cfg.Dashscope.BaseURL,
		)
	} else {
		slog.Warn("DASHSCOPE_API_KEY not set, semantic search will use text fallback")
	}

	// Create rate limiter
	var rateLimiter *RateLimiter
	if cfg.RateLimit.Enabled {
		rateLimiter = NewRateLimiter(cfg.RateLimit.Requests, cfg.RateLimit.Window)
		slog.Info("rate limiter enabled", "requests", cfg.RateLimit.Requests, "window", cfg.RateLimit.Window)
	}

	// Try connecting to database (non-fatal if unavailable)
	var traceRepo *database.TraceRepo
	var feedbackRepo *database.FeedbackRepo
	var evalRepo *database.EvaluationRepo
	var adminRepo *database.AdminRepo
	var kbRepo *database.KnowledgeBaseRepo
	dbAvail := false
	db, err := database.NewPostgres(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		slog.Warn("database unavailable, running without persistence", "error", err)
	} else {
		dbAvail = true
		traceRepo = database.NewTraceRepo(db)
		feedbackRepo = database.NewFeedbackRepo(db)
		evalRepo = database.NewEvaluationRepo(db)
		adminRepo = database.NewAdminRepo(db)
		if cfg.Admin.EncryptionKey != "" {
			adminRepo = adminRepo.WithEncryptionKey(crypto.DeriveKey(cfg.Admin.EncryptionKey))
			slog.Info("API key encryption enabled")
		}
		kbRepo = database.NewKnowledgeBaseRepo(db, embeddingClient)
		if err := database.Migrate(db); err != nil {
			slog.Error("database migration failed — refusing to start with incomplete schema", "error", err)
			return nil, fmt.Errorf("database migration failed: %w", err)
		}
	}

	// ── Override search config with database-stored API keys ──
	// If admin has configured API keys via the frontend, they override env vars.
	// This allows runtime configuration without restarting the server.
	if dbAvail && adminRepo != nil {
		ctx := context.Background()
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "tavily"); err == nil && key != "" {
			cfg.Tavily.APIKey = key
			if baseURL != "" {
				cfg.Tavily.Endpoint = baseURL
			}
			slog.Info("search config overridden from DB", "provider", "tavily")
		}
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "anysearch"); err == nil && key != "" {
			cfg.AnySearch.APIKey = key
			if baseURL != "" {
				cfg.AnySearch.Endpoint = baseURL
			}
			slog.Info("search config overridden from DB", "provider", "anysearch")
		}
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "zhihu"); err == nil && key != "" {
			cfg.Zhihu.AccessSecret = key
			if baseURL != "" {
				cfg.Zhihu.BaseURL = baseURL
			}
			cfg.Zhihu.Enabled = true
			slog.Info("search config overridden from DB", "provider", "zhihu")
		}
	}

	searchClient := tools.NewSearchClient(
		cfg.Tavily.APIKey, cfg.Tavily.Endpoint, cfg.Tavily.Timeout,
		cfg.Zhihu.Enabled, cfg.Zhihu.BaseURL, cfg.Zhihu.AccessSecret, cfg.Zhihu.Timeout,
		cfg.IMA.BaseURL, cfg.IMA.ClientID, cfg.IMA.APIKey, cfg.IMA.KBID, cfg.IMA.Timeout,
		cfg.Tencent.Enabled, cfg.Tencent.BaseURL, cfg.Tencent.Timeout,
		cfg.Weibo.Enabled, cfg.Weibo.BaseURL, cfg.Weibo.Timeout,
		cfg.ExtraHot.Enabled, cfg.ExtraHot.BaseURL, cfg.ExtraHot.Timeout,
		cfg.Bing.Enabled, cfg.Bing.BaseURL, cfg.Bing.Timeout,
		cfg.Jiaozhen.CLIPath, cfg.Jiaozhen.Timeout,
		cfg.AnySearch.APIKey, cfg.AnySearch.Endpoint, cfg.AnySearch.Timeout,
	)

	if !searchClient.HasSources() {
		slog.Warn("no search sources configured")
	}

	profileLoader := profile.NewLoader()

	// If DB is available, load profiles from DB (seeds built-in if empty)
	if dbAvail {
		profileLoader.WithDB(db)
		// Enable in-process L2 cache (LRU) for cross-goroutine profile caching
		l2Backend := profile.NewLRUCacheBackend(128, 10*time.Minute)
		profileLoader.WithL2Cache(profile.NewProfileL2Cache(l2Backend, 5*time.Minute))
	}

	// Create evaluation service
	evalSvc := services.NewEvaluationService(evalRepo, llm, profileLoader)

	// Create reputation service
	var reputationSvc *services.ReputationService
	if dbAvail {
		reputationSvc = services.NewReputationService(feedbackRepo, db)
	}

	// Wire profile publish hook to trigger auto-evaluation
	if dbAvail {
		profileLoader.PublishHook = func(slug string, version int, detail string) {
			evalSvc.TriggerEvaluationIfProfileChanged(context.Background(), slug, version, detail)
		}
	}

	// Create cron scheduler (only if DB is available)
	var cronSched *services.CronScheduler
	if dbAvail && adminRepo != nil {
		cronSched = services.NewCronScheduler(adminRepo)
	}

	// Create sensitive check service
	var sensitiveSvc *services.SensitiveCheckService
	if dbAvail && adminRepo != nil {
		sensitiveSvc = services.NewSensitiveCheckService(adminRepo)
		slog.Info("sensitive check service initialized")
	}

	// Create jiaozhen fact-checking client (optional)
	var jiaozhenClient *tools.JiaozhenClient
	if cfg.Jiaozhen.Enabled {
		jiaozhenClient = tools.NewJiaozhenClient(
			cfg.Jiaozhen.Enabled,
			cfg.Jiaozhen.CLIPath,
			cfg.Jiaozhen.CommandArgs,
			cfg.Jiaozhen.APIKey,
			cfg.Jiaozhen.Timeout,
			cfg.Jiaozhen.MaxClaims,
		)
		if jiaozhenClient.IsConfigured() {
			slog.Info("jiaozhen fact-checking enabled")
		} else {
			slog.Warn("jiaozhen enabled but CLI not found — install tencent-news-cli")
		}
	}

	// Create memory service (optional, requires DB + LLM + Embedding)
	var memorySvc *memsvc.Service
	if dbAvail {
		memorySvc = memsvc.NewService(db, llm, embeddingClient)
	}

	// Create LLM service (dynamic client factory with DB-backed model configs)
	llmSvc := services.NewLLMService(adminRepo, llm, cfg.DeepSeek.Timeout)

	// Initialize MCP registry — connect to configured MCP servers
	mcpRegistry := mcp.NewRegistry()
	for _, mcpCfg := range cfg.MCPServers {
		_ = mcpRegistry.Connect(context.Background(), mcp.MCPClientConfig{
			Name:      mcpCfg.Name,
			Transport: mcpCfg.Transport,
			Command:   mcpCfg.Command,
			Args:      mcpCfg.Args,
			Env:       mcpCfg.Env,
			URL:       mcpCfg.URL,
		})
	}

	// Initialize ToolRegistry — unified registry for all tools
	// (Steps + Built-in tools + MCP tools)
	toolRegistry := engine.NewToolRegistry()

	s := &Server{
		cfg:           cfg,
		hub:           websocket.NewHub(),
		sseHub:        NewSSEHub(),
		rateLimiter:   rateLimiter,
		llm:           llm,
		llmSvc:        llmSvc,
		search:        searchClient,
		embedding:     embeddingClient,
		profiles:      profileLoader,
		traces:        traceRepo,
		feedback:      feedbackRepo,
		evalRepo:      evalRepo,
		adminRepo:     adminRepo,
		kbRepo:        kbRepo,
		evalSvc:       evalSvc,
		reputationSvc: reputationSvc,
		dbAvail:       dbAvail,
		cronScheduler: cronSched,
		metrics:       NewMetricsRegistry(),
		webauthn:      NewWebAuthnService(cfg.WebAuthn.RPID, cfg.WebAuthn.RPName, cfg.WebAuthn.RPOrigin),
		passkeyChallenges: newPasskeyChallengeStore(),
		sensitiveSvc:  sensitiveSvc,
		jiaozhen:      jiaozhenClient,
		memorySvc:     memorySvc,
		mcpRegistry:   mcpRegistry,
		toolRegistry:  toolRegistry,
	}

	// ── User custom styles & AI builder ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		s.userStyleStore = database.NewUserStyleStore(db)
	}
	if llm != nil {
		s.styleBuilder = services.NewStyleBuilderService(llm)
	}

	// ── Knowledge Manager (operates directly on local PG) ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		s.kbMgr = services.NewKbManager(adminRepo.DB().DB, embeddingClient)

		// Wire GraphRAG — entity extraction + relation graph (replaces WeKnora's graph pipeline)
		if llm != nil {
			graphRAG := services.NewGraphRAGManager(adminRepo.DB().DB, embeddingClient, llm)
			s.kbMgr.SetGraphRAG(graphRAG)
			slog.Info("GraphRAG entity extraction wired into knowledge base",
				"entity_types", "person/organization/location/event/concept/product",
				"relation_types", "Author/Alias/Member_of/Located_in/Participated_in/Created/Related_to/Caused/Target_of",
			)
		}

		slog.Info("knowledge manager initialized",
			"docreader_addr", cfg.Kb.DocreaderAddr,
			"chunk_size", cfg.Kb.ChunkSize,
			"chunk_overlap", cfg.Kb.ChunkOverlap,
		)

		// Wire local KB into the multi-source search pipeline
		searchClient.SetKnowledgeSearcher(services.NewKbSearchAdapter(s.kbMgr))
		slog.Info("local knowledge base wired into search pipeline (BM25 + Dense + RRF)")
	} else {
		slog.Warn("knowledge manager skipped: database not available")
	}

	// ── Editorial system initialization ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		edStore := editorial.NewStore(adminRepo.DB().DB)
		edEmitter := &editorialWSEmitter{hub: s.hub}
		edSvc := editorial.NewService(edStore, edEmitter)

		// Wire source credibility into search client
		// This enriches search results with credibility scores from editorial_source_credibility
		searchClient.SetCredibilityLookup(editorial.NewCredibilityLookupAdapter(edStore))
		slog.Info("source credibility lookup wired into search client")

		// Register Agent executors (adapt V2 Steps to editorial AgentExecutor)
		if llm != nil {
			edSvc.Orchestrator().RegisterExecutor(editorial.NewResearchAgentExecutor(llm, searchClient, embeddingClient, edStore))
			if defaultProfile, ok := profileLoader.Get("yinyue"); ok {
				edSvc.Orchestrator().RegisterExecutor(editorial.NewWritingAgentExecutor(llm, defaultProfile, searchClient, edStore))
				edSvc.Orchestrator().RegisterExecutor(editorial.NewReviewAgentExecutor(llm, defaultProfile, searchClient, edStore))
			}

			// 初始化对照实验运行器
			expRunner := editorial.NewExperimentRunner(
				edStore, llm, searchClient, embeddingClient,
				profileLoader, edSvc.Orchestrator(),
			)
			editorial.SetExperimentRunner(expRunner)
		}

		s.editorialSvc = edSvc
		s.editorialHdlr = editorial.NewHandlers(edSvc)
		slog.Info("editorial system initialized")
	} else {
		slog.Warn("editorial system disabled — database not available")
	}

	return s, nil
}

// Router returns the HTTP router with all routes registered.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()

	// Middleware
	r.Use(corsMiddleware)
	r.Use(loggingMiddleware)
	r.Use(s.metricsMiddleware)
	r.Use(s.rateLimitMiddleware)

	// Health check
	r.Get("/health", s.handleHealth)

	// Prometheus metrics
	r.Get("/metrics", s.handleMetrics)

	// API v2
	r.Route("/api/v2", func(r chi.Router) {
		// Styles (jwtOptional: logged-in users see global + their private styles)
		r.With(s.jwtOptionalMiddleware).Get("/styles", s.handleListStylesWithUserStyles)
		r.Get("/styles/{slug}", s.handleGetStyle)

		// User Custom Styles (requires auth)
		r.With(s.jwtAuthMiddleware).Get("/my-styles", s.handleListMyStyles)
		r.With(s.jwtAuthMiddleware).Post("/my-styles", s.handleCreateMyStyle)
		r.With(s.jwtAuthMiddleware).Get("/my-styles/{id}", s.handleGetMyStyle)
		r.With(s.jwtAuthMiddleware).Put("/my-styles/{id}", s.handleUpdateMyStyle)
		r.With(s.jwtAuthMiddleware).Delete("/my-styles/{id}", s.handleDeleteMyStyle)
		r.With(s.jwtAuthMiddleware).Post("/my-styles/{id}/submit", s.handleSubmitMyStyleForReview)

		// AI Style Builder (requires auth)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions", s.handleCreateBuilderSession)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions/{id}/messages", s.handleSendBuilderMessage)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions/{id}/commit", s.handleCommitBuilderSession)

		// Models (public — list active models for composer)
		r.Get("/models", s.handleListActiveModels)

		// Topics
		r.Get("/topics", s.handleListTopics)
		r.Post("/topics", s.handleCreateTopic)
		r.Post("/topics/hot", s.handleFetchHotTopics)
		r.Delete("/topics/{id}", s.handleDeleteTopic)
r.Put("/topics/{id}", s.handleUpdateTopic)
		r.Get("/topics/recommend", s.handleTopicRecommend)
		r.Get("/topics/favorites", s.handleListFavoriteTopics)
		r.Get("/topics/platforms", s.handlePlatformStats)
		r.Get("/topics/platforms/{platform}", s.handleListTopicsByPlatform)
		r.Get("/topics/{id}/detail", s.handleTopicDetail)
		r.Get("/topics/{id}/trend", s.handleTopicTrend)
		r.With(s.jwtAuthMiddleware).Post("/topics/{id}/favorite", s.handleFavoriteTopic)
		r.With(s.jwtAuthMiddleware).Delete("/topics/{id}/favorite", s.handleUnfavoriteTopic)

	// Feedback
	r.Post("/feedback", s.handleFeedback)
	r.Get("/feedback/aggregation", s.handleListAggregations)
	r.Get("/feedback/aggregation/{style}/{version}", s.handleGetAggregation)
	r.Post("/feedback/aggregate", s.handleAggregateFeedback)
	r.Post("/feedback/suggestions/{style}/{version}", s.handleGenerateSuggestions)

	// Memory
	r.Get("/memories", s.handleListMemories)
	r.Post("/memories", s.handleCreateMemory)
	r.Delete("/memories/{id}", s.handleDeleteMemory)
	r.Post("/memories/{id}/dismiss", s.handleDismissMemory)

		// User Preferences (cloud-synced settings)
		r.With(s.jwtAuthMiddleware).Get("/preferences", s.handleGetPreferences)
		r.With(s.jwtAuthMiddleware).Put("/preferences", s.handleUpdatePreferences)

		// Workbuddy Adoption Callback
		r.Post("/workbuddy/adopt", s.handleWorkbuddyAdoption)
		r.Get("/workbuddy/adoptions/{traceId}", s.handleAdoptionHistory)

		// User Reputation
		r.Get("/reputation/{userId}", s.handleGetReputation)
		r.Post("/reputation/{userId}/recalculate", s.handleRecalculateReputation)
		r.Get("/reputation/{userId}/history", s.handleReputationHistory)

		// Knowledge Base (legacy simple KB — list/add/delete on knowledge_base table)
		r.Get("/kb", s.handleKBList)
		r.Post("/kb", s.handleKBAdd)
		r.Delete("/kb/{id}", s.handleKBDelete)

		// Knowledge Base (Hybrid Search + Document Management)
		// Primary paths: /kb/* (new — operates on knowledge_chunks with BM25+Dense+RRF)
		r.Get("/kb/kbs", s.handleKBListKBs)
		r.Post("/kb/manage", s.handleKBCreate)
		r.Put("/kb/manage/{id}", s.handleKBUpdate)
		r.Delete("/kb/manage/{id}", s.handleKBDeleteKB)
		r.Get("/kb/knowledge", s.handleKBListKnowledge)
		r.Post("/kb/knowledge", s.handleKBAddKnowledge)
		r.Post("/kb/knowledge/url", s.handleKBAddFromURL)
		r.Post("/kb/knowledge/upload", s.handleKBUploadFile)
		r.Delete("/kb/knowledge/{id}", s.handleKBDeleteKnowledge)
		r.Post("/kb/search", s.handleKBSearch)
		r.Get("/kb/status", s.handleKBStatus)
		r.Get("/kb/stats", s.handleKBStats)
		r.Get("/kb/documents/{id}/chunks", s.handleKBGetDocumentChunks)
		r.Get("/kb/documents/{id}/entities", s.handleKBGetDocumentEntities)
		r.Get("/kb/graph", s.handleKBGetGraph)

		// Compat alias: /weknora/* (kept for frontend transition)
		r.Get("/weknora/kbs", s.handleKBListKBs)
		r.Get("/weknora/knowledge", s.handleKBListKnowledge)
		r.Post("/weknora/knowledge", s.handleKBAddKnowledge)
		r.Post("/weknora/knowledge/url", s.handleKBAddFromURL)
		r.Post("/weknora/knowledge/upload", s.handleKBUploadFile)
		r.Delete("/weknora/knowledge/{id}", s.handleKBDeleteKnowledge)
		r.Post("/weknora/search", s.handleKBSearch)
		r.Get("/weknora/status", s.handleKBStatus)

		// User Materials (Scheme B: per-user WeKnora KB)
		r.With(s.jwtAuthMiddleware).Get("/materials", s.handleUserMaterialList)
		r.With(s.jwtAuthMiddleware).Post("/materials", s.handleUserMaterialCreate)
		r.With(s.jwtAuthMiddleware).Post("/materials/upload", s.handleUserMaterialUpload)
		r.With(s.jwtAuthMiddleware).Delete("/materials/{id}", s.handleUserMaterialDelete)
		r.With(s.jwtAuthMiddleware).Post("/materials/search", s.handleUserMaterialSearch)

		// Topic-Material Association
		r.With(s.jwtAuthMiddleware).Get("/topics/{topicId}/materials", s.handleTopicMaterialList)
		r.With(s.jwtAuthMiddleware).Post("/topics/{topicId}/materials/{materialId}", s.handleTopicMaterialAssociate)
		r.With(s.jwtAuthMiddleware).Delete("/topics/{topicId}/materials/{materialId}", s.handleTopicMaterialRemove)
		r.With(s.jwtAuthMiddleware).Post("/topics/{topicId}/materials/auto", s.handleTopicMaterialAuto)

		// Evaluation
		r.Get("/evaluation/sets", s.handleListEvalSets)
		r.Post("/evaluation/sets", s.handleCreateEvalSet)
		r.Get("/evaluation/sets/{id}", s.handleGetEvalSet)
		r.Get("/evaluation/sets/{id}/export", s.handleExportEvalSet)
		r.Post("/evaluation/sets/{id}/samples", s.handleAddEvalSamples)
		r.Get("/evaluation/sets/{id}/samples", s.handleListEvalSamples)
		r.Post("/evaluation/runs", s.handleCreateEvalRun)
		r.Get("/evaluation/runs", s.handleListEvalRuns)
		r.Get("/evaluation/runs/compare", s.handleCompareEvalRuns)
		r.Get("/evaluation/runs/{id}", s.handleGetEvalRun)
r.Get("/evaluation/runs/{id}/export/{format}", s.handleExportEvalRun)

		// WebSocket
		r.Get("/ws/agent", s.handleWebSocket)

		// SSE (Server-Sent Events)
		r.Get("/sse/topics", s.handleSSETopics)
		r.Get("/topics/stream", s.handleSSETopics) // alias per docs/03-api-specification.md
		r.Get("/sse/stats", s.handleSSEStats)

		// Editorial (编辑部系统) — JWT-protected
		if s.editorialHdlr != nil {
			r.Group(func(r chi.Router) {
				r.Use(s.jwtAuthMiddleware)
				s.editorialHdlr.RegisterRoutes(r)
			})
		}

// Auth (JWT)
r.Post("/auth/login", s.handleLogin)
r.Post("/auth/register", s.handleRegister)
r.Post("/auth/guest", s.handleGuestLogin)
r.Post("/auth/refresh", s.handleRefreshToken)
		r.With(s.jwtAuthMiddleware).Get("/auth/verify", s.handleVerifyToken)

		// Passkey / WebAuthn
		r.Post("/auth/passkey/register/begin", s.handlePasskeyRegisterBegin)
		r.Post("/auth/passkey/register/complete", s.handlePasskeyRegisterComplete)
		r.Post("/auth/passkey/login/begin", s.handlePasskeyLoginBegin)
		r.Post("/auth/passkey/login/complete", s.handlePasskeyLoginComplete)
		r.With(s.jwtAuthMiddleware).Get("/auth/passkey/list", s.handlePasskeyList)
		r.With(s.jwtAuthMiddleware).Delete("/auth/passkey/{id}", s.handlePasskeyDelete)

		// Sessions (user-facing, JWT-protected)
		r.With(s.jwtAuthMiddleware).Get("/sessions", s.handleListUserSessions)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}", s.handleGetUserSession)
		r.With(s.jwtAuthMiddleware).Delete("/sessions/{traceId}", s.handleDeleteUserSession)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/artifacts", s.handleGetSessionArtifacts)
		r.With(s.jwtAuthMiddleware).Post("/auth/change-password", s.handleChangePassword)

		// Admin (protected by admin token)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.adminAuthMiddleware)

			// Dashboard stats
			r.Get("/stats", s.handleAdminStats)
			r.Get("/exit-stats", s.handleAdminExitStats)

			// Traces
			r.Get("/traces", s.handleAdminListTraces)
			r.Get("/traces/{traceId}", s.handleAdminGetTrace)

			// Style Profile CRUD
			r.Get("/styles", s.handleAdminListStyles)
			r.Get("/styles/{slug}", s.handleAdminGetStyle)
			r.Post("/styles", s.handleAdminCreateStyle)
			r.Put("/styles/{slug}", s.handleAdminUpdateStyle)
			r.Post("/styles/{slug}/publish", s.handleAdminPublishStyle)
			r.Post("/styles/{slug}/archive", s.handleAdminArchiveStyle)
			r.Get("/styles/{slug}/versions", s.handleAdminListVersions)
			r.Post("/styles/{slug}/versions/{version}/republish", s.handleAdminRepublishVersion)
            r.Get("/styles/{slug}/versions/compare", s.handleAdminCompareVersions)

            // Community style review (pending user submissions)
            r.Get("/pending-styles", s.handleAdminListPendingStyles)
            r.Post("/pending-styles/{id}/approve", s.handleAdminApproveStyle)
            r.Post("/pending-styles/{id}/reject", s.handleAdminRejectStyle)

            // Rollout (Grayscale)
            r.Get("/styles/{slug}/rollout", s.handleAdminGetRollout)
            r.Put("/styles/{slug}/rollout", s.handleAdminUpdateRollout)
            r.Post("/styles/{slug}/rollout/preview", s.handleAdminPreviewRollout)

        // Sensitive Words
            r.Get("/sensitive-words", s.handleAdminListSensitiveWords)
            r.Post("/sensitive-words", s.handleAdminAddSensitiveWord)
            r.Delete("/sensitive-words/{id}", s.handleAdminDeleteSensitiveWord)
            r.Put("/sensitive-words/config", s.handleAdminSensitiveConfig)
            r.Get("/sensitive-words/config", s.handleAdminSensitiveConfig)

            // Model Configs
            r.Get("/models", s.handleAdminListModelConfigs)
            r.Post("/models", s.handleAdminCreateModelConfig)
            r.Post("/models/discover", s.handleAdminDiscoverModels)
            r.Put("/models/{id}", s.handleAdminUpdateModelConfig)
            r.Delete("/models/{id}", s.handleAdminDeleteModelConfig)

            // API Keys
            r.Get("/api-keys", s.handleAdminListAPIKeys)
            r.Post("/api-keys", s.handleAdminCreateAPIKey)
            r.Put("/api-keys/{id}", s.handleAdminUpdateAPIKey)
            r.Delete("/api-keys/{id}", s.handleAdminDeleteAPIKey)
            r.Post("/api-keys/{id}/test", s.handleAdminTestAPIKey)

            // Token Usage
            r.Get("/token-usage", s.handleAdminTokenUsage)

            // Cron Jobs
            r.Get("/cron-jobs", s.handleAdminListCronJobs)
            r.Post("/cron-jobs", s.handleAdminCreateCronJob)
            r.Put("/cron-jobs/{id}", s.handleAdminUpdateCronJob)
            r.Delete("/cron-jobs/{id}", s.handleAdminDeleteCronJob)
            r.Post("/cron-jobs/{id}/run", s.handleAdminRunCronJob)

            // Knowledge Base Admin
            r.Post("/kb/generate-embeddings", s.handleKBGenerateEmbeddings)

            // SSE Push (admin only)
            r.Post("/sse/push", s.handleSSEPushTopic)
        })
    })

	return r
}

// ─── Hot Topics Auto-Fetch ──────────────────────────────

// autoFetchHotTopics periodically fetches hot topics from external sources.
func (s *Server) autoFetchHotTopics(ctx context.Context, interval time.Duration) {
	if s.search == nil || s.traces == nil {
		slog.Info("hot topics auto-fetch skipped: search or db not configured")
		return
	}
	if interval <= 0 {
		interval = 10 * time.Minute
	}

	// Fetch immediately on startup
	if err := s.cronFetchHotTopics(ctx); err != nil {
		slog.Warn("hot topics auto-fetch failed on startup", "error", err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.cronFetchHotTopics(ctx); err != nil {
				slog.Warn("hot topics auto-fetch failed", "error", err)
			}
		}
	}
}

// Start starts the HTTP server.
func (s *Server) Start(ctx context.Context) error {
	srv := &http.Server{
		Addr:         s.cfg.ListenAddr(),
		Handler:      s.Router(),
		ReadTimeout:  s.cfg.Server.ReadTimeout,
		WriteTimeout: s.cfg.Server.WriteTimeout,
	}

	go func() {
		<-ctx.Done()
		slog.Info("shutting down server...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.WriteTimeout)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	// Start SSE topic push background task
	go s.PushTopicsFromDB(ctx, 30*time.Second)

	// Start hot topics auto-fetch (every 10 minutes)
	go s.autoFetchHotTopics(ctx, s.cfg.HotTopics.FetchInterval)

	// Start cron scheduler
	if s.cronScheduler != nil {
		go s.cronScheduler.Start(ctx, s.executeCronJob)
	}

	slog.Info("server starting", "addr", s.cfg.ListenAddr())
	return srv.ListenAndServe()
}

// ─── Handlers ────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	imaConfigured := false
	if s.search != nil && s.search.IMAClient() != nil {
		imaConfigured = s.search.IMAClient().IsConfigured()
	}

	embeddingConfigured := false
	if s.embedding != nil {
		embeddingConfigured = s.embedding.IsConfigured()
	}

	response.OK(w, map[string]interface{}{
		"status":              "ok",
		"version":             "v2",
		"llm_configured":      s.llm != nil,
		"search_configured":   s.search != nil && s.search.HasSources(),
		"db_configured":       s.dbAvail,
		"ima_configured":      imaConfigured,
		"embedding_configured": embeddingConfigured,
	})
}

func (s *Server) handleListStyles(w http.ResponseWriter, r *http.Request) {
	styles := s.profiles.List()
	response.OK(w, map[string]interface{}{"styles": styles})
}

func (s *Server) handleGetStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")
	// Support grayscale routing: if user_id is provided, use GetForUser
	userID := r.URL.Query().Get("user_id")
	if userID != "" {
		p, ok := s.profiles.GetForUser(slug, userID)
		if !ok {
			response.Err(w, http.StatusNotFound, "not_found", "style not found")
			return
		}
		response.OK(w, p)
		return
	}
	p, ok := s.profiles.Get(slug)
	if !ok {
		response.Err(w, http.StatusNotFound, "not_found", "style not found")
		return
	}
	response.OK(w, p)
}

func (s *Server) handleListTopics(w http.ResponseWriter, r *http.Request) {
	source := r.URL.Query().Get("source")
	page := 1
	pageSize := 20
	if p := r.URL.Query().Get("page"); p != "" {
		if v, err := strconv.Atoi(p); err == nil {
			page = v
		}
	}
	if ps := r.URL.Query().Get("page_size"); ps != "" {
		if v, err := strconv.Atoi(ps); err == nil {
			pageSize = v
		}
	}

	if s.traces != nil {
		topics, total, err := s.traces.ListTopics(r.Context(), source, page, pageSize)
		if err != nil {
			slog.Warn("failed to list topics", "error", err)
		}
		if topics == nil {
			topics = []map[string]interface{}{}
		}
		response.OK(w, map[string]interface{}{"topics": topics, "total": total})
		return
	}

	response.OK(w, map[string]interface{}{
		"topics": []interface{}{},
		"total":  0,
	})
}

func (s *Server) handleCreateTopic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}
	if req.Title == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}

	if s.traces != nil {
		id, err := s.traces.CreateTopic(r.Context(), req.Title, req.Description, "")
		if err != nil {
			slog.Warn("failed to create topic", "error", err)
		} else {
			response.Created(w, map[string]interface{}{
				"id":          id,
				"title":       req.Title,
				"description": req.Description,
				"source":      "user",
			})
			return
		}
	}

	response.Created(w, map[string]interface{}{
		"id":          "topic_placeholder",
		"title":       req.Title,
		"description": req.Description,
		"source":      "user",
	})
}

func (s *Server) handleFeedback(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TraceID  string                   `json:"trace_id"`
		Segments []map[string]interface{} `json:"segments"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	// Reject duplicate feedback — once submitted, it cannot be changed
	if s.traces != nil {
		already, err := s.traces.HasFeedback(r.Context(), req.TraceID)
		if err == nil && already {
			response.Err(w, http.StatusConflict, "already_submitted", "feedback has already been submitted for this trace")
			return
		}
	}

	if s.traces != nil && len(req.Segments) > 0 {
		s.traces.SaveFeedback(r.Context(), req.TraceID, req.Segments)
	}

	// Trigger memory extraction from feedback (async, non-blocking)
	if s.memorySvc != nil && s.memorySvc.IsAvailable() && s.traces != nil {
		go s.triggerFeedbackMemoryExtraction(req.TraceID)
	}

	slog.Info("feedback received", "trace_id", req.TraceID, "segments", len(req.Segments))
	response.OK(w, map[string]interface{}{"received": true})
}

// triggerFeedbackMemoryExtraction retrieves feedback + adoption signals and triggers memory extraction.
func (s *Server) triggerFeedbackMemoryExtraction(traceID string) {
	ctx := context.Background()

	// Get user ID from trace
	userID, err := s.traces.GetTraceUserID(ctx, traceID)
	if err != nil || userID == "" {
		slog.Debug("memory: skip feedback extraction, user not found", "trace_id", traceID, "error", err)
		return
	}

	// Check rollout
	if !s.memorySvc.IsEnabledForUser(userID) {
		return
	}

	// Get feedback segments
	feedback, err := s.traces.GetFeedbackByTrace(ctx, traceID)
	if err != nil {
		slog.Warn("memory: failed to get feedback for extraction", "error", err, "trace_id", traceID)
		return
	}
	if len(feedback) == 0 {
		return
	}

	// Check workbuddy adoption for quality signal
	isAdopted, _ := s.traces.IsTraceAdopted(ctx, traceID)
	completedAt := time.Now()
	signals := memory.CollectSignals(isAdopted, feedback, completedAt)

	// Build extract session — reuse article from DB trace
	trace, err := s.traces.GetTrace(ctx, traceID)
	if err != nil {
		slog.Warn("memory: failed to get trace for extraction", "error", err, "trace_id", traceID)
		return
	}
	article, _ := trace["article"].(string)
	styleSlug, _ := trace["style_slug"].(string)
	mode, _ := trace["mode"].(string)

	session := memory.ExtractSession{
		UserID:    userID,
		TraceID:   traceID,
		Article:   article,
		StyleSlug: styleSlug,
		Mode:      mode,
		Feedback:  feedback,
		Signals:   signals,
	}

	s.memorySvc.Extract(ctx, session)
	slog.Info("memory: feedback-triggered extraction started", "trace_id", traceID, "feedback_count", len(feedback))
}

// ─── WebSocket Handler ───────────────────────────────────

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	// ─── Token Validation ────────────────────────────────
	// Extract token from query parameter or Sec-WebSocket-Protocol header.
	// Browsers can't set custom headers on WebSocket, so query param is the standard approach.
	userID := "anonymous"
	userRole := "user"

	token := r.URL.Query().Get("token")
	if token == "" {
		// Try Sec-WebSocket-Protocol header: client sends "bearer.<jwt>"
		// Browsers can't set custom headers on WS, so subprotocol is an alternative to query param.
		for _, proto := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
			proto = strings.TrimSpace(proto)
			if strings.HasPrefix(proto, "bearer.") {
				token = strings.TrimPrefix(proto, "bearer.")
				break
			}
		}
	}

	if token != "" {
		if payload, err := s.ValidateJWT(token); err == nil {
			userID = payload.Sub
			userRole = payload.Role
		} else if s.cfg.WS.AuthEnabled {
			// Token present but invalid — reject
			http.Error(w, "invalid token", http.StatusUnauthorized)
			return
		}
		// If auth is disabled and token is invalid, fall through as anonymous
	} else if s.cfg.WS.AuthEnabled {
		// No token and auth is required — reject
		http.Error(w, "token required", http.StatusUnauthorized)
		return
	}

	conn, err := ws.Accept(w, r, &ws.AcceptOptions{
		InsecureSkipVerify: true, // Allow cross-origin in dev
	})
	if err != nil {
		slog.Error("websocket accept failed", "error", err)
		if s.metrics != nil {
			s.metrics.WSErrorsTotal.Inc("accept")
		}
		return
	}

	if s.metrics != nil {
		s.metrics.WSConnectionsActive.Inc()
	}

	client := websocket.NewClient(conn)
	go client.WriteLoop()

	// Read loop with message handler
	go func() {
		client.ReadLoop(s.handleClientMessage(client, userID, userRole))
		if s.metrics != nil {
			s.metrics.WSConnectionsActive.Dec()
		}

		// ── Signal disconnect to active agent execution ──
		// When the WebSocket client disconnects, notify the ExecutionContext
		// so the agent can pause gracefully instead of continuing to run.
		if traceID := client.TraceID(); traceID != "" {
			if val, ok := s.sessions.Load(traceID); ok {
				if execCtx, ok := val.(*engine.ExecutionContext); ok {
					execCtx.SignalDisconnect()
					slog.Info("client disconnected, signaled agent",
						"trace_id", traceID,
						"step", execCtx.CurrentStep)
				}
			}
		}
	}()

	// Keep connection alive until ReadLoop exits
	// (ReadLoop will close the send channel, which ends WriteLoop)
}

func (s *Server) handleClientMessage(client *websocket.Client, userID, userRole string) func(*websocket.ClientMessage) {
	return func(msg *websocket.ClientMessage) {
	switch msg.Type {
	case websocket.MsgAgentStart:
		s.handleAgentStart(client, msg.Payload, userID, userRole)
	case websocket.MsgAgentPause:
		s.handleAgentControl(client, msg.Payload, "pause")
	case websocket.MsgAgentResume:
		s.handleAgentControl(client, msg.Payload, "resume")
	case websocket.MsgAgentCancel:
		s.handleAgentControl(client, msg.Payload, "cancel")
	case websocket.MsgAgentConfirm:
		s.handleAgentConfirm(client, msg.Payload)
	case websocket.MsgAgentEdit:
		s.handleAgentEdit(client, msg.Payload)
	case websocket.MsgSessionResume:
		s.handleSessionResume(client, msg.Payload)
	default:
		slog.Warn("unknown message type", "type", msg.Type)
	}
	}
}

func (s *Server) handleAgentStart(client *websocket.Client, payload json.RawMessage, userID, userRole string) {
	var p websocket.AgentStartPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Error("failed to parse agent.start payload", "error", err)
		return
	}

	// Guest write limit: guests can only complete 1 article
	if userRole == "guest" && s.adminRepo != nil && s.adminRepo.DB() != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		var completedCount int
		s.adminRepo.DB().QueryRowContext(ctx, `
			SELECT COUNT(*) FROM agent_traces WHERE user_id = $1 AND status = 'completed'
		`, userID).Scan(&completedCount)
		cancel()

		if completedCount >= 1 {
			client.SendDirect(&websocket.ServerMessage{
				Type: websocket.MsgAgentError,
				Payload: map[string]interface{}{
					"code":    "guest_limit_reached",
					"message": "游客模式仅支持 1 次完整写作，请注册后继续使用",
					"action":  "register_required",
				},
			})
			return
		}
	}

	// ── Per-user concurrent agent limit ──
	if s.cfg.Agent.MaxConcurrentPerUser > 0 {
		userActive := 0
		s.sessions.Range(func(_, v interface{}) bool {
			if ec, ok := v.(*engine.ExecutionContext); ok && ec.UserID == userID {
				if ec.Status == engine.StatusRunning || ec.Status == engine.StatusPaused {
					userActive++
				}
			}
			return true
		})
		if userActive >= s.cfg.Agent.MaxConcurrentPerUser {
			client.SendDirect(&websocket.ServerMessage{
				Type: websocket.MsgAgentError,
				Payload: map[string]interface{}{
					"code":         "concurrent_limit",
					"message":      fmt.Sprintf("已有 %d 个写作任务进行中（上限 %d），请等待完成或取消后再试", userActive, s.cfg.Agent.MaxConcurrentPerUser),
					"limit":        s.cfg.Agent.MaxConcurrentPerUser,
					"active_count": userActive,
				},
			})
			return
		}
	}

	// ── Global concurrent agent limit ──
	if s.cfg.Agent.MaxConcurrent > 0 {
		globalActive := 0
		s.sessions.Range(func(_, v interface{}) bool {
			if ec, ok := v.(*engine.ExecutionContext); ok {
				if ec.Status == engine.StatusRunning || ec.Status == engine.StatusPaused {
					globalActive++
				}
			}
			return true
		})
		if globalActive >= s.cfg.Agent.MaxConcurrent {
			client.SendDirect(&websocket.ServerMessage{
				Type: websocket.MsgAgentError,
				Payload: map[string]interface{}{
					"code":    "server_busy",
					"message": "服务器繁忙，当前并发任务已满，请稍后再试",
				},
			})
			return
		}
	}

	traceID := engine.GenerateTraceID()

	slog.Info("agent started", "trace_id", traceID, "user_id", userID, "role", userRole, "style", func() string {
		if p.Style != "" {
			return p.Style
		}
		return "yinyue"
	}())

	// Register client with trace ID
	s.hub.Register(traceID, client)

	// Resolve LLM client: use dynamic service if available, fall back to static
	var llmClient *tools.LLMClient
	if s.llmSvc != nil {
		llmClient = s.llmSvc.GetClient(context.Background(), p.Model)
	} else {
		llmClient = s.llm
	}

	// Create execution context
	execCtx := engine.NewExecutionContext(traceID, userID, p.Message)
	execCtx.StyleSlug = p.Style
	if execCtx.StyleSlug == "" {
		execCtx.StyleSlug = "yinyue"
	}
	execCtx.Mode = p.Mode
	if execCtx.Mode == "" {
		execCtx.Mode = "auto"
	}
	execCtx.UserMaterials = p.UserMaterials
	execCtx.WordLimit = p.WordLimit
	execCtx.SessionID = traceID // SessionID 用于记忆 dismiss 追踪
	// ConversationID 用于短期记忆分组（同一会话内的消息组成对话历史）
	if p.SessionID != "" {
		execCtx.ConversationID = p.SessionID
	} else {
		execCtx.ConversationID = traceID
	}

	// ── Exit mechanism configuration ──
	execCtx.MaxTokens = s.cfg.Agent.MaxTokens
	execCtx.MaxFixAttempts = s.cfg.Agent.MaxFixAttempts
	execCtx.MaxLLMFails = s.cfg.Agent.CircuitBreakerFails
	execCtx.ConfirmTimeout = s.cfg.Agent.ConfirmTimeout

	// Store session
	s.sessions.Store(traceID, execCtx)

	// Persist trace to database
	if s.traces != nil {
		s.traces.CreateTrace(context.Background(), execCtx)
	}

	// Create editorial task for writing process traceability
	// All intermediate artifacts (search results, research brief, outline,
	// draft, review report, etc.) will be recorded against this task.
	if s.editorialSvc != nil {
		taskOwnerID := userID
		if taskOwnerID == "" || taskOwnerID == "anonymous" {
			taskOwnerID = AdminUserID
		}
		taskTitle := p.Message
		if len([]rune(taskTitle)) > 200 {
			taskTitle = string([]rune(taskTitle)[:200])
		}
		task, err := s.editorialSvc.CreateTask(context.Background(), editorial.CreateTaskInput{
			Title:     taskTitle,
			StyleSlug: execCtx.StyleSlug,
			Priority:  3,
		}, taskOwnerID)
		if err != nil {
			slog.Warn("failed to create editorial task for trace", "error", err, "trace_id", traceID)
		} else if s.traces != nil {
			s.traces.LinkEditorialTask(context.Background(), traceID, task.ID)
			slog.Info("editorial task created for trace", "trace_id", traceID, "task_id", task.ID)
		}
	}

	// Emit agent.created
	client.SendDirect(&websocket.ServerMessage{
		Type: websocket.MsgAgentCreated,
		Payload: websocket.AgentCreatedPayload{
			TraceID: traceID,
			Style:   execCtx.StyleSlug,
			Mode:    execCtx.Mode,
		},
	})

	// Create emitter
	emitter := NewWSEmitter(s.hub, traceID)

	// Load style profile (needed by both pipeline and unified modes)
	var styleProfile *profile.StyleProfile
	if s.profiles != nil {
		if p, ok := s.profiles.Get(execCtx.StyleSlug); ok {
			styleProfile = p
		}
	}

	// Create and run engine
	// Mode selection: payload agent_mode > server config AGENT_MODE
	// "unified" uses LLM-driven ReAct loop, "pipeline" uses fixed steps
	agentMode := s.cfg.Agent.Mode
	if p.AgentMode == "unified" || p.AgentMode == "pipeline" {
		agentMode = p.AgentMode
	}

	var agentRunner interface {
		Run(context.Context, *engine.ExecutionContext) error
	}

	if agentMode == "unified" {
		// Build tool registry with all steps + built-in tools + MCP tools
		registry := s.buildToolRegistry(llmClient, styleProfile, execCtx)

		agentRunner = agent.NewUnifiedAgent(registry, llmClient, emitter)
		slog.Info("using unified agent (ReAct loop)", "trace_id", traceID)
	} else {
		// Pipeline mode: build fixed step array
		var engineSteps []engine.Step

		// ── Short-term memory: load conversation history before intent classification ──
		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			engineSteps = append(engineSteps, steps.NewShortTermMemoryStep(
				s.memorySvc,
				&embedderAdapter{svc: s.memorySvc},
				memory.DefaultDynamicWindowConfig(),
			))
		}

		engineSteps = append(engineSteps,
			steps.NewIntentStep(llmClient),
		)

		// ── Parallel Group: Memory retrieval ∥ Search chain ──
		searchBranch := []engine.Step{
			steps.NewQueryPlanStep(llmClient),
			steps.NewSearchStep(llmClient, s.search),
			steps.NewRelevanceStepWithEmbedding(s.embedding),
			steps.NewCompressStep(llmClient),
		}

		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			memoryBranch := []engine.Step{
				steps.NewMemoryGateStepWithEntityGraph(s.memorySvc, &embedderAdapter{svc: s.memorySvc}),
			}
			engineSteps = append(engineSteps, engine.NewParallelGroup(
				"parallel_pre_write",
				memoryBranch,
				searchBranch,
			))
		} else {
			engineSteps = append(engineSteps, searchBranch...)
		}

		// ── Working memory: incremental summarization after search/relevance ──
		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			engineSteps = append(engineSteps, steps.NewWorkingMemoryStep(
				&workingMemoryLLMAdapter{llm: llmClient},
				memory.DefaultSummarizerConfig(),
			))
		}

		// ChatStep: handles chat intent (skips itself for non-chat intents)
		engineSteps = append(engineSteps, steps.NewChatStep(llmClient))

		if execCtx.Mode == "guided" {
			engineSteps = append(engineSteps, steps.NewOutlineStep(llmClient))
		}

		engineSteps = append(engineSteps,
			steps.NewWriteStepWithSearch(llmClient, styleProfile, s.search),
			s.newPostReviewStepWithLLM(llmClient, styleProfile),
			steps.NewAutoFixStepWithProfile(llmClient, styleProfile),
		)

		// Memory extract: extract patterns after article completion (async)
		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			engineSteps = append(engineSteps, steps.NewMemoryExtractStep(s.memorySvc))
			engineSteps = append(engineSteps, steps.NewShortTermStoreStep(
				s.memorySvc,
				&embedderAdapter{svc: s.memorySvc},
			))
		}

		agentRunner = engine.NewAgentEngine(emitter, engineSteps)
		slog.Info("using fixed pipeline engine", "trace_id", traceID)
	}

	// Run in background
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("agent goroutine panicked",
					"trace_id", traceID,
					"panic", r,
					"stack", string(debug.Stack()),
				)
				// Push the error to the frontend so the UI can recover
				emitter.Error("panic", fmt.Sprintf("内部错误: %v", r), execCtx.CurrentStep)
				execCtx.Status = engine.StatusFailed
				if s.traces != nil {
					s.traces.FailTrace(context.Background(), traceID, fmt.Sprintf("panic: %v", r))
				}
				if s.metrics != nil {
					s.metrics.AgentExecutionsTotal.Inc(execCtx.StyleSlug, "panic")
				}
			}
		}()

		ctx := context.Background()
		if s.cfg.Agent.Timeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.cfg.Agent.Timeout)
			defer cancel()
		}
		start := time.Now()
		if err := agentRunner.Run(ctx, execCtx); err != nil {
			slog.Error("agent execution failed", "trace_id", traceID, "error", err)
			if s.traces != nil {
				s.traces.FailTrace(ctx, traceID, err.Error())
			}
			if s.metrics != nil {
				s.metrics.AgentExecutionsTotal.Inc(execCtx.StyleSlug, "failed")
				s.metrics.AgentDuration.Observe(time.Since(start), execCtx.StyleSlug)
			}
		} else {
			// Persist completed trace
			if s.traces != nil {
				s.traces.CompleteTrace(ctx, execCtx)
			}
			if s.metrics != nil {
				s.metrics.AgentExecutionsTotal.Inc(execCtx.StyleSlug, "completed")
				s.metrics.AgentDuration.Observe(time.Since(start), execCtx.StyleSlug)
			}

			// Record writing process artifacts for traceability
			if s.editorialSvc != nil {
				taskID, _ := s.traces.GetEditorialTaskID(ctx, traceID)
				if taskID != "" {
					recorder := editorial.NewArtifactRecorder(s.editorialSvc.Store())
					if err := recorder.RecordWritingArtifacts(ctx, execCtx, taskID); err != nil {
						slog.Warn("failed to record writing artifacts", "error", err, "trace_id", traceID)
					}
				}
			}
		}
		// Cleanup
		s.hub.Unregister(traceID)
		s.sessions.Delete(traceID)
	}()
}

func (s *Server) handleAgentControl(client *websocket.Client, payload json.RawMessage, action string) {
	var p websocket.AgentControlPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}

	val, ok := s.sessions.Load(p.TraceID)
	if !ok {
		return
	}
	execCtx := val.(*engine.ExecutionContext)

	switch action {
	case "pause":
		execCtx.Pause()
	case "resume":
		execCtx.Resume()
	case "cancel":
		execCtx.Cancel()
	}
}

func (s *Server) handleAgentConfirm(client *websocket.Client, payload json.RawMessage) {
	var p websocket.AgentConfirmPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return
	}

	val, ok := s.sessions.Load(p.TraceID)
	if !ok {
		return
	}
	execCtx := val.(*engine.ExecutionContext)
	execCtx.ConfirmOutline(p.Data)
}

// handleAgentEdit handles an agent.edit message from the client.
// It allows the user to edit the article text (or a segment) while the agent
// is paused or after completion. The edited text replaces the corresponding
// content in the execution context.
func (s *Server) handleAgentEdit(client *websocket.Client, payload json.RawMessage) {
	var p websocket.AgentEditPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Warn("failed to parse agent.edit payload", "error", err)
		client.SendDirect(&websocket.ServerMessage{
			Type: websocket.MsgAgentError,
			Payload: map[string]interface{}{
				"code":    "bad_payload",
				"message": "invalid agent.edit payload",
			},
		})
		return
	}

	val, ok := s.sessions.Load(p.TraceID)
	if !ok {
		client.SendDirect(&websocket.ServerMessage{
			Type: websocket.MsgAgentError,
			Payload: map[string]interface{}{
				"code":      "session_not_found",
				"trace_id":   p.TraceID,
				"message":   "session not found or expired",
			},
		})
		return
	}

	execCtx := val.(*engine.ExecutionContext)

	switch p.Field {
	case "article":
		// Replace the entire article text
		oldArticle := execCtx.Article
		execCtx.Article = p.Value
		slog.Info("article edited by user",
			"trace_id", p.TraceID,
			"old_length", len([]rune(oldArticle)),
			"new_length", len([]rune(p.Value)),
			"reason", p.Reason)

	case "title":
		// Replace the first heading line in the article
		lines := strings.SplitN(execCtx.Article, "\n", 2)
		if len(lines) > 0 {
			lines[0] = "## " + p.Value
			if len(lines) > 1 {
				execCtx.Article = strings.Join(lines, "\n")
			} else {
				execCtx.Article = lines[0]
			}
			slog.Info("title edited by user", "trace_id", p.TraceID, "new_title", p.Value)
		}

	case "paragraph":
		// Replace a specific paragraph by index
		paragraphs := strings.Split(execCtx.Article, "\n\n")
		if p.Index >= 0 && p.Index < len(paragraphs) {
			oldPara := paragraphs[p.Index]
			paragraphs[p.Index] = p.Value
			execCtx.Article = strings.Join(paragraphs, "\n\n")
			slog.Info("paragraph edited by user",
				"trace_id", p.TraceID,
				"index", p.Index,
				"old_length", len([]rune(oldPara)),
				"new_length", len([]rune(p.Value)))
		}

	default:
		client.SendDirect(&websocket.ServerMessage{
			Type: websocket.MsgAgentError,
			Payload: map[string]interface{}{
				"code":    "invalid_field",
				"message": "field must be 'article', 'title', or 'paragraph'",
			},
		})
		return
	}

	// Notify the client that the edit was applied
	client.SendDirect(&websocket.ServerMessage{
		Type: websocket.MsgAgentEdited,
		Payload: map[string]interface{}{
			"trace_id": p.TraceID,
			"field":    p.Field,
			"index":    p.Index,
			"success":  true,
		},
	})
}

// handleSessionResume handles a session.resume message from a reconnecting client.
// It re-associates the new WebSocket client with an existing trace session
// and sends back the current execution state so the UI can recover.
func (s *Server) handleSessionResume(client *websocket.Client, payload json.RawMessage) {
	var p websocket.SessionResumePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		slog.Warn("failed to parse session.resume payload", "error", err)
		return
	}

	traceID := p.TraceID
	if traceID == "" {
		client.SendDirect(&websocket.ServerMessage{
			Type: websocket.MsgSessionResumed,
			Payload: websocket.SessionResumedPayload{
				Status:  "not_found",
				Message: "no trace_id provided",
			},
		})
		return
	}

	// Check if the session still exists in memory
	val, ok := s.sessions.Load(traceID)
	if !ok {
		// Session not found — either completed, cancelled, or expired
		client.SendDirect(&websocket.ServerMessage{
			Type: websocket.MsgSessionResumed,
			Payload: websocket.SessionResumedPayload{
				TraceID: traceID,
				Status:  "not_found",
				Message: "session not found or expired",
			},
		})
		return
	}

	execCtx := val.(*engine.ExecutionContext)

	// Re-associate the client with this trace ID
	s.hub.Register(traceID, client)

	// Determine current status from the execution context
	status := string(execCtx.Status)
	if status == "" {
		status = "running"
	}
	currentStep := ""
	if execCtx.CurrentStep != "" {
		currentStep = string(execCtx.CurrentStep)
	}

	// Find the current running step from history (more reliable)
	for _, stepRecord := range execCtx.StepHistory {
		if stepRecord.Status == "running" {
			currentStep = string(stepRecord.Step)
			break
		}
	}

	// Build the response payload
	respPayload := websocket.SessionResumedPayload{
		TraceID:      traceID,
		Status:       status,
		Step:         currentStep,
		Article:      execCtx.Article,
		ArticleTitle: execCtx.ArticleTitle,
		Style:        execCtx.StyleSlug,
		Mode:         execCtx.Mode,
	}

	// Include outline if awaiting input (paused state)
	if execCtx.Status == engine.StatusPaused && execCtx.Outline != nil {
		respPayload.Outline = execCtx.Outline
	}

	// Include review if completed
	if status == "completed" && execCtx.ReviewResult != nil {
		respPayload.Review = execCtx.ReviewResult
	}

	client.SendDirect(&websocket.ServerMessage{
		Type:    websocket.MsgSessionResumed,
		Payload: respPayload,
	})

	slog.Info("session resumed after reconnect",
		"trace_id", traceID,
		"status", status,
		"step", currentStep)
}

// ─── Middleware ──────────────────────────────────────────

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		slog.Debug("request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		next.ServeHTTP(w, r)
	})
}

// ─── Sensitive Check Adapter ───────────────────────────

// sensitiveCheckAdapter wraps services.SensitiveCheckService to implement engine.SensitiveChecker.
type sensitiveCheckAdapter struct {
	svc *services.SensitiveCheckService
}

func (a *sensitiveCheckAdapter) Check(ctx context.Context, text string) *engine.SensitiveCheckResult {
	if a == nil || a.svc == nil {
		return &engine.SensitiveCheckResult{Passed: true, Summary: "sensitive check not configured"}
	}
	r := a.svc.Check(ctx, text)
	result := &engine.SensitiveCheckResult{
		Passed:  r.Passed,
		Summary: r.Summary,
	}
	for _, h := range r.Hits {
		result.Hits = append(result.Hits, engine.SensitiveHit{
			Word:     h.Word,
			Category: h.Category,
			Severity: h.Severity,
			Action:   h.Action,
			Count:    h.Count,
		})
	}
	return result
}

// newPostReviewStep creates a PostReviewStep with sensitive checking if available.
func (s *Server) newPostReviewStep() engine.Step {
	if s.sensitiveSvc != nil {
		return steps.NewPostReviewStepWithSensitiveCheck(s.llm, &sensitiveCheckAdapter{svc: s.sensitiveSvc})
	}
	return steps.NewPostReviewStep(s.llm)
}

// newPostReviewStepWithLLM creates a PostReviewStep with a specific LLM client and style profile.
func (s *Server) newPostReviewStepWithLLM(llm *tools.LLMClient, p *profile.StyleProfile) engine.Step {
	if s.sensitiveSvc != nil {
		return steps.NewPostReviewStepWithSearchAndJiaozhen(llm, &sensitiveCheckAdapter{svc: s.sensitiveSvc}, p, s.search, s.jiaozhen)
	}
	return steps.NewPostReviewStepWithSearchAndJiaozhen(llm, nil, p, s.search, s.jiaozhen)
}

// buildToolRegistry builds a ToolRegistry containing all pipeline steps as tools,
// built-in function tools, and MCP tools (if MCP servers are configured).
// This is used by the UnifiedAgent to give the LLM planner access to all capabilities.
func (s *Server) buildToolRegistry(llmClient *tools.LLMClient, styleProfile *profile.StyleProfile, execCtx *engine.ExecutionContext) *engine.ToolRegistry {
	registry := engine.NewToolRegistry()

	// ── Macro Tools: pipeline steps wrapped as AgentTool ──
	registry.Register(engine.NewStepTool(
		steps.NewIntentStep(llmClient),
		"意图分类：分析用户输入，判定为 writing/polish/chat/shorten/expand/extract_points",
		false,
	))

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.Register(engine.NewStepTool(
			steps.NewMemoryGateStep(s.memorySvc),
			"记忆门控：检索用户写作偏好记忆，注入到执行上下文",
			false,
		))
	}

	registry.Register(engine.NewStepTool(
		steps.NewChatStep(llmClient),
		"对话回复：处理 chat 意图，直接流式输出回复（非写作模式专用）",
		true, // terminal — article is produced
	))
	registry.Register(engine.NewStepTool(
		steps.NewQueryPlanStep(llmClient),
		"检索规划：LLM 分析用户输入，提取核心话题并生成多角度搜索查询（仅写作模式需要）",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewSearchStep(llmClient, s.search),
		"多源搜索：并发执行知乎/IMA/Tavily/腾讯新闻/微博搜索，返回 20 条结果",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewRelevanceStepWithEmbedding(s.embedding),
		"相关性过滤：对搜索结果评分和语义去重，保留高质量素材",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewCompressStep(llmClient),
		"素材压缩：将搜索结果压缩为结构化研究简报，节省 prompt token",
		false,
	))

	if execCtx.Mode == "guided" {
		registry.Register(engine.NewStepTool(
			steps.NewOutlineStep(llmClient),
			"提纲生成：为引导模式生成文章提纲（标题+要点），等待用户确认",
			false,
		))
	}

	registry.Register(engine.NewStepTool(
		steps.NewWriteStepWithSearch(llmClient, styleProfile, s.search),
		"文章生成：按风格 Profile 生成文章，支持流式输出和 Agent Loop",
		true, // terminal — article is produced
	))
	registry.Register(engine.NewStepTool(
		s.newPostReviewStepWithLLM(llmClient, styleProfile),
		"质量评审：多维度评分（事实/结构/风格/修辞/安全）+ 敏感词检查",
		false,
	))
 registry.Register(engine.NewStepTool(
 steps.NewAutoFixStepWithProfile(llmClient, styleProfile),
 "自动修正：根据评审结果自动修正可修复的问题（含标题独立修正）",
 false,
 ))

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.Register(engine.NewStepTool(
			steps.NewMemoryExtractStep(s.memorySvc),
			"记忆提取：从文章和反馈中异步提取写作偏好模式",
			false,
		))
	}

	// ── MCP Tools: dynamically discovered from MCP servers ──
	if s.mcpRegistry != nil {
		s.mcpRegistry.RegisterTools(registry)
	}

	slog.Info("tool registry built",
		"total_tools", len(registry.All()),
		"mcp_servers", len(s.cfg.MCPServers),
	)

	return registry
}

// handleListActiveModels returns active model configs for the composer (public endpoint).
func (s *Server) handleListActiveModels(w http.ResponseWriter, r *http.Request) {
	if s.adminRepo == nil {
		response.OK(w, map[string]interface{}{"models": []interface{}{}})
		return
	}

	configs, err := s.adminRepo.ListModelConfigs(r.Context())
	if err != nil {
		slog.Warn("failed to list model configs", "error", err)
		response.OK(w, map[string]interface{}{"models": []interface{}{}})
		return
	}

	// Filter to active only and return minimal info
	type modelInfo struct {
		ID          string `json:"id"`
		ModelName   string `json:"model_name"`
		DisplayName string `json:"display_name"`
		Provider    string `json:"provider"`
		IsDefault   bool   `json:"is_default"`
		HasAPIKey   bool   `json:"has_api_key"`
	}

	var models []modelInfo
	for _, c := range configs {
		if c.IsActive {
			models = append(models, modelInfo{
				ID:          c.ID,
				ModelName:   c.ModelName,
				DisplayName: c.DisplayName,
				Provider:    c.Provider,
				IsDefault:   c.IsDefault,
				HasAPIKey:   c.HasAPIKey,
			})
		}
	}
	if models == nil {
		models = []modelInfo{}
	}
	response.OK(w, map[string]interface{}{"models": models})
}
