package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	ws "github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/writing-agent-v2/internal/config"
	"github.com/luminbuddy/writing-agent-v2/internal/database"
	"github.com/luminbuddy/writing-agent-v2/internal/engine"
	"github.com/luminbuddy/writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/writing-agent-v2/internal/profile"
	"github.com/luminbuddy/writing-agent-v2/internal/services"
	"github.com/luminbuddy/writing-agent-v2/internal/tools"
	"github.com/luminbuddy/writing-agent-v2/internal/websocket"
	memsvc "github.com/luminbuddy/writing-agent-v2/internal/memory"
	"github.com/luminbuddy/writing-agent-v2/pkg/crypto"
	"github.com/luminbuddy/writing-agent-v2/pkg/response"
)

// Server holds all application dependencies.
type Server struct {
	cfg           *config.Config
	hub           *websocket.Hub
	sseHub        *SSEHub
	rateLimiter   *RateLimiter
	llm           *tools.LLMClient
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
}

// New creates a new Server.
func New(cfg *config.Config) *Server {
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

	searchClient := tools.NewSearchClient(
		cfg.Tavily.APIKey, cfg.Tavily.Endpoint, cfg.Tavily.Timeout,
		cfg.Zhihu.Enabled, cfg.Zhihu.BaseURL, cfg.Zhihu.AccessSecret, cfg.Zhihu.Timeout,
		cfg.IMA.BaseURL, cfg.IMA.ClientID, cfg.IMA.APIKey, cfg.IMA.KBID, cfg.IMA.Timeout,
		cfg.Tencent.Enabled, cfg.Tencent.BaseURL, cfg.Tencent.Timeout,
		cfg.Weibo.Enabled, cfg.Weibo.BaseURL, cfg.Weibo.Timeout,
	)
	if !searchClient.HasSources() {
		slog.Warn("no search sources configured")
	}

	// Create embedding client (Dashscope)
	embeddingClient := tools.NewEmbeddingClient(cfg.Dashscope.APIKey, cfg.Dashscope.Model, cfg.Dashscope.Dimension)
	if embeddingClient.IsConfigured() {
		slog.Info("embedding client configured", "model", cfg.Dashscope.Model, "dimension", cfg.Dashscope.Dimension)
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
			slog.Warn("database migration failed", "error", err)
		}
	}

	profileLoader := profile.NewLoader()

	// If DB is available, load profiles from DB (seeds built-in if empty)
	if dbAvail {
		profileLoader.WithDB(db)
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
		slog.Info("jiaozhen fact-checking enabled", "cli_path", cfg.Jiaozhen.CLIPath)
	}

	// Create memory service (optional, requires DB + LLM + Embedding)
	var memorySvc *memsvc.Service
	if dbAvail {
		memorySvc = memsvc.NewService(db, llm, embeddingClient)
	}

	return &Server{
		cfg:           cfg,
		hub:           websocket.NewHub(),
		sseHub:        NewSSEHub(),
		rateLimiter:   rateLimiter,
		llm:           llm,
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
	}
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
		// Styles
		r.Get("/styles", s.handleListStyles)
		r.Get("/styles/{slug}", s.handleGetStyle)
		r.Post("/styles/{slug}/publish", s.handlePublishStyle)

		// Topics
		r.Get("/topics", s.handleListTopics)
		r.Post("/topics", s.handleCreateTopic)

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

		// Workbuddy Adoption Callback
		r.Post("/workbuddy/adopt", s.handleWorkbuddyAdoption)
		r.Get("/workbuddy/adoptions/{traceId}", s.handleAdoptionHistory)

		// User Reputation
		r.Get("/reputation/{userId}", s.handleGetReputation)
		r.Post("/reputation/{userId}/recalculate", s.handleRecalculateReputation)
		r.Get("/reputation/{userId}/history", s.handleReputationHistory)

		// Knowledge Base (Semantic Search)
		r.Get("/kb", s.handleKBList)
		r.Post("/kb", s.handleKBAdd)
		r.Delete("/kb/{id}", s.handleKBDelete)
		r.Post("/kb/search", s.handleKBSemanticSearch)

		// Evaluation
		r.Get("/evaluation/sets", s.handleListEvalSets)
		r.Post("/evaluation/sets", s.handleCreateEvalSet)
		r.Get("/evaluation/sets/{id}", s.handleGetEvalSet)
		r.Post("/evaluation/sets/{id}/samples", s.handleAddEvalSamples)
		r.Get("/evaluation/sets/{id}/samples", s.handleListEvalSamples)
		r.Post("/evaluation/runs", s.handleCreateEvalRun)
		r.Get("/evaluation/runs", s.handleListEvalRuns)
		r.Get("/evaluation/runs/{id}", s.handleGetEvalRun)
