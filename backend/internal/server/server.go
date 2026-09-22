package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/mcp"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingtransport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/crypto"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// Server holds all application dependencies.
type Server struct {
	cfg               *config.Config
	sseHub            *SSEHub
	rateLimiter       *RateLimiter
	llm               *tools.LLMClient
	llmSvc            *services.LLMService
	search            *tools.SearchClient
	embedding         *tools.EmbeddingClient
	profiles          *profile.Loader
	traces            *database.TraceRepo
	feedback          *database.FeedbackRepo
	evalRepo          *database.EvaluationRepo
	wabenchRepo       *database.WABenchRepo
	adminRepo         *database.AdminRepo
	kbRepo            *database.KnowledgeBaseRepo
	evalSvc           *services.EvaluationService
	wabenchSvc        *services.WABenchEvaluationService
	reputationSvc     *services.ReputationService
	dbAvail           bool
	sessions          sync.Map // traceID → *engine.ExecutionContext
	cronScheduler     *services.CronScheduler
	metrics           *MetricsRegistry
	webauthn          *WebAuthnService
	passkeyChallenges *passkeyChallengeStore
	sensitiveSvc      *services.SensitiveCheckService
	jiaozhen          *tools.JiaozhenClient
	memorySvc         *memsvc.Service
	mcpRegistry       *mcp.Registry
	mcpServer         *mcp.MCPServer
	toolRegistry      *engine.ToolRegistry
	editorialSvc      *editorial.Service
	editorialHdlr     *editorial.Handlers
	planner           *editorial.Planner
	dagExecutor       *editorial.DAGExecutor
	redTeamRepo       *database.RedTeamRepo
	evidenceRepo      *database.EvidenceRepo
	sessionEvents     *database.SessionEventRepo

	userStyleStore *database.UserStyleStore
	styleBuilder   *services.StyleBuilderService

	// Knowledge Manager (operates directly on local PG)
	kbMgr *services.KbManager

	// Self-Evolution service
	evolutionSvc *services.EvolutionService

	// Canary health monitor for auto-rollback
	canaryMonitor *CanaryHealthMonitor

	// MCP security sandbox
	sandbox *MCPSandbox

	// Route metadata registry for /api/v2/admin/routes discovery
	routeReg *routeRegistry

	// Horizontal scaling: Redis-backed session adapter (nil = single-instance mode)
	sessionAdapter *RedisSessionAdapter

	// Instance ID for multi-instance identification
	instanceID string

	// Raw database handle (for queries without a dedicated repo)
	db *database.DB

	// Governed writing API. Nil only when persistence is unavailable.
	writingAPI      writingAPIService
	governedRollout *governedRolloutDependencies
	governedTrigger *governedRunTrigger
	arReview        *arReviewService
	kbSearch        tools.KnowledgeSearcher
	editorialTools  *editorial.EditorialToolRegistry
	editorialAgents *editorial.DynamicAgentRegistry

	// Deployment readiness is stricter than liveness: configured external
	// dependencies remain unready until a bounded probe succeeds.
	readiness         *ReadinessRegistry
	providerPreflight *ProviderPreflight

	// Billing
	billingRepo *database.BillingRepo
	pointCalc   *services.PointCalculator
	alipaySvc   *AlipayService

	// Ops patrol (rule-based health checks → admin alerts)
	adminAlertRepo *database.AdminAlertRepo
	opsPatrol      *OpsPatrol

	// Email verification (commercial feature)
	emailSvc    *EmailService
	redisClient *RedisClient
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
		//
		// Edition boundary (docs/29-wp5-boundary-governance.md §3): the tavily and
		// anysearch overrides below are edition-neutral config plumbing shared by both
		// editions. In the OSS edition both clients are stubs (search_stubs.go) whose
		// Search/Extract return ErrProviderNotInstalled, so keys collected here can
		// never produce paid-search results. OSS deployment templates must not carry
		// TAVILY_*/ANYSEARCH_* variables (enforced by tools/edition_boundary_test.go).
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
		cfg.Weibo.Enabled, cfg.Weibo.AppID, cfg.Weibo.AppSecret, cfg.Weibo.TokenEndpoint, cfg.Weibo.BaseURL, cfg.Weibo.Timeout,
		cfg.ExtraHot.Enabled, cfg.ExtraHot.BaseURL, cfg.ExtraHot.Timeout,
		cfg.Bing.Enabled, cfg.Bing.BaseURL, cfg.Bing.Timeout,
		cfg.Jiaozhen.CLIPath, cfg.Jiaozhen.Timeout,
		cfg.AnySearch.APIKey, cfg.AnySearch.Endpoint, cfg.AnySearch.Timeout,
		cfg.SearXNG.BaseURL, cfg.SearXNG.Timeout,
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
		db:            db,
	}

	// ── User custom styles & AI builder ──
	if dbAvail && adminRepo != nil && adminRepo.DB() != nil {
		s.userStyleStore = database.NewUserStyleStore(db)
		s.db = db
	}

	// ── Billing ──
	if dbAvail && db != nil {
		s.billingRepo = database.NewBillingRepo(db)
		s.pointCalc = services.NewPointCalculator(db)
		if s.db == nil {
			s.db = db
		}

		// Initialize Alipay payment service
		if cfg.Alipay.Enabled {
			alipaySvc, err := NewAlipayService(AlipayConfigConfig{
				Enabled:        cfg.Alipay.Enabled,
				AppID:          cfg.Alipay.AppID,
				PrivateKey:     cfg.Alipay.PrivateKey,
				PublicKey:      cfg.Alipay.PublicKey,
				CertPath:       cfg.Alipay.CertPath,
				RootCertPath:   cfg.Alipay.RootCertPath,
				AlipayCertPath: cfg.Alipay.AlipayCertPath,
				NotifyURL:      cfg.Alipay.NotifyURL,
				ReturnURL:      cfg.Alipay.ReturnURL,
				Sandbox:        cfg.Alipay.Sandbox,
			}, s.billingRepo)
			if err != nil {
				slog.Error("alipay: failed to initialize payment service", "error", err)
			} else {
				s.alipaySvc = alipaySvc
				slog.Info("alipay payment service enabled", "sandbox", cfg.Alipay.Sandbox)
			}
		}
		slog.Info("billing system initialized", "db_available", true)
	}
	if dbAvail && db != nil {
		governedStore, err := writingstore.New(db)
		if err != nil {
			return nil, fmt.Errorf("initialize governed writing store: %w", err)
		}
		s.writingAPI = newPersistentWritingAPI(governedStore)
		// R14 feature flag: RESEARCH_REVIEW_ENABLED (default false) gates the
		// research_review compile/run entries; read-only research endpoints
		// and legacy modes are untouched.
		if api, ok := s.writingAPI.(*persistentWritingAPI); ok {
			api.researchReviewEnabled = cfg.WritingRuntime.ResearchReviewEnabled
		}
		s.governedRollout = newGovernedRolloutDependencies(governedStore, s.metrics)
		// M1.4: mount the governed runtime behind WRITING_RUNTIME_MODE
		// (default off — zero behavior change) and wire its controller into
		// the writing API's control routes plus the post-approval trigger.
		s.mountGovernedRuntime(governedStore)
		// T10: AR-012 candidate evaluation sidecar (default off; nil = every
		// endpoint reports AR_REVIEW_UNAVAILABLE).
		s.arReview = newArReviewService(governedStore, cfg.WritingRuntime.ArReview)
		if s.arReview != nil {
			slog.Info("ar review sidecar enabled", "exchange", cfg.WritingRuntime.ArReview.ExchangeDir)
		}
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

		// ── Canary Health Monitor (auto-rollback) ──
		s.canaryMonitor = NewCanaryHealthMonitor(s, 30*time.Second)
		s.canaryMonitor.Start()
	}

	// ── Ops Patrol (rule-based health checks → admin alerts) ──
	// OPS_PATROL_ENABLED=false 可在部署层面整体禁用（运行时开关见 admin_patrol_config）。
	if dbAvail && s.adminRepo != nil && opsPatrolEnabledFromEnv() {
		s.adminAlertRepo = database.NewAdminAlertRepo(db)
		s.opsPatrol = NewOpsPatrol(s, 0)
		s.opsPatrol.Start()
		slog.Info("ops patrol initialized")
	}

	// ── MCP Security Sandbox ──
	// Initialized even without DB (fails open with defaults)
	s.sandbox = NewMCPSandbox(s.db)
	// Inject sandbox hook into all registered MCP tools
	if s.mcpRegistry != nil && s.toolRegistry != nil {
		adapter := &mcpSandboxAdapter{sb: s.sandbox}
		for _, t := range s.toolRegistry.All() {
			if mcpTool, ok := t.(*mcp.MCPAgentTool); ok {
				mcpTool.SetSandboxHook(adapter)
			}
		}
	}

	// ── Security Event Recorder (Prompt Injection Audit) ──
	// Persists interception events to security_events table for audit dashboard.
	// Falls back to in-memory only if DB is unavailable.
	if dbAvail && s.db != nil {
		recorder := newSecurityEventDBRecorder(s.db)
		engine.SetSecurityEventRecorder(recorder)
		slog.Info("security event recorder initialized — prompt injection interceptions will be persisted")
	} else {
		slog.Warn("DB not available — security events will only be tracked in-memory")
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

		// Wire KB manager and user style store into Style Builder for tool calls
		if s.styleBuilder != nil {
			s.styleBuilder.WithKbManager(s.kbMgr)
			if s.userStyleStore != nil {
				s.styleBuilder.WithUserStyleStore(s.userStyleStore)
			}
			slog.Info("style builder wired with KB manager and user style store")
		}
	} else {
		slog.Warn("knowledge manager skipped: database not available")
	}

	// WABench V2 shadow runner uses the same Harness and runtime dependencies
	// as the user-facing writing path; it does not bypass retrieval or tools.
	if wabenchRepo != nil && defaultLLM != nil {
		// Memory port for WABench: nil when memory service is unavailable,
		// which disables memory injection for all candidates. When available,
		// the adapter uses it read-only (PrepareInjection, never SubmitOutcome).
		var wabenchMemoryPort memoryport.Port
		if s.memorySvc != nil && s.memorySvc.IsAvailable() {
			wabenchMemoryPort = s.memoryPort()
		}
		wabenchAdapter := services.NewHarnessWABenchExecutorWithResolver(
			s.llmSvc,
			s.search,
			services.NewKbSearchAdapter(s.kbMgr),
			s.profiles,
			s.userStyleStore,
			wabenchMemoryPort,
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
		// Editorial Transport Migration: 编排事件经 SSE 定向推送（task owner 隔离），
		// 取代旧的 WebSocket 全局广播。
		edEmitter := newEditorialSSEEmitter(s.sseHub, edStore)
		edSvc := editorial.NewService(edStore, edEmitter)

		// Wire source credibility into search client
		// This enriches search results with credibility scores from editorial_source_credibility
		searchClient.SetCredibilityLookup(editorial.NewCredibilityLookupAdapter(edStore))
		slog.Info("source credibility lookup wired into search client")

		// 初始化对照实验运行器（不再依赖 Orchestrator）
		// 传入 LLMResolver 而非静态 LLMClient，使 admin 面板模型配置变更即时生效
		expRunner := editorial.NewExperimentRunner(
			edStore, s.llmSvc, searchClient, embeddingClient,
			profileLoader, edEmitter,
		)
		editorial.SetExperimentRunner(expRunner)

		s.editorialSvc = edSvc
		s.editorialHdlr = editorial.NewHandlers(edSvc)
		slog.Info("editorial system initialized")

		// Beta: 编辑部模式 DAG 工作流初始化
		// 传入 LLMResolver 而非静态 LLMClient，使 admin 面板模型配置变更即时生效
		if s.llmSvc != nil {
			s.planner = editorial.NewPlanner(s.llmSvc)
			agentRegistry := editorial.NewDynamicAgentRegistry()
			s.dagExecutor = editorial.NewDAGExecutor(
				agentRegistry, edStore, edEmitter,
			)
			// 注册预设 Agent 执行器到 DAGExecutor（以 BaseRole 作为 key）
			kbAdapter := services.NewKbSearchAdapter(s.kbMgr)

			// ── 初始化编辑部工具注册中心（DAG 模式独立实例）──
			dagToolRegistry := editorial.NewEditorialToolRegistry()
			editorial.RegisterBuiltinTools(dagToolRegistry)
			// Keep the unified capability view able to see the editorial
			// surface (M1.5); registration authority stays with the DAG.
			s.editorialTools = dagToolRegistry
			s.editorialAgents = agentRegistry

			researchExec := editorial.NewResearchAgentExecutor(s.llmSvc, searchClient, embeddingClient, edStore, kbAdapter, dagToolRegistry)
			s.dagExecutor.RegisterExecutor("researcher", researchExec)
			writingExec := editorial.NewWritingAgentExecutor(s.llmSvc, profileLoader, s.userStyleStore, searchClient, edStore, kbAdapter, dagToolRegistry)
			reviewExec := editorial.NewReviewAgentExecutor(s.llmSvc, profileLoader, s.userStyleStore, searchClient, edStore, kbAdapter, dagToolRegistry)
			s.dagExecutor.RegisterExecutor("writer", writingExec)
			s.dagExecutor.RegisterExecutor("reviewer", reviewExec)
			slog.Info("DAG workflow system initialized (planner + executor)")
		}
	} else {
		slog.Warn("editorial system disabled — database not available")
	}

	// ── Horizontal scaling: generate instance ID ──
	instanceID := getEnvInstanceID(cfg.Server.Host, cfg.Server.Port)

	// ── In-Process MCP Server ──
	if cfg.MCPServer.Enabled {
		s.initMCPServer(cfg)
	}

	// Set instance ID
	s.instanceID = instanceID

	// ── Redis + Email Service (commercial feature) ──
	if cfg.Redis.Enabled {
		s.redisClient = NewRedisClient(cfg.Redis.URL)
		if s.redisClient != nil {
			slog.Info("Redis client initialized for verification codes", "instance_id", instanceID)
			// Also wire session adapter
			s.sessionAdapter = NewRedisSessionAdapter(s.redisClient, instanceID, 30*time.Second)
			go s.sessionAdapter.StartHeartbeat(context.Background(), &s.sessions)
		}
	} else {
		slog.Info("Single-instance mode (Redis not enabled)", "instance_id", instanceID)
	}

	// ── Email Service ──
	s.emailSvc = NewEmailService(cfg.SMTP)
	if s.emailSvc != nil {
		slog.Info("email service initialized",
			"smtp_host", cfg.SMTP.Host,
			"smtp_port", cfg.SMTP.Port,
			"from", cfg.SMTP.Username,
		)
	}

	s.initializeReadiness(time.Now())
	s.providerPreflight = NewProviderPreflight(cfg, s.readiness, s.search, s.updateMCPReadiness)
	s.providerPreflight.Start(context.Background())

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
	r.Get("/ready", s.handleReady)

	// Prometheus metrics
	r.Get("/metrics", s.handleMetrics)

	// A2A Agent Cards (public — capability discovery)
	r.Get("/api/v2/agent-cards", s.handleAgentCards)

	// API v2
	r.Route("/api/v2", func(r chi.Router) {
		s.registerWritingRoutes(r)
		// Styles (jwtOptional: logged-in users see global + their private styles)
		r.With(s.jwtOptionalMiddleware).Get("/styles", s.handleListStylesWithUserStyles)
		r.With(s.jwtOptionalMiddleware).Get("/styles/{slug}", s.handleGetStyle)

		// User Custom Styles (requires auth)
		r.With(s.jwtAuthMiddleware).Get("/my-styles", s.handleListMyStyles)
		r.With(s.jwtAuthMiddleware).Post("/my-styles", s.handleCreateMyStyle)
		r.With(s.jwtAuthMiddleware).Get("/my-styles/{id}", s.handleGetMyStyle)
		r.With(s.jwtAuthMiddleware).Put("/my-styles/{id}", s.handleUpdateMyStyle)
		r.With(s.jwtAuthMiddleware).Delete("/my-styles/{id}", s.handleDeleteMyStyle)
		r.With(s.jwtAuthMiddleware).Post("/my-styles/{id}/submit", s.handleSubmitMyStyleForReview)
		r.With(s.jwtAuthMiddleware).Post("/my-styles/{id}/withdraw", s.handleWithdrawMyStyleReview)

		// AI Style Builder (requires auth)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions", s.handleCreateBuilderSession)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions/{id}/messages", s.handleSendBuilderMessage)
		r.With(s.jwtAuthMiddleware).Post("/style-builder/sessions/{id}/commit", s.handleCommitBuilderSession)

		// Models (public — list active models for composer)
		r.Get("/models", s.handleListActiveModels)

		// Billing (user-facing)
		// /billing/plans is public (no JWT) so unauthenticated users can view pricing
		r.Get("/billing/plans", s.handleBillingPlans)
		r.With(s.jwtAuthMiddleware).Get("/billing/balance", s.handleBillingBalance)
		r.With(s.jwtAuthMiddleware).Get("/billing/subscription", s.handleBillingSubscription)
		r.With(s.jwtAuthMiddleware).Post("/billing/subscribe", s.handleBillingSubscribe)
		r.With(s.jwtAuthMiddleware).Get("/billing/consumption", s.handleBillingConsumption)
		r.With(s.jwtAuthMiddleware).Get("/billing/consumption/summary", s.handleBillingConsumptionSummary)
		r.With(s.jwtAuthMiddleware).Post("/billing/recharge", s.handleBillingRecharge)
		r.With(s.jwtAuthMiddleware).Get("/billing/recharge/orders", s.handleBillingRechargeOrders)
		r.With(s.jwtAuthMiddleware).Post("/billing/redeem", s.handleBillingRedeem)
		r.With(s.jwtAuthMiddleware).Get("/billing/features", s.handleBillingFeatures)
		r.With(s.jwtAuthMiddleware).Post("/billing/payment/alipay", s.handleAlipayCreatePayment)
		r.With(s.jwtAuthMiddleware).Get("/billing/orders", s.handleBillingOrderStatus)

		// Payment callback (no JWT, public endpoint)
		r.Post("/billing/callback/alipay", s.handleAlipayCallback)
		r.Get("/billing/callback/alipay", s.handleAlipayCallback)

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

		// Memory (requires auth — user identity from JWT, guests excluded)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/memories", s.handleListMemories)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/memories", s.handleCreateMemory)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Delete("/memories/{id}", s.handleDeleteMemory)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/memories/{id}/dismiss", s.handleDismissMemory)

		// Memory Files (Markdown memory layer — guests excluded)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/memories/file", s.handleGetMemoryFile)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/memories/file/export", s.handleExportMemoryFile)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/memories/file/import", s.handleImportMemoryFile)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/memories/global", s.handleGetGlobalMemory)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Put("/memories/global", s.handleUpdateGlobalMemory)

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
		r.With(s.jwtAuthMiddleware).Put("/materials/{id}/move", s.handleMaterialMove)

		// Material Folders
		r.With(s.jwtAuthMiddleware).Get("/material-folders", s.handleFolderList)
		r.With(s.jwtAuthMiddleware).Post("/material-folders", s.handleFolderCreate)
		r.With(s.jwtAuthMiddleware).Put("/material-folders/{id}", s.handleFolderUpdate)
		r.With(s.jwtAuthMiddleware).Delete("/material-folders/{id}", s.handleFolderDelete)

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

		// SSE (Server-Sent Events)
		r.Get("/sse/topics", s.handleSSETopics)
		r.Get("/topics/stream", s.handleSSETopics) // alias per docs/03-api-specification.md
		r.Get("/sse/stats", s.handleSSEStats)
		r.Post("/sse/test-notify", s.handleSSETestNotification) // user-scoped test notification

		// Editorial (编辑部系统) — JWT-protected
		if s.editorialHdlr != nil {
			r.Group(func(r chi.Router) {
				r.Use(s.jwtAuthMiddleware)
				s.editorialHdlr.RegisterRoutes(r)
			})
		}

		// Editorial workflow command facade (REST) — Editorial Transport Migration.
		// 取代已下线的 workflow.* WebSocket 消息：命令走 REST，进度事件经
		// /api/v2/sse/topics 以 workflow:*/node:* 事件定向推送给 task owner。
		// 刻意不提供 pause/resume 端点：DAGExecutor 尚无 checkpoint/resume 语义，
		// 假的 "not_yet_implemented" 端点不是诚实的契约。
		if s.editorialSvc != nil {
			r.Group(func(r chi.Router) {
				r.Use(s.jwtAuthMiddleware, s.rejectGuestMiddleware)
				r.Post("/workflows", s.handleWorkflowCreate)
				r.Post("/workflows/{id}/execute", s.handleWorkflowExecute)
				r.Post("/workflows/{id}/cancel", s.handleWorkflowCancel)
				r.Get("/workflows/{id}", s.handleWorkflowGet)
			})
		}

		// Auth (JWT)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/register", s.handleRegister)
		r.Post("/auth/guest", s.handleGuestLogin)
		r.Post("/auth/refresh", s.handleRefreshToken)
		r.With(s.jwtAuthMiddleware).Get("/auth/verify", s.handleVerifyToken)

		// Passkey / WebAuthn
		// register/begin uses optional JWT — if the user is logged in (e.g. from personal center),
		// the user_id is resolved from the JWT context. If not (e.g. from the login page),
		// user_id is taken from the request body.
		r.With(s.jwtOptionalMiddleware).Post("/auth/passkey/register/begin", s.handlePasskeyRegisterBegin)
		r.With(s.jwtOptionalMiddleware).Post("/auth/passkey/register/complete", s.handlePasskeyRegisterComplete)
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
		r.With(s.jwtAuthMiddleware).Put("/sessions/{traceId}/title", s.handleUpdateSessionTitle)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/versions", s.handleListArticleVersions)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/versions/{versionId}", s.handleGetArticleVersion)
		// Plan CRUD (DAG 工作流计划增删查改)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}/plan", s.handleGetSessionPlan)
		r.With(s.jwtAuthMiddleware).Put("/sessions/{traceId}/plan", s.handleUpdateSessionPlan)
		r.With(s.jwtAuthMiddleware).Delete("/sessions/{traceId}/plan", s.handleDeleteSessionPlan)
		r.With(s.jwtAuthMiddleware).Post("/auth/change-password", s.handleChangePassword)
		r.With(s.jwtAuthMiddleware).Post("/auth/update-profile", s.handleUpdateProfile)
		r.With(s.jwtAuthMiddleware).Post("/auth/deactivate", s.handleDeactivateAccount)
		r.With(s.jwtAuthMiddleware).Get("/auth/sessions", s.handleListUserActiveSessions)

		// Email verification (commercial feature)
		r.Post("/auth/send-code", s.handleSendVerificationCode)
		r.Post("/auth/forgot-password", s.handleForgotPassword)
		r.Post("/auth/reset-password", s.handleResetPassword)
		r.With(s.jwtAuthMiddleware).Post("/auth/bind-email", s.handleBindEmail)
		r.With(s.jwtAuthMiddleware).Post("/auth/unbind-email", s.handleUnbindEmail)
		r.With(s.jwtAuthMiddleware).Get("/auth/my-email", s.handleGetEmail)

		// Admin (protected by admin token + fine-grained RBAC permissions)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.adminAuthMiddleware)

			// Dashboard stats (any admin user)
			r.Get("/stats", s.handleAdminStats)
			r.Get("/exit-stats", s.handleAdminExitStats)
			r.Get("/routes", s.handleAdminRoutes)
			r.Get("/provider-preflight", s.handleAdminGetProviderPreflight)
			r.Post("/provider-preflight", s.handleAdminRunProviderPreflight)
			// Unified capability inventory (M1.5 slice 2)
			r.Get("/capabilities", s.handleAdminCapabilities)

			// AR-012 candidate evaluation console (T10, eval.view)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("eval.view"))
				r.Get("/ar-review/jobs", s.handleAdminListArReviewJobs)
				r.Get("/ar-review/jobs/{jobId}/artifacts/{kind}", s.handleAdminReadArReviewArtifact)
			})

			// Traces (audit.view)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("audit.view"))
				r.Get("/traces", s.handleAdminListTraces)
				r.Get("/traces/{traceId}", s.handleAdminGetTrace)
				r.Get("/audit-logs", s.handleAdminListAuditLogs)
				r.Get("/ab-metrics", s.handleAdminABMetrics)
				r.Get("/token-usage", s.handleAdminTokenUsage)
				r.Get("/evidence/{traceId}", s.handleAdminGetEvidence)
			})

			// Style Profile CRUD (style.create)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("style.create"))
				r.Get("/styles", s.handleAdminListStyles)
				r.Get("/styles/{slug}", s.handleAdminGetStyle)
				r.Post("/styles", s.handleAdminCreateStyle)
				r.Put("/styles/{slug}", s.handleAdminUpdateStyle)
				r.Post("/styles/{slug}/publish", s.handleAdminPublishStyle)
				r.Post("/styles/{slug}/archive", s.handleAdminArchiveStyle)
				r.Get("/styles/{slug}/versions", s.handleAdminListVersions)
				r.Post("/styles/{slug}/versions/{version}/republish", s.handleAdminRepublishVersion)
				r.Get("/styles/{slug}/versions/compare", s.handleAdminCompareVersions)
				// Rollout (Grayscale)
				r.Get("/styles/{slug}/rollout", s.handleAdminGetRollout)
				r.Put("/styles/{slug}/rollout", s.handleAdminUpdateRollout)
				r.Post("/styles/{slug}/rollout/preview", s.handleAdminPreviewRollout)
			})

			// Community style review (style.review)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("style.review"))
				r.Get("/pending-styles", s.handleAdminListPendingStyles)
				r.Post("/pending-styles/{id}/approve", s.handleAdminApproveStyle)
				r.Post("/pending-styles/{id}/reject", s.handleAdminRejectStyle)
			})

			// Sensitive Words (sensitive.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("sensitive.manage"))
				r.Get("/sensitive-words", s.handleAdminListSensitiveWords)
				r.Post("/sensitive-words", s.handleAdminAddSensitiveWord)
				r.Delete("/sensitive-words/{id}", s.handleAdminDeleteSensitiveWord)
				r.Put("/sensitive-words/config", s.handleAdminSensitiveConfig)
				r.Get("/sensitive-words/config", s.handleAdminSensitiveConfig)
			})

			// Model Configs (model.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("model.manage"))
				r.Get("/models", s.handleAdminListModelConfigs)
				r.Post("/models", s.handleAdminCreateModelConfig)
				r.Post("/models/discover", s.handleAdminDiscoverModels)
				r.Put("/models/{id}", s.handleAdminUpdateModelConfig)
				r.Delete("/models/{id}", s.handleAdminDeleteModelConfig)
				r.Post("/models/batch", s.handleAdminBatchModels)
				r.Get("/memory/telemetry", s.handleAdminMemoryTelemetry)
			})

			// API Keys (apikey.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("apikey.manage"))
				r.Get("/api-keys", s.handleAdminListAPIKeys)
				r.Post("/api-keys", s.handleAdminCreateAPIKey)
				r.Put("/api-keys/{id}", s.handleAdminUpdateAPIKey)
				r.Delete("/api-keys/{id}", s.handleAdminDeleteAPIKey)
				r.Post("/api-keys/{id}/test", s.handleAdminTestAPIKey)
				r.Post("/api-keys/batch", s.handleAdminBatchAPIKeys)
			})

			// Cron Jobs (cron.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("cron.manage"))
				r.Get("/cron-jobs", s.handleAdminListCronJobs)
				r.Post("/cron-jobs", s.handleAdminCreateCronJob)
				r.Put("/cron-jobs/{id}", s.handleAdminUpdateCronJob)
				r.Delete("/cron-jobs/{id}", s.handleAdminDeleteCronJob)
				r.Post("/cron-jobs/{id}/run", s.handleAdminRunCronJob)
				r.Post("/cron-jobs/batch", s.handleAdminBatchCronJobs)
			})

			// Knowledge Base Admin (kb.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("kb.manage"))
				r.Post("/kb/generate-embeddings", s.handleKBGenerateEmbeddings)
				r.Post("/kb/rechunk", s.handleKBRechunk)
				r.Post("/kb/reimport", s.handleKBReimport)
			})

			// MCP Server Admin (mcp.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("mcp.manage"))
				r.Get("/mcp/status", s.handleAdminMCPStatus)
				r.Get("/mcp/tools", s.handleAdminMCPTools)
				r.Get("/mcp/export", s.handleAdminMCPExport)
				r.Get("/mcp/servers", s.handleAdminListMCPServers)
				r.Post("/mcp/servers", s.handleAdminCreateMCPServer)
				r.Put("/mcp/servers/{id}", s.handleAdminUpdateMCPServer)
				r.Delete("/mcp/servers/{id}", s.handleAdminDeleteMCPServer)
				r.Post("/mcp/servers/{id}/reconnect", s.handleAdminReconnectMCPServer)
				// Tool Plugin Management
				r.Get("/tool-plugins", s.handleAdminListToolPlugins)
				r.Post("/tool-plugins", s.handleAdminCreateToolPlugin)
				r.Get("/tool-plugins/{name}", s.handleAdminGetToolPlugin)
				r.Delete("/tool-plugins/{name}", s.handleAdminDeleteToolPlugin)
			})

			// MCP Security Sandbox (sandbox.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("sandbox.manage"))
				r.Get("/mcp/sandbox/policies", s.handleAdminListMCPToolPolicies)
				r.Post("/mcp/sandbox/policies", s.handleAdminCreateMCPToolPolicy)
				r.Put("/mcp/sandbox/policies/{id}", s.handleAdminUpdateMCPToolPolicy)
				r.Delete("/mcp/sandbox/policies/{id}", s.handleAdminDeleteMCPToolPolicy)
				r.Get("/mcp/sandbox/violations", s.handleAdminListMCPViolations)
				r.Get("/mcp/sandbox/stats", s.handleAdminGetSandboxStats)
				r.Post("/mcp/sandbox/test", s.handleAdminTestSandbox)
				r.Post("/mcp/sandbox/reload", s.handleAdminReloadSandbox)
			})

			// Security Audit (security.view)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("security.view"))
				r.Get("/security/audit", s.handleAdminSecurityAudit)
				r.Get("/security/events", s.handleAdminSecurityEvents)
			})

			// Self-Evolution (evolution.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("evolution.manage"))
				r.Get("/evolution/candidates", s.handleAdminListEvolutionCandidates)
				r.Post("/evolution/candidates/{id}/approve", s.handleAdminApproveEvolutionCandidate)
				r.Post("/evolution/candidates/{id}/reject", s.handleAdminRejectEvolutionCandidate)
				r.Post("/evolution/candidates/{id}/canary", s.handleAdminEnableCanaryRollout)
				r.Post("/evolution/candidates/{id}/rollback", s.handleAdminRollbackCanary)
				r.Post("/evolution/candidates/{id}/promote", s.handleAdminPromoteToFull)
				r.Get("/evolution/candidates/{id}/metrics", s.handleAdminGetCanaryMetrics)
				r.Get("/evolution/gate-configs", s.handleAdminListGateConfigs)
				r.Get("/evolution/gate-config/{slug}", s.handleAdminGetGateConfig)
				r.Put("/evolution/gate-config/{slug}", s.handleAdminUpdateGateConfig)
				r.Get("/evolution/candidates/{id}/events", s.handleAdminGetGateEvents)
				r.Get("/evolution/candidates/{id}/health", s.handleAdminGetHealthSnapshots)
			})

			// WritingAgentBench V2 (wabench.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("wabench.manage"))
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
			})

			// RBAC — Role & Permission Management (rbac.manage)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("rbac.manage"))
				r.Get("/rbac/roles", s.handleAdminListRoles)
				r.Get("/rbac/permissions", s.handleAdminListPermissions)
				r.Get("/rbac/roles/{id}/permissions", s.handleAdminGetRolePermissions)
				r.Put("/rbac/roles/{id}/permissions", s.handleAdminUpdateRolePermissions)
				r.Get("/rbac/users", s.handleAdminListUsersWithRoles)
				r.Get("/rbac/users/{userId}/roles", s.handleAdminListUserRoles)
				r.Post("/rbac/users/{userId}/roles", s.handleAdminAssignUserRole)
				r.Delete("/rbac/users/{userId}/roles/{roleId}", s.handleAdminRemoveUserRole)
			})

			// Billing Management (billing.view for read, billing.manage for write)
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("billing.view"))
				r.Get("/billing/overview", s.handleAdminBillingOverview)
				r.Get("/billing/users", s.handleAdminBillingUsers)
				r.Get("/billing/users/{userId}", s.handleAdminBillingUserDetail)
				r.Get("/billing/revenue", s.handleAdminBillingRevenue)
				r.Get("/billing/consumption", s.handleAdminBillingConsumption)
				r.Get("/billing/point-rates", s.handleAdminBillingPointRates)
				r.Get("/billing/plans", s.handleAdminBillingPlans)
				r.Get("/billing/redeem-codes", s.handleAdminBillingListRedeemCodes)
			})
			r.Group(func(r chi.Router) {
				r.Use(s.requirePermission("billing.manage"))
				r.Post("/billing/point-rates", s.handleAdminBillingCreatePointRate)
				r.Put("/billing/point-rates/{id}", s.handleAdminBillingUpdatePointRate)
				r.Delete("/billing/point-rates/{id}", s.handleAdminBillingDeletePointRate)
				r.Put("/billing/multiplier", s.handleAdminBillingSetMultiplier)
				r.Put("/billing/plans/{id}", s.handleAdminBillingUpdatePlan)
				r.Post("/billing/recharge", s.handleAdminBillingRecharge)
				r.Post("/billing/redeem-codes", s.handleAdminBillingCreateRedeemCodes)
				r.Delete("/billing/redeem-codes/{id}", s.handleAdminBillingDisableRedeemCode)
				r.Post("/billing/reset-plan-balance", s.handleAdminBillingReset)
				r.Post("/billing/expire-subscriptions", s.handleAdminBillingExpire)
			})

			// SSE Notifications (admin test — any admin)
			r.Post("/sse/notify", s.handleSSESendNotification)

			// Ops Patrol Alerts (any admin; ack/resolve audit-logged)
			r.Get("/alerts", s.handleAdminListAlerts)
			r.Post("/alerts/{id}/ack", s.handleAdminAckAlert)
			r.Post("/alerts/{id}/resolve", s.handleAdminResolveAlert)
			r.Get("/patrol/config", s.handleAdminGetPatrolConfig)
			r.Put("/patrol/config", s.handleAdminUpdatePatrolConfig)
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
		if s.canaryMonitor != nil {
			s.canaryMonitor.Stop()
		}
		if s.opsPatrol != nil {
			s.opsPatrol.Stop()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Server.WriteTimeout)
		defer cancel()
		srv.Shutdown(shutdownCtx)
	}()

	if s.governedTrigger != nil { go s.governedTrigger.Serve(ctx) }

	// AR-012 candidate evaluation worker (nil when the sidecar is disabled).
	if s.arReview != nil { go s.arReview.Serve(ctx) }

	// Start SSE topic push background task
	go s.PushTopicsFromDB(ctx, 30*time.Second)

	// Start hot topics auto-fetch (every 10 minutes)
	go s.autoFetchHotTopics(ctx, s.cfg.HotTopics.FetchInterval)

	// Start cron scheduler
	if s.cronScheduler != nil {
		go s.cronScheduler.Start(ctx, s.executeCronJob)
	}

	// Start billing cron (plan_balance reset + subscription expiry)
	go s.startBillingCron(ctx)

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

	// Count active sessions for health/load balancing
	activeSessions := 0
	s.sessions.Range(func(_, v interface{}) bool {
		if ec, ok := v.(*engine.ExecutionContext); ok {
			if ec.Status == engine.StatusRunning || ec.Status == engine.StatusPaused {
				activeSessions++
			}
		}
		return true
	})

	response.OK(w, map[string]interface{}{
		"status":               "ok",
		"version":              "v2",
		"instance_id":          s.instanceID,
		"llm_configured":       s.llm != nil && s.llm.IsConfigured(),
		"search_configured":    s.search != nil && s.search.HasExternalSources(),
		"db_configured":        s.dbAvail,
		"embedding_configured": embeddingConfigured,
		"active_sessions":      activeSessions,
		"redis_enabled":        s.cfg.Redis.Enabled,
	})
}

