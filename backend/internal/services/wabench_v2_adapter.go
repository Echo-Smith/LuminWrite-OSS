package services

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	pkgmemory "github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

const LuminbuddyV2AdapterID = "luminbuddy-v2"

var (
	builtinStyleRefPattern = regexp.MustCompile(`^luminbuddy\.builtin-style\.([a-z0-9_-]+)$`)
	legacyStyleRefPattern  = regexp.MustCompile(`^luminbuddy\.legacy-style\.([a-z0-9_-]+)$`)
	userStyleRefPattern    = regexp.MustCompile(`^luminbuddy\.user-style\.([a-f0-9]{32})\.v([1-9][0-9]*)$`)
)

var publicWABenchStyleRefs = map[string]string{
	"wabench.public.general-writing": "yinyue",
	"wabench.public.deep-commentary": "yinyue",
	"wabench.public.policy-essay":    "shenlun",
	"wabench.public.social-note":     "xiaohongshu",
}

type WABenchAgentRequest struct {
	RunID         string
	Input         string
	Case          database.WABenchCase
	Candidate     database.WABenchCandidate
	FrozenSources []database.WABenchSourceFixture
}

type WABenchToolEvent struct {
	Step       string                 `json:"step"`
	Status     string                 `json:"status"`
	DurationMs int64                  `json:"durationMs"`
	Error      string                 `json:"error,omitempty"`
	Evidence   map[string]interface{} `json:"evidence,omitempty"`
}

type WABenchAgentTrace struct {
	TraceID            string
	Article            string
	ArticleTitle       string
	Status             engine.ExecutionStatus
	TaskIntent         string
	TotalTokens        int
	LatencyMs          int64
	ToolEvents         []WABenchToolEvent
	SearchResults      []engine.SearchResult
	WebSearchTriggered bool
	KnowledgeTriggered bool
	KnowledgeProviders []string
	StepHistory        []engine.StepRecord
	TracePersisted     bool
}

type WABenchAgentExecutor interface {
	Execute(context.Context, WABenchAgentRequest) (*WABenchAgentTrace, error)
}

type WABenchLLMResolver interface {
	GetClient(context.Context, string) *tools.LLMClient
}

type staticWABenchLLMResolver struct {
	client *tools.LLMClient
}

func (r staticWABenchLLMResolver) GetClient(context.Context, string) *tools.LLMClient {
	return r.client
}

type HarnessWABenchExecutor struct {
	llmResolver  WABenchLLMResolver
	search       *tools.SearchClient
	kb           tools.KnowledgeSearcher
	profiles     *profile.Loader
	userStyles   *database.UserStyleStore
	sessionStore agent.SessionStore
	traces       *database.TraceRepo
}

func NewHarnessWABenchExecutor(
	llm *tools.LLMClient,
	search *tools.SearchClient,
	kb tools.KnowledgeSearcher,
	profiles *profile.Loader,
	userStyles *database.UserStyleStore,
	sessionStore agent.SessionStore,
	traces *database.TraceRepo,
) *HarnessWABenchExecutor {
	return NewHarnessWABenchExecutorWithResolver(
		staticWABenchLLMResolver{client: llm}, search, kb, profiles,
		userStyles, sessionStore, traces,
	)
}

func NewHarnessWABenchExecutorWithResolver(
	llmResolver WABenchLLMResolver,
	search *tools.SearchClient,
	kb tools.KnowledgeSearcher,
	profiles *profile.Loader,
	userStyles *database.UserStyleStore,
	sessionStore agent.SessionStore,
	traces *database.TraceRepo,
) *HarnessWABenchExecutor {
	return &HarnessWABenchExecutor{
		llmResolver: llmResolver, search: search, kb: kb, profiles: profiles,
		userStyles: userStyles, sessionStore: sessionStore, traces: traces,
	}
}

