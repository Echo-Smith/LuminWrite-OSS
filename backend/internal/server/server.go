package server

import (
	"context"
	"database/sql"
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
	wabenchRepo   *database.WABenchRepo
	adminRepo     *database.AdminRepo
	kbRepo        *database.KnowledgeBaseRepo
	evalSvc       *services.EvaluationService
	wabenchSvc    *services.WABenchEvaluationService
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
	mcpServer     *mcp.MCPServer
	toolRegistry  *engine.ToolRegistry
	editorialSvc  *editorial.Service
	editorialHdlr *editorial.Handlers
	redTeamRepo    *database.RedTeamRepo
	evidenceRepo   *database.EvidenceRepo
	sessionEvents  *database.SessionEventRepo

	userStyleStore  *database.UserStyleStore
	styleBuilder   *services.StyleBuilderService

	// Knowledge Manager (operates directly on local PG)
	kbMgr *services.KbManager

	// Self-Evolution service
	evolutionSvc *services.EvolutionService

	// Route metadata registry for /api/v2/admin/routes discovery
	routeReg *routeRegistry

	// Raw database handle (for queries without a dedicated repo)
	db *database.DB
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
		// Configure A/B testing for Responses API if ratio is set
		if cfg.DeepSeek.ResponsesAPIRatio > 0 {
			llm.SetResponsesAPIRatio(cfg.DeepSeek.ResponsesAPIRatio)
		}
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
	var wabenchRepo *database.WABenchRepo
	var adminRepo *database.AdminRepo
	var kbRepo *database.KnowledgeBaseRepo
	var evidenceRepo *database.EvidenceRepo
	var sessionEventRepo *database.SessionEventRepo
	dbAvail := false
	db, err := database.NewPostgres(cfg.Database.URL, cfg.Database.MaxOpenConns, cfg.Database.MaxIdleConns)
	if err != nil {
		slog.Warn("database unavailable, running without persistence", "error", err)
	} else {
		dbAvail = true
		traceRepo = database.NewTraceRepo(db)
		feedbackRepo = database.NewFeedbackRepo(db)
		evalRepo = database.NewEvaluationRepo(db)
		if cfg.Evaluation.WABenchPrivateInputJSONL != "" {
			privateResolver, resolverErr := database.NewJSONLWABenchPrivateInputResolver(cfg.Evaluation.WABenchPrivateInputJSONL)
			if resolverErr != nil {
				return nil, fmt.Errorf("initialize WABench private input resolver: %w", resolverErr)
			}
			wabenchRepo = database.NewWABenchRepo(db, privateResolver)
			slog.Info("WABench private input resolver enabled")
		} else {
			wabenchRepo = database.NewWABenchRepo(db)
		}
		adminRepo = database.NewAdminRepo(db)
		if cfg.Admin.EncryptionKey != "" {
			adminRepo = adminRepo.WithEncryptionKey(crypto.DeriveKey(cfg.Admin.EncryptionKey))
			slog.Info("API key encryption enabled")
		}
		kbRepo = database.NewKnowledgeBaseRepo(db, embeddingClient)
		evidenceRepo = database.NewEvidenceRepo(db)
		sessionEventRepo = database.NewSessionEventRepo(db)
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

		// ── Override DashScope/Embedding config from DB ──
		// Allows admin to configure embedding API key via frontend MCP Keys page.
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "dashscope"); err == nil && key != "" {
			cfg.Dashscope.APIKey = key
			if baseURL != "" {
				cfg.Dashscope.BaseURL = baseURL
			}
			slog.Info("embedding config overridden from DB", "provider", "dashscope")
		}

		// ── Override search engine configs from DB ──
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "tencent"); err == nil && key != "" {
			_ = key // tencent uses CLI, no API key needed; baseURL override only
			if baseURL != "" {
				cfg.Tencent.BaseURL = baseURL
			}
			cfg.Tencent.Enabled = true
			slog.Info("search config overridden from DB", "provider", "tencent")
		}
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "weibo"); err == nil && key != "" {
			_ = key
			if baseURL != "" {
				cfg.Weibo.BaseURL = baseURL
			}
			cfg.Weibo.Enabled = true
			slog.Info("search config overridden from DB", "provider", "weibo")
		}
		if key, baseURL, err := adminRepo.GetAPIKeyValue(ctx, "bing"); err == nil && key != "" {
			_ = key
			if baseURL != "" {
				cfg.Bing.BaseURL = baseURL
			}
			cfg.Bing.Enabled = true
			slog.Info("search config overridden from DB", "provider", "bing")
		}
		// ── Override Jiaozhen (较真事实核查) config from DB ──
		if key, _, err := adminRepo.GetAPIKeyValue(ctx, "jiaozhen"); err == nil && key != "" {
			cfg.Jiaozhen.APIKey = key
			cfg.Jiaozhen.Enabled = true
			slog.Info("jiaozhen config overridden from DB", "provider", "jiaozhen")
		}
		// ── Override Tencent News CLI config from DB ──
		// tencent_news and jiaozhen share the same CLI API key (TENCENT_NEWS_API_KEY).
		// If a DB key exists for "tencent_news", apply it to jiaozhen as well.
		if key, _, err := adminRepo.GetAPIKeyValue(ctx, "tencent_news"); err == nil && key != "" {
			if cfg.Jiaozhen.APIKey == "" {
				cfg.Jiaozhen.APIKey = key
			}
			cfg.Jiaozhen.Enabled = true
			slog.Info("tencent_news config overridden from DB", "provider", "tencent_news")
		}

		// ── Reconfigure embedding client with DB-overridden DashScope config ──
		if cfg.Dashscope.APIKey != "" {
			embeddingClient.Reconfigure(
				cfg.Dashscope.APIKey,
				cfg.Dashscope.BaseURL,
				cfg.Dashscope.Model,
				cfg.Dashscope.Dimension,
			)
			if embeddingClient.IsConfigured() {
				slog.Info("embedding client reconfigured from DB",
					"model", cfg.Dashscope.Model,
					"dimension", cfg.Dashscope.Dimension,
					"base_url", cfg.Dashscope.BaseURL,
				)
			} else {
				slog.Warn("embedding client reconfigured but key is placeholder")
			}
		}
	}

	searchClient := tools.NewSearchClient(
		cfg.Tavily.APIKey, cfg.Tavily.Endpoint, cfg.Tavily.Timeout,
		cfg.Zhihu.Enabled, cfg.Zhihu.BaseURL, cfg.Zhihu.AccessSecret, cfg.Zhihu.Timeout,
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

	// Create LLM service (dynamic client factory with DB-backed model configs)
	// This must be created early so all subsystems (Evaluation, Memory, GraphRAG,
	// StyleBuilder, Editorial) can use the dynamic client instead of static fallback.
	llmSvc := services.NewLLMService(adminRepo, llm, cfg.DeepSeek.Timeout)

	// Resolve the default LLM client from the DB-backed service.
	// If DB has model configs with API keys, this returns a dynamic client;
	// otherwise it falls back to the static env-based llm.
	defaultLLM := llm
	if llmSvc != nil {
		if c := llmSvc.GetDefaultClient(context.Background()); c != nil {
			defaultLLM = c
			slog.Info("using DB-backed LLM client for subsystems")
		}
	}

	// Create evaluation service
	// Uses defaultLLM (DB-backed) instead of static llm
	evalSvc := services.NewEvaluationService(evalRepo, defaultLLM, profileLoader)

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
	// Uses defaultLLM (DB-backed) instead of static llm
	var memorySvc *memsvc.Service
	if dbAvail {
		memorySvc = memsvc.NewService(db, defaultLLM, embeddingClient, sensitiveSvc)
	}

	// Initialize MCP registry — connect to configured MCP servers
	mcpRegistry := mcp.NewRegistry()

	// 1. Connect env-var configured MCP servers (legacy)
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

	// 2. Connect DB-backed MCP servers (admin-managed)
	if dbAvail && adminRepo != nil {
		dbServers, err := adminRepo.ListMCPServers(context.Background())
		if err != nil {
			slog.Warn("failed to load MCP servers from DB", "error", err)
		} else {
			for _, srv := range dbServers {
				if !srv.IsActive {
					continue
				}
				mcpCfg := mcp.MCPClientConfig{
					Name:      srv.Name,
					Transport: srv.Transport,
					Command:   srv.Command,
					Args:      srv.Args,
					Env:       srv.Env,
					URL:       srv.URL,
				}
				if err := mcpRegistry.Connect(context.Background(), mcpCfg); err != nil {
					adminRepo.UpdateMCPServerStatus(context.Background(), srv.ID, "failed", err.Error())
				} else {
					adminRepo.UpdateMCPServerStatus(context.Background(), srv.ID, "connected", "")
				}
			}
		}
	}

	// Initialize ToolRegistry — registry for all tools
	// (Steps + Built-in tools + MCP tools)
	toolRegistry := engine.NewToolRegistry()

	// ── Red-team report repo ──
	var redTeamRepo *database.RedTeamRepo
	if dbAvail {
		redTeamRepo = database.NewRedTeamRepo(db)
	}

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
		wabenchRepo:   wabenchRepo,
		adminRepo:     adminRepo,
		kbRepo:        kbRepo,
		evalSvc:       evalSvc,
		reputationSvc: reputationSvc,
		dbAvail:       dbAvail,
		cronScheduler: cronSched,
		metrics:       NewMetricsRegistry(),
		webauthn:      NewWebAuthnService(cfg.WebAuthn.RPID, cfg.WebAuthn.RPName, cfg.WebAuthn.RPOrigin),
		passkeyChallenges: newPasskeyChallengeStore(func() *sql.DB {
			if dbAvail && db != nil {
				return db.DB // *database.DB embeds *sql.DB
			}
			return nil
		}()), // db may be nil — falls back to in-memory
		sensitiveSvc:  sensitiveSvc,
		jiaozhen:      jiaozhenClient,
		memorySvc:     memorySvc,
		mcpRegistry:   mcpRegistry,
		toolRegistry:  toolRegistry,
		redTeamRepo:   redTeamRepo,
		evidenceRepo:  evidenceRepo,
		sessionEvents: sessionEventRepo,
	}

	// ── User custom styles & AI builder ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		s.userStyleStore = database.NewUserStyleStore(db)
		s.db = db
	}
	if llm != nil {
		s.styleBuilder = services.NewStyleBuilderService(defaultLLM)
		// Wire LLM metrics (Prometheus instrumentation)
		llm.SetMetricsRecorder(s.metrics)
	}

	// ── Self-Evolution service ──
	if dbAvail && evalRepo != nil {
		s.evolutionSvc = services.NewEvolutionService(evalRepo, s.profiles)
		if s.db != nil {
			s.evolutionSvc.SetDB(s.db)
		}
		slog.Info("self-evolution service initialized")
	}

	// ── Knowledge Manager (operates directly on local PG) ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		s.kbMgr = services.NewKbManager(adminRepo.DB().DB, embeddingClient)

		// Wire GraphRAG — entity extraction + relation graph (replaces WeKnora's graph pipeline)
	if llm != nil {
		// Use defaultLLM (DB-backed) for GraphRAG instead of static llm
		graphRAG := services.NewGraphRAGManager(adminRepo.DB().DB, embeddingClient, defaultLLM)
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

	// KB is now a standalone tool (search_knowledge), not mixed into SearchClient.
	// The KbSearchAdapter is passed to Harness directly via NewHarness().
	slog.Info("local knowledge base initialized (standalone search_knowledge tool)")
	} else {
		slog.Warn("knowledge manager skipped: database not available")
	}

	// WABench V2 shadow runner uses the same Harness and runtime dependencies
	// as the user-facing writing path; it does not bypass retrieval or tools.
	if wabenchRepo != nil && defaultLLM != nil {
		wabenchAdapter := services.NewHarnessWABenchExecutorWithResolver(
			s.llmSvc,
			s.search,
			services.NewKbSearchAdapter(s.kbMgr),
			s.profiles,
			s.userStyleStore,
			&harnessSessionStore{svc: s.memorySvc},
			s.traces,
		)
		s.wabenchSvc = services.NewWABenchEvaluationService(
			wabenchRepo,
			wabenchAdapter,
			services.NewLLMWABenchJudgeWithResolver(s.llmSvc),
		)
		slog.Info("WABench V2 shadow runner initialized", "adapter", services.LuminbuddyV2AdapterID)
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
			edSvc.Orchestrator().RegisterExecutor(editorial.NewResearchAgentExecutor(defaultLLM, searchClient, embeddingClient, edStore))
			if defaultProfile, ok := profileLoader.Get("yinyue"); ok {
				edSvc.Orchestrator().RegisterExecutor(editorial.NewWritingAgentExecutor(defaultLLM, defaultProfile, searchClient, edStore))
				edSvc.Orchestrator().RegisterExecutor(editorial.NewReviewAgentExecutor(defaultLLM, defaultProfile, searchClient, edStore))
			}

			// 初始化对照实验运行器
			expRunner := editorial.NewExperimentRunner(
				edStore, defaultLLM, searchClient, embeddingClient,
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

	// ── In-Process MCP Server ──
	if cfg.MCPServer.Enabled {
		s.initMCPServer(cfg)
	}

	return s, nil
}

// Router returns the HTTP router with all routes registered.
func (s *Server) Router() http.Handler {
	s.routeReg = newRouteRegistry()
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

	// Memory Files (Markdown memory layer)
	r.With(s.jwtAuthMiddleware).Get("/memories/file", s.handleGetMemoryFile)
	r.With(s.jwtAuthMiddleware).Post("/memories/file/export", s.handleExportMemoryFile)
	r.With(s.jwtAuthMiddleware).Post("/memories/file/import", s.handleImportMemoryFile)
	r.With(s.jwtAuthMiddleware).Get("/memories/global", s.handleGetGlobalMemory)
	r.With(s.jwtAuthMiddleware).Put("/memories/global", s.handleUpdateGlobalMemory)

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
		r.With(s.jwtAuthMiddleware).Get("/materials/{id}", s.handleUserMaterialGet)
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

		// Red-Team Security Evaluation
		r.Get("/evaluation/redteam/cases", s.handleRedTeamCases)
		r.Post("/evaluation/redteam/seed", s.handleRedTeamSeed)
		r.Post("/evaluation/redteam/run", s.handleRedTeamRun)
		r.Get("/evaluation/redteam/reports", s.handleListRedTeamReports)
		r.Get("/evaluation/redteam/reports/{id}", s.handleGetRedTeamReport)

		// Tool Graph — dependency visualization
		r.Get("/tools/graph", s.handleToolGraph)

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
		// register/begin is public — allows registration from the login page before JWT is obtained.
		// The handler resolves user_id from the request body (or falls back to AdminUserID).
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
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/events", s.handleGetSessionEvents)
		r.With(s.jwtAuthMiddleware).Put("/sessions/{traceId}/article", s.handleUpdateSessionArticle)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/versions", s.handleListArticleVersions)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/versions/{versionId}", s.handleGetArticleVersion)
		r.With(s.jwtAuthMiddleware).Post("/auth/change-password", s.handleChangePassword)
		r.With(s.jwtAuthMiddleware).Post("/auth/update-profile", s.handleUpdateProfile)

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

            // A/B Test Metrics (Responses API vs Chat Completions)
            r.Get("/ab-metrics", s.handleAdminABMetrics)

            // Cron Jobs
            r.Get("/cron-jobs", s.handleAdminListCronJobs)
            r.Post("/cron-jobs", s.handleAdminCreateCronJob)
            r.Put("/cron-jobs/{id}", s.handleAdminUpdateCronJob)
            r.Delete("/cron-jobs/{id}", s.handleAdminDeleteCronJob)
            r.Post("/cron-jobs/{id}/run", s.handleAdminRunCronJob)

            // Knowledge Base Admin
            r.Post("/kb/generate-embeddings", s.handleKBGenerateEmbeddings)
            r.Post("/kb/rechunk", s.handleKBRechunk)
            r.Post("/kb/reimport", s.handleKBReimport)

            // MCP Server Admin
            r.Get("/mcp/status", s.handleAdminMCPStatus)
            r.Get("/mcp/tools", s.handleAdminMCPTools)
            r.Get("/mcp/export", s.handleAdminMCPExport)
            // MCP Server CRUD (DB-backed external server management)
            r.Get("/mcp/servers", s.handleAdminListMCPServers)
            r.Post("/mcp/servers", s.handleAdminCreateMCPServer)
            r.Put("/mcp/servers/{id}", s.handleAdminUpdateMCPServer)
            r.Delete("/mcp/servers/{id}", s.handleAdminDeleteMCPServer)
            r.Post("/mcp/servers/{id}/reconnect", s.handleAdminReconnectMCPServer)

            // Tool Plugin Management (hot-pluggable tool sets)
            r.Get("/tool-plugins", s.handleAdminListToolPlugins)
            r.Post("/tool-plugins", s.handleAdminCreateToolPlugin)
            r.Get("/tool-plugins/{name}", s.handleAdminGetToolPlugin)
            r.Delete("/tool-plugins/{name}", s.handleAdminDeleteToolPlugin)

            // SSE Notifications (admin test notification)
            r.Post("/sse/notify", s.handleSSESendNotification)

            // Audit Logs
            r.Get("/audit-logs", s.handleAdminListAuditLogs)

            // Security Audit (Prompt Injection Interception Stats)
            r.Get("/security/audit", s.handleAdminSecurityAudit)

            // Self-Evolution Candidate Management
            r.Get("/evolution/candidates", s.handleAdminListEvolutionCandidates)
            r.Post("/evolution/candidates/{id}/approve", s.handleAdminApproveEvolutionCandidate)
            r.Post("/evolution/candidates/{id}/reject", s.handleAdminRejectEvolutionCandidate)
            r.Post("/evolution/candidates/{id}/canary", s.handleAdminEnableCanaryRollout)

            // Batch Operations
            r.Post("/models/batch", s.handleAdminBatchModels)
            r.Post("/api-keys/batch", s.handleAdminBatchAPIKeys)
            r.Post("/cron-jobs/batch", s.handleAdminBatchCronJobs)

            // Evidence System
            r.Get("/evidence/{traceId}", s.handleAdminGetEvidence)

			// WritingAgentBench V2 shadow evaluation
			r.Put("/evaluation/wabench/candidates/{id}", s.handleAdminUpsertWABenchCandidate)
			r.Get("/evaluation/wabench/overview", s.handleAdminWABenchOverview)
			r.Get("/evaluation/wabench/suites", s.handleAdminWABenchSuites)
			r.Get("/evaluation/wabench/candidates", s.handleAdminWABenchCandidates)
			r.Get("/evaluation/wabench/runs", s.handleAdminWABenchRuns)
			r.Post("/evaluation/wabench/runs", s.handleAdminCreateWABenchRun)
			r.Get("/evaluation/wabench/runs/{id}", s.handleAdminGetWABenchRun)
			r.Get("/evaluation/wabench/runs/{id}/bundle", s.handleAdminGetWABenchRunBundle)
			r.Get("/evaluation/wabench/reviews", s.handleAdminWABenchReviews)
			r.Get("/evaluation/wabench/reviews/template.xlsx", s.handleAdminWABenchReviewTemplate)
			r.Post("/evaluation/wabench/reviews/import", s.handleAdminImportWABenchReviews)
			r.Get("/evaluation/wabench/badcases", s.handleAdminWABenchBadcases)
			r.Get("/evaluation/wabench/releases", s.handleAdminWABenchReleases)
			r.Post("/evaluation/wabench/red-team/seed", s.handleAdminSeedWABenchRedTeam)

            // Route Discovery — list all registered API routes
            r.Get("/routes", s.handleAdminRoutes)
        })
    })

	// Walk chi router to discover all routes and populate metadata
	s.registerRoutesFromChi(r)

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
		if s.mcpServer != nil {
			s.mcpServer.Close()
		}
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

	// Start memory file watcher (hot-reload of Markdown memory files)
	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		s.memorySvc.StartFileWatch(ctx)
	}

	// Start in-process MCP server (HTTP mode)
	if s.mcpServer != nil {
		s.startMCPServerHTTP()
	}

	slog.Info("server starting", "addr", s.cfg.ListenAddr())
	return srv.ListenAndServe()
}