func (s *Server) handleListStyles(w http.ResponseWriter, r *http.Request) {
	styles := s.profiles.List()
	response.OK(w, map[string]interface{}{"styles": styles})
}

func (s *Server) handleGetStyle(w http.ResponseWriter, r *http.Request) {
	slug := chi.URLParam(r, "slug")

	// Handle user custom styles (slug prefix "my_")
	// These are stored in user_style_profiles, not in the global profile loader.
	if strings.HasPrefix(slug, "my_") && s.userStyleStore != nil {
		// Need the authenticated user to resolve which user's style this is.
		userID := s.getUserIDFromRequest(r)
		if userID == "anonymous" {
			response.Err(w, http.StatusUnauthorized, "unauthorized", "login required to view custom styles")
			return
		}
		rawSlug := strings.TrimPrefix(slug, "my_")
		p, err := s.userStyleStore.GetProfileBySlugAndOwner(r.Context(), rawSlug, userID)
		if err != nil {
			response.Err(w, http.StatusNotFound, "not_found", "style not found")
			return
		}
		// Load latest version config
		if p.CurrentVersion == 0 {
			response.Err(w, http.StatusNotFound, "no_version", "style has no saved version")
			return
		}
		v, err := s.userStyleStore.GetLatestVersion(r.Context(), p.ID)
		if err != nil {
			response.Err(w, http.StatusNotFound, "not_found", "style version not found")
			return
		}
		var sp profile.StyleProfile
		if err := json.Unmarshal([]byte(v.Config), &sp); err != nil {
			response.Err(w, http.StatusInternalServerError, "internal_error", "failed to parse style config")
			return
		}
		response.OK(w, sp)
		return
	}

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

// resolveMaterialContents resolves browser-supplied material identities onto
// tenant-scoped, owner-authorized raw contents (resolvedMaterial in
// governed_runners.go). The research executors read these bytes by
// material_ref from the content store — no external download, no
// client-controlled path.
func (s *Server) resolveMaterialContents(parent context.Context, userID string, refs []writingtransport.MaterialReference) ([]resolvedMaterial, error) {
	if s.kbMgr == nil || strings.TrimSpace(userID) == "" {
		return nil, fmt.Errorf("material store unavailable")
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	result := make([]resolvedMaterial, 0, len(refs))
	seen := map[string]struct{}{}
	for _, ref := range refs {
		if strings.TrimSpace(ref.MaterialID) == "" {
			return nil, fmt.Errorf("material id is required")
		}
		if _, duplicate := seen[ref.MaterialID]; duplicate {
			continue
		}
		seen[ref.MaterialID] = struct{}{}
		material, err := s.kbMgr.GetMaterial(ctx, userID, ref.MaterialID)
		if err != nil || material == nil || material.Status != "active" || material.DocID == "" {
			return nil, fmt.Errorf("material not found")
		}
		expectedRef := "kb://documents/" + material.DocID
		if ref.SourceRef != "" && ref.SourceRef != expectedRef {
			return nil, fmt.Errorf("material source reference changed")
		}
		document, err := s.kbMgr.GetDocument(ctx, userID, material.DocID)
		if err != nil || document == nil || strings.TrimSpace(document.Content) == "" {
			return nil, fmt.Errorf("material content unavailable")
		}
		content := []byte(document.Content)
		sum := sha256.Sum256(content)
		result = append(result, resolvedMaterial{MaterialID: material.ID, Title: material.Title,
			SourceRef: expectedRef, MediaType: "text/plain", Content: content, contentSum: sum})
	}
	return result, nil
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
		return steps.NewPostReviewStepWithSearch(llm, &sensitiveCheckAdapter{svc: s.sensitiveSvc}, p, s.search)
	}
	return steps.NewPostReviewStepWithSearch(llm, nil, p, s.search)
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
			Repeatable: false,
			Terminal:   false,
			Category:   "planning",
		},
	)

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.RegisterWithDescriptor(
			engine.NewStepTool(
				steps.NewMemoryGateStep(s.memoryPort()),
				"记忆门控：检索用户写作偏好记忆，注入到执行上下文",
				false,
			),
			engine.ToolDescriptor{
				DependsOn:  nil,
				Repeatable: false,
				Terminal:   false,
				Category:   "memory",
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
			Repeatable: true,
			Terminal:   true,
			Category:   "writing",
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
			Repeatable: false,
			Terminal:   false,
			Category:   "retrieval",
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
			Repeatable: true,
			Terminal:   false,
			Category:   "retrieval",
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
			Repeatable: true,
			Terminal:   false,
			Category:   "retrieval",
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
			Repeatable: true,
			Terminal:   false,
			Category:   "retrieval",
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
				Repeatable: false,
				Terminal:   false,
				Category:   "planning",
			},
		)
	}

	// Build KB searcher based on execCtx.KBEnabled (defaults to true)
	registryKB := tools.KnowledgeSearcher(services.NewKbSearchAdapter(s.kbMgr))
	if execCtx.KBEnabled != nil && !*execCtx.KBEnabled {
		registryKB = nil
	}

	registry.RegisterWithDescriptor(
		engine.NewStepTool(
			steps.NewWriteStepWithKB(llmClient, styleProfile, s.search, registryKB),
			"文章生成：按风格 Profile 生成文章，支持流式输出和 Agent Loop",
			true, // terminal — article is produced
		),
		engine.ToolDescriptor{
			DependsOn:  nil,
			Repeatable: true,
			Terminal:   true,
			Category:   "writing",
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
			Repeatable: true,
			Terminal:   false,
			Category:   "review",
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
			Repeatable: true,
			Terminal:   false,
			Category:   "review",
		},
	)

	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		registry.RegisterWithDescriptor(
			engine.NewStepTool(
				steps.NewMemoryExtractStep(s.memoryPort()),
				"记忆提取：从文章和反馈中异步提取写作偏好模式",
				false,
			),
			engine.ToolDescriptor{
				DependsOn:  nil,
				Repeatable: false,
				Terminal:   false,
				Category:   "memory",
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
		ID              string  `json:"id"`
		ModelName       string  `json:"model_name"`
		DisplayName     string  `json:"display_name"`
		Provider        string  `json:"provider"`
		IsDefault       bool    `json:"is_default"`
		HasAPIKey       bool    `json:"has_api_key"`
		PointsPerKToken float64 `json:"points_per_k_token"`
		CostLevel       string  `json:"cost_level"`
	}

	var models []modelInfo
	for _, c := range configs {
		if c.IsActive {
			mi := modelInfo{
				ID:          c.ID,
				ModelName:   c.ModelName,
				DisplayName: c.DisplayName,
				Provider:    c.Provider,
				IsDefault:   c.IsDefault,
				HasAPIKey:   c.HasAPIKey,
			}
			// 查询模型的点数费率信息
			if s.pointCalc != nil {
				if info, err := s.pointCalc.GetModelPointInfo(r.Context(), c.ModelName); err == nil && info != nil {
					mi.PointsPerKToken = info.PointsPerKToken
					mi.CostLevel = info.CostLevel
				}
			}
			models = append(models, mi)
		}
	}
	if models == nil {
		models = []modelInfo{}
	}
	response.OK(w, map[string]interface{}{"models": models})
}