func boolFeature(flags map[string]interface{}, key string, fallback bool) bool {
	value, ok := flags[key]
	if !ok {
		return fallback
	}
	result, ok := value.(bool)
	if !ok {
		return fallback
	}
	return result
}

func stringFeature(flags map[string]interface{}, key string) string {
	value, _ := flags[key].(string)
	return strings.TrimSpace(value)
}

func contextString(contextData map[string]interface{}, key string) string {
	value, _ := contextData[key].(string)
	return value
}

func contextBool(contextData map[string]interface{}, key string) bool {
	value, _ := contextData[key].(bool)
	return value
}

func manifestModelName(manifest map[string]interface{}, key string) string {
	value, _ := manifest[key].(string)
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	if key == "model" {
		value, _ = manifest["modelName"].(string)
	}
	return strings.TrimSpace(value)
}

func (e *HarnessWABenchExecutor) resolveProfile(ctx context.Context, refs []string) (*profile.StyleProfile, error) {
	for _, ref := range refs {
		if slug, ok := publicWABenchStyleRefs[ref]; ok {
			if e.profiles == nil {
				return nil, fmt.Errorf("style profile loader is unavailable")
			}
			if result, exists := e.profiles.Get(slug); exists {
				return result, nil
			}
			return nil, fmt.Errorf("public WABench style profile %s is unavailable", slug)
		}
		if matches := builtinStyleRefPattern.FindStringSubmatch(ref); len(matches) == 2 {
			if e.profiles == nil {
				return nil, fmt.Errorf("style profile loader is unavailable")
			}
			if result, ok := e.profiles.Get(matches[1]); ok {
				return result, nil
			}
			return nil, fmt.Errorf("builtin style profile %s not found", matches[1])
		}
		if matches := legacyStyleRefPattern.FindStringSubmatch(ref); len(matches) == 2 {
			if e.profiles != nil {
				if result, ok := e.profiles.Get(matches[1]); ok {
					return result, nil
				}
			}
			return nil, fmt.Errorf("legacy style %s must be rebound before evaluation", matches[1])
		}
		if matches := userStyleRefPattern.FindStringSubmatch(ref); len(matches) == 3 {
			if e.userStyles == nil {
				return nil, fmt.Errorf("user style store is unavailable")
			}
			profileID, err := uuid.Parse(matches[1])
			if err != nil {
				return nil, fmt.Errorf("invalid user style profile reference %s: %w", ref, err)
			}
			version, err := strconv.Atoi(matches[2])
			if err != nil {
				return nil, fmt.Errorf("invalid user style version in %s: %w", ref, err)
			}
			snapshot, err := e.userStyles.GetVersionByNumber(ctx, profileID.String(), version)
			if err != nil {
				return nil, err
			}
			var result profile.StyleProfile
			if err := json.Unmarshal([]byte(snapshot.Config), &result); err != nil {
				return nil, fmt.Errorf("decode user style %s: %w", ref, err)
			}
			result.Version = version
			if err := profile.ValidateProfile(&result); err != nil {
				return nil, fmt.Errorf("invalid user style %s: %w", ref, err)
			}
			return &result, nil
		}
	}
	return nil, fmt.Errorf("case has no resolvable rule profile reference")
}