r.Get("/evaluation/runs/{id}/export/{format}", s.handleExportEvalRun)

		// WebSocket
		r.Get("/ws/agent", s.handleWebSocket)

		// SSE (Server-Sent Events)
		r.Get("/sse/topics", s.handleSSETopics)
		r.Get("/sse/stats", s.handleSSEStats)

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

		// Admin (protected by admin token)
		r.Route("/admin", func(r chi.Router) {
			r.Use(s.adminAuthMiddleware)

			// Dashboard stats
			r.Get("/stats", s.handleAdminStats)

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
			r.Get("/styles/{slug}/versions/compare", s.handleAdminCompareVersions)

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

	// Start cron scheduler
	if s.cronScheduler != nil {
		go s.cronScheduler.Start(ctx, s.executeCronJob)
	}

	slog.Info("server starting", "addr", s.cfg.ListenAddr())
	return srv.ListenAndServe()
}

// ─── Handlers ────────────────────────────────────────────

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{
		"status":            "ok",
		"version":           "v2",
		"llm_configured":    s.llm != nil,
		"search_configured": s.search != nil && s.search.HasSources(),
		"db_configured":     s.dbAvail,
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

	if s.traces != nil && len(req.Segments) > 0 {
		s.traces.SaveFeedback(r.Context(), req.TraceID, req.Segments)
	}

	slog.Info("feedback received", "trace_id", req.TraceID, "segments", len(req.Segments))
	response.OK(w, map[string]interface{}{"received": true})
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

	traceID := engine.GenerateTraceID()

	slog.Info("agent started", "trace_id", traceID, "user_id", userID, "role", userRole, "style", func() string {
		if p.Style != "" {
			return p.Style
		}
		return "yinyue"
	}())

	// Register client with trace ID
	s.hub.Register(traceID, client)

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

	// Store session
	s.sessions.Store(traceID, execCtx)

	// Persist trace to database
	if s.traces != nil {
		s.traces.CreateTrace(context.Background(), execCtx)
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

	// Create steps
	var engineSteps []engine.Step
	engineSteps = append(engineSteps,
		steps.NewIntentStep(s.llm),
	)
	
	// Memory gate: retrieve and gate memories after intent classification
	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		engineSteps = append(engineSteps, steps.NewMemoryGateStep(s.memorySvc))
	}

	engineSteps = append(engineSteps,
		steps.NewQueryPlanStep(s.llm),
		steps.NewSearchStep(s.llm, s.search),
		steps.NewRelevanceStepWithEmbedding(s.embedding),
	)
	if execCtx.Mode == "guided" {
		engineSteps = append(engineSteps, steps.NewOutlineStep(s.llm))
	}

	// Load style profile for WriteStep
	var styleProfile *profile.StyleProfile
	if s.profiles != nil {
		if p, ok := s.profiles.Get(execCtx.StyleSlug); ok {
			styleProfile = p
		}
	}

	engineSteps = append(engineSteps,
		steps.NewWriteStepWithProfile(s.llm, styleProfile),
		s.newPostReviewStep(),
		steps.NewAutoFixStep(s.llm),
	)

	// Memory extract: extract patterns after article completion (async)
	if s.memorySvc != nil && s.memorySvc.IsAvailable() {
		engineSteps = append(engineSteps, steps.NewMemoryExtractStep(s.memorySvc))
	}

	// Create and run engine
	eng := engine.NewAgentEngine(emitter, engineSteps)

	// Run in background
	go func() {
		ctx := context.Background()
		start := time.Now()
		if err := eng.Run(ctx, execCtx); err != nil {
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
		TraceID: traceID,
		Status:  status,
		Step:    currentStep,
		Article: execCtx.Article,
		Style:   execCtx.StyleSlug,
		Mode:    execCtx.Mode,
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