// ─── Handlers ────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
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
	execCtx.TopicURL = p.TopicURL
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

	// Create emitter (wrapped with event logging for session replay)
	baseEmitter := NewWSEmitter(s.hub, traceID)
	emitter := NewLoggingEmitter(baseEmitter, s.sessionEvents, traceID, EventLogCoarse)

	// Load style profile (needed by both pipeline and harness modes)
	var styleProfile *profile.StyleProfile
	if s.profiles != nil {
		if p, ok := s.profiles.Get(execCtx.StyleSlug); ok {
			styleProfile = p
		}
	}

	// Mode selection: payload agent_mode > server config AGENT_MODE
	// "harness" uses Harness-LLM single-layer continuous session (架构 C)
	// "pipeline" uses fixed steps
	// "editorial" uses editorial orchestrator (选题→研究→写作→审校)
	agentMode := s.cfg.Agent.Mode
	if p.AgentMode == "harness" || p.AgentMode == "pipeline" || p.AgentMode == "editorial" {
		agentMode = p.AgentMode
	}

	// Store agent mode so resume can rebuild the correct runner
	execCtx.AgentMode = agentMode

	var agentRunner interface {
		Run(context.Context, *engine.ExecutionContext) error
	}

	// ── Editorial mode: 启动编辑部编排器 (选题→研究→写作→审校) ──
	if agentMode == "editorial" && s.editorialSvc != nil {
		// 1. 创建编辑部任务
		edTask, err := s.editorialSvc.CreateTask(context.Background(), editorial.CreateTaskInput{
			Title:       p.Message,
			Description: "编辑部模式写作",
			StyleSlug:   execCtx.StyleSlug,
			TokenBudget: 300000,
			Priority:    3,
		}, userID)
		if err != nil {
			slog.Error("editorial: failed to create task", "error", err, "trace_id", traceID)
			client.Send(&websocket.ServerMessage{
				Type:    "error",
				Payload: map[string]string{"message": "failed to create editorial task"},
			})
			return
		}

		// 2. 创建选题卡 Artifact 并自动批准
		topicCardContent, _ := json.Marshal(map[string]interface{}{
			"title":       p.Message,
			"description": "编辑部模式写作",
			"style_slug":  execCtx.StyleSlug,
		})
		topicCard, err := s.editorialSvc.SubmitArtifact(context.Background(), edTask.ID, editorial.SubmitArtifactInput{
			Type:       editorial.ArtifactTopicCard,
			Content:    string(topicCardContent),
			ProducedBy: "human",
		})
		if err != nil {
			slog.Error("editorial: failed to create topic card", "error", err, "task_id", edTask.ID)
		} else {
			s.editorialSvc.ReviewArtifact(context.Background(), topicCard.ID, editorial.ReviewArtifactInput{
				Status:     editorial.ArtifactStatusApproved,
				ReviewerID: "system",
				ReviewNote: "编辑部模式自动批准",
			})
		}

		// 3. 推进状态链：draft → pending_approval → research
		// 这将触发研究 Agent，然后自动流经 writing → review → pending_publish
		if err := s.editorialSvc.AdvanceTask(context.Background(), edTask.ID, editorial.AdvanceTaskInput{
			TargetStatus: editorial.StatusPendingApproval,
			DecidedBy:    userID,
			Rationale:   "编辑部模式自动提交审批",
		}); err != nil {
			slog.Error("editorial: failed to advance to pending_approval", "error", err, "task_id", edTask.ID)
		}
		if err := s.editorialSvc.AdvanceTask(context.Background(), edTask.ID, editorial.AdvanceTaskInput{
			TargetStatus: editorial.StatusResearch,
			DecidedBy:    userID,
			Rationale:   "编辑部模式自动批准立项",
		}); err != nil {
			slog.Error("editorial: failed to advance to research", "error", err, "task_id", edTask.ID)
		}

		// 关联 trace 与 editorial task
		if s.traces != nil {
			s.traces.LinkEditorialTask(context.Background(), traceID, edTask.ID)
		}

		slog.Info("editorial mode started", "trace_id", traceID, "task_id", edTask.ID)

		// Editorial orchestrator 通过 editorialWSEmitter 自动把事件转发到 WebSocket
		// 前端通过 editorial.event 消息接收进度
		return
	}

	// ── Harness/Pipeline mode ──
	if agentMode == "harness" {
		// 架构 C: Harness-LLM 单层持续会话
		writingSession := agent.NewWritingSession(execCtx.ConversationID, userID, execCtx.StyleSlug)
		writingSession.UserMaterials = execCtx.UserMaterials
		if execCtx.TopicURL != "" {
			writingSession.UserMaterials = append(writingSession.UserMaterials, "选题链接: "+execCtx.TopicURL)
		}
		if execCtx.MemoryContext != nil {
			writingSession.MemoryContext = execCtx.MemoryContext
		}

		h := agent.NewHarness(
			llmClient, s.search, services.NewKbSearchAdapter(s.kbMgr), styleProfile,
			&harnessSessionStore{svc: s.memorySvc},
			emitter,
		)
		agentRunner = &harnessRunner{harness: h, session: writingSession}
		slog.Info("using harness agent (单层持续会话)", "trace_id", traceID, "conversation_id", execCtx.ConversationID)
	} else if agentMode == "pipeline" {
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
			steps.NewWriteStepWithKB(llmClient, styleProfile, s.search, services.NewKbSearchAdapter(s.kbMgr)),
			s.newPostReviewStepWithLLM(llmClient, styleProfile),
			steps.NewAutoFixStepWithProfile(llmClient, styleProfile),
			s.newPostReviewStepWithLLM(llmClient, styleProfile), // re-review after fix
		)

		// Memory extract: extract patterns after article completion (async)
		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			engineSteps = append(engineSteps, steps.NewMemoryExtractStep(s.memorySvc))
			engineSteps = append(engineSteps, steps.NewShortTermStoreStep(
				s.memorySvc,
				&embedderAdapter{svc: s.memorySvc},
			))
		}

		ae := engine.NewAgentEngine(emitter, engineSteps)
		// Wire step hook for real-time trace persistence (per-step, not just at completion)
		if s.traces != nil {
			ae.SetStepHook(func(ctx context.Context, execCtx *engine.ExecutionContext) {
				s.traces.UpdateTraceStep(ctx, execCtx)
			})
		}
		agentRunner = ae
		slog.Info("using fixed pipeline engine", "trace_id", traceID)
	} else {
		// Default fallback: harness mode (架构 C)
		writingSession := agent.NewWritingSession(execCtx.ConversationID, userID, execCtx.StyleSlug)
		writingSession.UserMaterials = execCtx.UserMaterials
		if execCtx.TopicURL != "" {
			writingSession.UserMaterials = append(writingSession.UserMaterials, "选题链接: "+execCtx.TopicURL)
		}

		h := agent.NewHarness(
			llmClient, s.search, services.NewKbSearchAdapter(s.kbMgr), styleProfile,
			&harnessSessionStore{svc: s.memorySvc},
			emitter,
		)
		agentRunner = &harnessRunner{harness: h, session: writingSession}
		slog.Info("using harness agent (default fallback)", "trace_id", traceID)
	}

	// Run in background
	go s.runAgent(agentRunner, execCtx, emitter, traceID, styleProfile)
}