func (e *HarnessWABenchExecutor) Execute(ctx context.Context, request WABenchAgentRequest) (*WABenchAgentTrace, error) {
	if e == nil || e.llmResolver == nil {
		return nil, fmt.Errorf("WABench V2 adapter requires an LLM client")
	}
	llm := e.llmResolver.GetClient(ctx, manifestModelName(request.Candidate.ModelManifest, "model"))
	if llm == nil {
		return nil, fmt.Errorf("WABench candidate model is unavailable")
	}
	styleProfile, err := e.resolveProfile(ctx, request.Case.RuleProfileRefs)
	if err != nil {
		return nil, err
	}
	traceID := "wabe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	userID := "anonymous"
	memoryEnabled := boolFeature(request.Candidate.FeatureFlags, "memoryEnabled", false)
	if memoryEnabled {
		userID = stringFeature(request.Candidate.FeatureFlags, "memoryUserId")
		if _, err := uuid.Parse(userID); err != nil {
			return nil, fmt.Errorf("memoryEnabled requires a valid frozen memoryUserId")
		}
	}

	execCtx := engine.NewExecutionContext(traceID, userID, request.Input)
	execCtx.StyleSlug = styleProfile.Slug
	execCtx.Mode = "auto"
	execCtx.AgentMode = "harness"
	execCtx.ConversationID = request.RunID + ":" + request.Case.CaseID
	execCtx.SessionID = request.RunID
	execCtx.MaxLLMFails = 2

	writingSession := agent.NewWritingSession(execCtx.ConversationID, userID, styleProfile.Slug)
	if request.Case.TaskType == "polish" || request.Case.TaskType == "dedupe" {
		writingSession.CurrentArticle = contextString(request.Case.Context, "article")
	}
	for _, fixture := range request.FrozenSources {
		writingSession.SearchResults = append(writingSession.SearchResults, engine.SearchResult{
			Title: fixture.Title, Snippet: fixture.ExcerptText, URL: fixture.SourceRef,
			Source: "frozen_fixture:" + fixture.Provider,
		})
	}

	var sessionStore agent.SessionStore
	if memoryEnabled {
		sessionStore = readOnlyWABenchSessionStore{inner: e.sessionStore}
	}
	search := e.search
	kb := e.kb
	if request.Case.SourceMode == "frozen" {
		search = nil
		kb = nil
	} else if contextBool(request.Case.Context, "knowledgeOnly") {
		search = nil
	}
	emitter := newWABenchCaptureEmitter()
	harness := agent.NewHarness(llm, search, kb, styleProfile, sessionStore, emitter)

	if e.traces != nil {
		persisted := *execCtx
		persisted.UserInput = "[WABench private input " + request.Case.InputHash + "]"
		if err := e.traces.CreateTrace(ctx, &persisted); err != nil {
			return nil, fmt.Errorf("create WABench agent trace: %w", err)
		}
		execCtx.Status = engine.StatusIdle
	}
	started := time.Now()
	err = harness.Run(ctx, execCtx, writingSession)
	latency := time.Since(started).Milliseconds()
	if e.traces != nil {
		_ = e.traces.UpdateTraceStep(ctx, execCtx)
		_ = e.traces.CompleteTrace(ctx, execCtx)
		if err != nil {
			_ = e.traces.FailTrace(ctx, traceID, err.Error())
		}
	}

	events, emitterErrors := emitter.snapshot()
	trace := &WABenchAgentTrace{
		TraceID: traceID, Article: execCtx.Article, ArticleTitle: execCtx.ArticleTitle,
		Status: execCtx.Status, TotalTokens: execCtx.TotalTokens, LatencyMs: latency,
		ToolEvents: events, SearchResults: append([]engine.SearchResult(nil), writingSession.SearchResults...),
		StepHistory:    append([]engine.StepRecord(nil), execCtx.StepHistory...),
		TracePersisted: e.traces != nil,
	}
	if execCtx.TaskIntent != nil {
		trace.TaskIntent = execCtx.TaskIntent.TaskMode
	}
	providers := map[string]bool{}
	for _, event := range events {
		switch event.Step {
		case "search_web":
			trace.WebSearchTriggered = true
		case "search_knowledge":
			trace.KnowledgeTriggered = true
		}
	}
	for _, result := range trace.SearchResults {
		if strings.HasPrefix(result.Source, "local_kb") || strings.HasPrefix(result.Source, "frozen_fixture:") {
			provider := strings.TrimPrefix(result.Source, "frozen_fixture:")
			if provider == "local_kb" || provider == "" {
				provider = "luminbuddy-local-kb"
			}
			providers[provider] = true
		}
	}
	for provider := range providers {
		trace.KnowledgeProviders = append(trace.KnowledgeProviders, provider)
	}
	sort.Strings(trace.KnowledgeProviders)
	if err == nil && len(emitterErrors) > 0 {
		err = fmt.Errorf("agent emitted error: %s", strings.Join(emitterErrors, "; "))
	}
	return trace, err
}