// runAgent runs the agent in a background goroutine with pause-aware cleanup.
//
// Cloud-server model: when the agent pauses due to client disconnect,
// the goroutine exits (releasing resources) but the session stays in
// memory for a TTL period. If the client reconnects and resumes within
// the TTL, a new goroutine is started to continue from the pause point.
// If the TTL expires, the session is cleaned up and the paused state
// is persisted to the database (read-only recovery for late reconnects).
//
// Parameters:
//   - agentRunner: the pipeline or harness agent
//   - execCtx: the shared execution context
//   - emitter: the event emitter (LoggingEmitter wrapping WSEmitter)
//   - traceID: the trace identifier
//   - styleProfile: the style profile (needed to rebuild the runner on resume)
func (s *Server) runAgent(
	agentRunner interface {
		Run(context.Context, *engine.ExecutionContext) error
	},
	execCtx *engine.ExecutionContext,
	emitter engine.EventEmitter,
	traceID string,
	styleProfile *profile.StyleProfile,
) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("agent goroutine panicked",
				"trace_id", traceID,
				"panic", r,
				"stack", string(debug.Stack()),
			)
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
	err := agentRunner.Run(ctx, execCtx)

	// ── Paused: persist paused state, keep session with TTL ──
	if err == nil && execCtx.Status == engine.StatusPaused {
		slog.Info("agent paused (client disconnect), keeping session with TTL",
			"trace_id", traceID,
			"step", execCtx.CurrentStep,
		)

		// Persist paused state to DB (not completed_at — remains in-progress)
		if s.traces != nil {
			if perr := s.traces.PauseTrace(context.Background(), execCtx); perr != nil {
				slog.Warn("failed to persist paused trace", "error", perr, "trace_id", traceID)
			}
		}

		// Unregister the WS hub (no client connected now), but keep session
		s.hub.Unregister(traceID)

		// Schedule TTL-based cleanup
		ttl := s.cfg.Agent.PausedSessionTTL
		if ttl <= 0 {
			ttl = 2 * time.Minute
		}
		time.AfterFunc(ttl, func() {
			// Only clean up if the session is still paused (not resumed)
			if val, ok := s.sessions.Load(traceID); ok {
				if ec, ok := val.(*engine.ExecutionContext); ok && ec.Status == engine.StatusPaused {
					slog.Info("paused session TTL expired, cleaning up",
						"trace_id", traceID,
						"step", ec.CurrentStep,
					)
					s.sessions.Delete(traceID)
				}
			}
		})
		return
	}

	// ── Failed ──
	if err != nil {
		slog.Error("agent execution failed", "trace_id", traceID, "error", err)
		if s.traces != nil {
			s.traces.FailTrace(ctx, traceID, err.Error())
		}
		if s.metrics != nil {
			s.metrics.AgentExecutionsTotal.Inc(execCtx.StyleSlug, "failed")
			s.metrics.AgentDuration.Observe(time.Since(start), execCtx.StyleSlug)
		}
	} else {
		// ── Completed successfully ──
		if s.traces != nil {
			s.traces.CompleteTrace(ctx, execCtx)
		}

		// Broadcast article:completed via SSE — only when an actual article was produced
		// (skip chat/shorten/expand intents that don't generate a full article)
		if s.sseHub != nil && execCtx.Article != "" {
			topic := ""
			if execCtx.WritingTask != nil {
				topic = execCtx.WritingTask.Topic
			}
			s.sseHub.Broadcast(&SSEEvent{
				Event: "article:completed",
				Data: map[string]interface{}{
					"trace_id":      traceID,
					"user_id":       execCtx.UserID,
					"style_slug":    execCtx.StyleSlug,
					"topic":         topic,
					"article_title": execCtx.ArticleTitle,
					"timestamp":     time.Now().Format(time.RFC3339),
				},
			})
		}
		if s.metrics != nil {
			s.metrics.AgentExecutionsTotal.Inc(execCtx.StyleSlug, "completed")
			s.metrics.AgentDuration.Observe(time.Since(start), execCtx.StyleSlug)
		}

		// Record search results as evidence for traceability
		if s.evidenceRepo != nil && len(execCtx.SearchResults) > 0 {
			if err := s.evidenceRepo.SaveSearchEvidence(ctx, traceID, execCtx.SearchResults); err != nil {
				slog.Warn("failed to save search evidence", "error", err, "trace_id", traceID)
			}
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

	// Cleanup: unregister hub and delete session
	s.hub.Unregister(traceID)
	s.sessions.Delete(traceID)
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
		// If the session was paused due to client disconnect, the goroutine
		// has already exited. We need to reconnect the channels and start
		// a new goroutine to continue from where it left off.
		if execCtx.Status == engine.StatusPaused {
			slog.Info("resuming paused session after reconnect",
				"trace_id", p.TraceID,
				"step", execCtx.CurrentStep,
			)

			// Reconnect: reset disconnect channel and control channels
			execCtx.Reconnect()
			execCtx.ResumeFromPause()

			// Rebuild the agent runner and emitter (wrapped with event logging)
			baseEmitter := NewWSEmitter(s.hub, p.TraceID)
			emitter := NewLoggingEmitter(baseEmitter, s.sessionEvents, p.TraceID, EventLogCoarse)

			// Resolve LLM client
			var llmClient *tools.LLMClient
			if s.llmSvc != nil {
				llmClient = s.llmSvc.GetClient(context.Background(), "")
			} else {
				llmClient = s.llm
			}

			// Load style profile
			var styleProfile *profile.StyleProfile
			if s.profiles != nil {
				if sp, ok := s.profiles.Get(execCtx.StyleSlug); ok {
					styleProfile = sp
				}
			}

			// Rebuild agent runner based on mode
			var agentRunner interface {
				Run(context.Context, *engine.ExecutionContext) error
			}

			// Use the original agent mode from the execution context,
			// falling back to server config if not set
			resumeMode := execCtx.AgentMode
			if resumeMode == "" {
				resumeMode = s.cfg.Agent.Mode
			}

			// Editorial mode doesn't support resume — it's driven by the
			// orchestrator state machine, not a long-lived Run() goroutine.
			if resumeMode == "editorial" {
				slog.Info("editorial mode: no resume needed, orchestrator drives the flow", "trace_id", p.TraceID)
				return
			}

			if resumeMode == "harness" {
				writingSession := agent.NewWritingSession(execCtx.ConversationID, execCtx.UserID, execCtx.StyleSlug)
				writingSession.UserMaterials = execCtx.UserMaterials
				if execCtx.TopicURL != "" {
					writingSession.UserMaterials = append(writingSession.UserMaterials, "选题链接: "+execCtx.TopicURL)
				}
				if execCtx.MemoryContext != nil {
					writingSession.MemoryContext = execCtx.MemoryContext
				}
				// 恢复已有文章和标题（断线重连后继续修改已有文章）
				if execCtx.Article != "" {
					writingSession.CurrentArticle = execCtx.Article
				}
				if execCtx.ArticleTitle != "" {
					writingSession.ArticleTitle = execCtx.ArticleTitle
				}

				h := agent.NewHarness(
					llmClient, s.search, services.NewKbSearchAdapter(s.kbMgr), styleProfile,
					&harnessSessionStore{svc: s.memorySvc},
					emitter,
				)
				agentRunner = &harnessRunner{harness: h, session: writingSession}
			} else {
				var engineSteps []engine.Step
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
				if s.memorySvc != nil && s.memorySvc.IsAvailable() {
					engineSteps = append(engineSteps, steps.NewWorkingMemoryStep(
						&workingMemoryLLMAdapter{llm: llmClient},
						memory.DefaultSummarizerConfig(),
					))
				}
				engineSteps = append(engineSteps, steps.NewChatStep(llmClient))
				if execCtx.Mode == "guided" {
					engineSteps = append(engineSteps, steps.NewOutlineStep(llmClient))
				}
				engineSteps = append(engineSteps,
					steps.NewWriteStepWithKB(llmClient, styleProfile, s.search, services.NewKbSearchAdapter(s.kbMgr)),
					s.newPostReviewStepWithLLM(llmClient, styleProfile),
					steps.NewAutoFixStepWithProfile(llmClient, styleProfile),
					s.newPostReviewStepWithLLM(llmClient, styleProfile),
				)
				if s.memorySvc != nil && s.memorySvc.IsAvailable() {
					engineSteps = append(engineSteps, steps.NewMemoryExtractStep(s.memorySvc))
					engineSteps = append(engineSteps, steps.NewShortTermStoreStep(
						s.memorySvc,
						&embedderAdapter{svc: s.memorySvc},
					))
				}
				ae := engine.NewAgentEngine(emitter, engineSteps)
				if s.traces != nil {
					ae.SetStepHook(func(ctx context.Context, execCtx *engine.ExecutionContext) {
						s.traces.UpdateTraceStep(ctx, execCtx)
					})
				}
				agentRunner = ae
			}

			// Start new goroutine to continue execution
			go s.runAgent(agentRunner, execCtx, emitter, p.TraceID, styleProfile)

			// Emit resumed event
			emitter.Resumed(execCtx.CurrentStep)
			return
		}

		// Normal resume (agent is still running, just paused by user)
		// Only send resume signal if the agent is actually in a paused state.
		// If the agent is running (e.g. waiting for outline confirmation via await_input),
		// the resume signal is irrelevant — the agent is not paused, it's waiting for
		// a confirm signal via agent.confirm, not agent.resume.
		if execCtx.Status == engine.StatusPaused {
			execCtx.Resume()
		} else {
			slog.Debug("resume requested but agent is not paused, ignoring",
				"trace_id", p.TraceID,
				"status", execCtx.Status)
		}
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
//
// Resolution strategy:
//  1. Check in-memory sessions (active or recently paused)
//  2. Fall back to database trace records (completed/failed sessions)
//  3. Return "not_found" if neither has the trace
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

	// ── 1. Check in-memory sessions ──
	val, ok := s.sessions.Load(traceID)
	if ok {
		execCtx := val.(*engine.ExecutionContext)

		// Re-associate the client with this trace ID
		s.hub.Register(traceID, client)

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

		// Build the response payload with full state
		respPayload := websocket.SessionResumedPayload{
			TraceID:          traceID,
			Status:           status,
			Step:             currentStep,
			Article:          execCtx.Article,
			ArticleTitle:     execCtx.ArticleTitle,
			Style:            execCtx.StyleSlug,
			Mode:             execCtx.Mode,
			ConversationID:   execCtx.ConversationID,
			UserInput:        execCtx.UserInput,
			ReasoningContent: execCtx.ReasoningContent,
		}

		// Convert step history to JSON-serializable format
		if len(execCtx.StepHistory) > 0 {
			respPayload.StepHistory = execCtx.StepHistory
		}

		// Include outline if awaiting input (paused state or running with outline pending)
		if (execCtx.Status == engine.StatusPaused || execCtx.Status == engine.StatusRunning) && execCtx.Outline != nil {
			respPayload.Outline = execCtx.Outline
		}

		// Mark as resumable only if the session is paused due to client disconnect.
		// If the session is running (e.g. waiting for outline confirmation), the client
		// should use agent.confirm, not agent.resume.
		if execCtx.Status == engine.StatusPaused {
			respPayload.CanResume = true
		}

		// Include review if completed
		if status == "completed" && execCtx.ReviewResult != nil {
			respPayload.Review = execCtx.ReviewResult
		}

		client.SendDirect(&websocket.ServerMessage{
			Type:    websocket.MsgSessionResumed,
			Payload: respPayload,
		})

		slog.Info("session resumed from memory",
			"trace_id", traceID,
			"status", status,
			"step", currentStep)
		return
	}

	// ── 2. Fall back to database trace record ──
	if s.traces != nil {
		trace, err := s.traces.GetTrace(context.Background(), traceID)
		if err == nil && trace != nil {
			status, _ := trace["status"].(string)
			if status == "" {
				status = "completed"
			}

			respPayload := websocket.SessionResumedPayload{
				TraceID:        traceID,
				Status:         status,
				Article:        getStr(trace, "article"),
				ArticleTitle:   getStr(trace, "article_title"),
				Style:          getStr(trace, "style_slug"),
				Mode:           getStr(trace, "mode"),
				UserInput:      getStr(trace, "user_input"),
				StepHistory:    trace["step_history"],
				Review:         trace["review"],
				ReasoningContent: getStr(trace, "reasoning_content"),
				// ConversationID: not stored in DB trace, client can use traceID
				ConversationID: traceID,
			}

			client.SendDirect(&websocket.ServerMessage{
				Type:    websocket.MsgSessionResumed,
				Payload: respPayload,
			})

			slog.Info("session resumed from database",
				"trace_id", traceID,
				"status", status)
			return
		}
	}

	// ── 3. Not found ──
	client.SendDirect(&websocket.ServerMessage{
		Type: websocket.MsgSessionResumed,
		Payload: websocket.SessionResumedPayload{
			TraceID: traceID,
			Status:  "not_found",
			Message: "session not found or expired",
		},
	})

	slog.Info("session resume: not found",
		"trace_id", traceID)
}

// getStr safely extracts a string from a map[string]interface{}.
func getStr(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
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
// This is used by the Harness to give the LLM planner access to all capabilities.
//
// Each tool is registered with a ToolDescriptor that declares its dependencies,
// repeatability, terminal flag, and category. This replaces the former hardcoded
// nonRepeatableTools and toolDependencies maps (deleted with the old ReAct agent).
func (s *Server) buildToolRegistry(llmClient *tools.LLMClient, styleProfile *profile.StyleProfile, execCtx *engine.ExecutionContext) *engine.ToolRegistry {
	registry := engine.NewToolRegistry()

	// ── Macro Tools: pipeline steps wrapped as AgentTool ──
	// Each tool gets a ToolDescriptor with:
	//   DependsOn: tools that must execute first
	//   Repeatable: false = only once per session
	//   Terminal:   true = can end the agent loop
	//   Category:   for dependency graph visualization

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewIntentStep(llmClient),
			"意图分类：分析用户输入，判定为 writing/polish/chat/shorten/expand/extract_points",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable:  false,
			Terminal:    false,
			Category:    "planning",
		},
	)

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.RegisterWithDescriptor(
			engine.NewStepTool(
				steps.NewMemoryGateStep(s.memorySvc),
				"记忆门控：检索用户写作偏好记忆，注入到执行上下文",
				false,
			),
			engine.ToolDescriptor{
				DependsOn:  nil,
				Repeatable:  false,
				Terminal:    false,
				Category:    "memory",
			},
		)
	}

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewChatStep(llmClient),
			"对话回复：处理 chat 意图，直接流式输出回复（非写作模式专用）",
			true, // terminal — article is produced
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable:  true,
			Terminal:    true,
			Category:    "writing",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewQueryPlanStep(llmClient),
			"检索规划：LLM 分析用户输入，提取核心话题并生成多角度搜索查询（仅写作模式需要）",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable:  false,
			Terminal:    false,
			Category:    "retrieval",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewSearchStep(llmClient, s.search),
			"多源搜索：并发执行知乎/Tavily/腾讯新闻/微博/本地知识库搜索，返回 20 条结果",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  []string{"query_plan"},
			Repeatable:  true,
			Terminal:    false,
			Category:    "retrieval",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewRelevanceStepWithEmbedding(s.embedding),
			"相关性过滤：对搜索结果评分和语义去重，保留高质量素材",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  []string{"search"},
			Repeatable:  true,
			Terminal:    false,
			Category:    "retrieval",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewCompressStep(llmClient),
			"素材压缩：将搜索结果压缩为结构化研究简报，节省 prompt token",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  []string{"relevance"},
			Repeatable:  true,
			Terminal:    false,
			Category:    "retrieval",
		},
	)

	if execCtx.Mode == "guided" {
		registry.RegisterWithDescriptor(
			engine.NewStepTool(
				steps.NewOutlineStep(llmClient),
				"提纲生成：为引导模式生成文章提纲（标题+要点），等待用户确认",
				false,
			),
			engine.ToolDescriptor{
				DependsOn:  nil,
				Repeatable:  false,
				Terminal:    false,
				Category:    "planning",
			},
		)
	}

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewWriteStepWithKB(llmClient, styleProfile, s.search, services.NewKbSearchAdapter(s.kbMgr)),
			"文章生成：按风格 Profile 生成文章，支持流式输出和 Agent Loop",
			true, // terminal — article is produced
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable:  true,
			Terminal:    true,
			Category:    "writing",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			s.newPostReviewStepWithLLM(llmClient, styleProfile),
			"质量评审：多维度评分（事实/结构/风格/修辞/安全）+ 敏感词检查",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable:  true,
			Terminal:    false,
			Category:    "review",
		},
	)

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewAutoFixStepWithProfile(llmClient, styleProfile),
			"自动修正：根据评审结果自动修正可修复的问题（含标题独立修正）",
			false,
		),
		engine.ToolDescriptor{
			DependsOn:  []string{"post_review"},
			Repeatable:  true,
			Terminal:    false,
			Category:    "review",
		},
	)

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.RegisterWithDescriptor(
			engine.NewStepTool(
				steps.NewMemoryExtractStep(s.memorySvc),
				"记忆提取：从文章和反馈中异步提取写作偏好模式",
				false,
			),
			engine.ToolDescriptor{
				DependsOn:  nil,
				Repeatable:  false,
				Terminal:    false,
				Category:    "memory",
			},
		)
	}

	// ── MCP Tools: dynamically discovered from MCP servers ──
	// MCP tools use the basic Register() method (no descriptor = repeatable, no deps)
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