// readOnlyWABenchSessionStore allows an explicitly opted-in candidate to read
// its frozen user's memory without writing evaluation messages back into the
// user's conversation or changing subsequent benchmark cases.
type readOnlyWABenchSessionStore struct {
	inner agent.SessionStore
}

func (s readOnlyWABenchSessionStore) LoadHistory(ctx context.Context, conversationID string, limit int) ([]pkgmemory.ConversationMessage, error) {
	if s.inner == nil {
		return nil, nil
	}
	return s.inner.LoadHistory(ctx, conversationID, limit)
}

func (s readOnlyWABenchSessionStore) StoreMessage(context.Context, *pkgmemory.ConversationMessage) error {
	return nil
}

func (s readOnlyWABenchSessionStore) IsEnabledForUser(userID string) bool {
	return s.inner != nil && s.inner.IsEnabledForUser(userID)
}

func (s readOnlyWABenchSessionStore) Retrieve(ctx context.Context, req pkgmemory.RetrieveRequest) (*pkgmemory.MemoryContext, error) {
	if retriever, ok := s.inner.(agent.MemoryRetriever); ok {
		return retriever.Retrieve(ctx, req)
	}
	return nil, nil
}

type wabenchCaptureEmitter struct {
	mu     sync.Mutex
	events []WABenchToolEvent
	errors []string
}

func newWABenchCaptureEmitter() *wabenchCaptureEmitter {
	return &wabenchCaptureEmitter{}
}

func (e *wabenchCaptureEmitter) StepStart(step engine.StepName, stepIndex int) {}

func (e *wabenchCaptureEmitter) StepComplete(step engine.StepName, result interface{}, durationMs int64) {
	e.mu.Lock()
	defer e.mu.Unlock()
	evidence := map[string]interface{}{}
	if value, ok := result.(map[string]interface{}); ok {
		evidence = value
	}
	status := "complete"
	errorMessage, _ := evidence["error"].(string)
	if errorMessage != "" {
		status = "error"
	}
	e.events = append(e.events, WABenchToolEvent{
		Step: string(step), Status: status, DurationMs: durationMs,
		Error: errorMessage, Evidence: evidence,
	})
}

func (e *wabenchCaptureEmitter) StreamDelta(string)                                          {}
func (e *wabenchCaptureEmitter) StreamReset()                                                {}
func (e *wabenchCaptureEmitter) ReasoningDelta(string)                                       {}
func (e *wabenchCaptureEmitter) ArticleTitle(string)                                         {}
func (e *wabenchCaptureEmitter) StreamDone(string)                                           {}
func (e *wabenchCaptureEmitter) AwaitInput(engine.StepName, interface{}, []string, int, int) {}
func (e *wabenchCaptureEmitter) Paused(engine.StepName, interface{})                         {}
func (e *wabenchCaptureEmitter) PausedWithReason(engine.StepName, interface{}, string)       {}
func (e *wabenchCaptureEmitter) Resumed(engine.StepName)                                     {}

func (e *wabenchCaptureEmitter) Error(code, message string, step engine.StepName) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.errors = append(e.errors, fmt.Sprintf("%s:%s:%s", step, code, message))
}

func (e *wabenchCaptureEmitter) Completed(string, string, interface{}, interface{}) {}
func (e *wabenchCaptureEmitter) Cancelled()                                         {}
func (e *wabenchCaptureEmitter) Compaction(int, int, string)                        {}

func (e *wabenchCaptureEmitter) snapshot() ([]WABenchToolEvent, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]WABenchToolEvent(nil), e.events...), append([]string(nil), e.errors...)
}
