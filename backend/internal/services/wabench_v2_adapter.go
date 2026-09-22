package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

const LuminbuddyV2AdapterID = "luminbuddy-v2"

var (
	builtinStyleRefPattern = regexp.MustCompile(`^luminbuddy\.builtin-style\.([a-z0-9_-]+)$`)
	legacyStyleRefPattern  = regexp.MustCompile(`^luminbuddy\.legacy-style\.([a-z0-9_-]+)$`)
	userStyleRefPattern    = regexp.MustCompile(`^luminbuddy\.user-style\.([a-f0-9]{32})\.v([1-9][0-9]*)$`)
)

var publicWABenchStyleRefs = map[string]string{
	"wabench.public.general-writing": "default",
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

// HarnessWABenchExecutor runs WABench candidates through the harness
// execution CORE (RunCore): frozen inputs, frozen config, one isolated
// session per case, provisional output only. It owns no session
// persistence and no legacy trace lifecycle (⑥B) — run identity, timing
// and evaluation evidence belong to WABench itself.
//
// Feature flag matrix for WP3 A/B/C/D candidates:
//   - A: memoryEnabled=false, contextCompilerEnabled=false, projectMemoryEnabled=false — baseline
//   - B: memoryEnabled=false, contextCompilerEnabled=true,  projectMemoryEnabled=false — context only
//   - C: memoryEnabled=true,  contextCompilerEnabled=true,  projectMemoryEnabled=false — context + user memory
//   - D: memoryEnabled=true,  contextCompilerEnabled=true,  projectMemoryEnabled=true  — context + user memory + project memory
type HarnessWABenchExecutor struct {
	llmResolver WABenchLLMResolver
	search      *tools.SearchClient
	kb          tools.KnowledgeSearcher
	profiles    *profile.Loader
	userStyles  *database.UserStyleStore
	// memoryPort is the memory consumption contract for read-only injection.
	// nil disables memory injection entirely (candidates A and B).
	// WABench must NOT call SubmitOutcome — memory is strictly read-only.
	memoryPort memoryport.Port
}

func NewHarnessWABenchExecutor(
	llm *tools.LLMClient,
	search *tools.SearchClient,
	kb tools.KnowledgeSearcher,
	profiles *profile.Loader,
	userStyles *database.UserStyleStore,
	memoryPort memoryport.Port,
) *HarnessWABenchExecutor {
	return NewHarnessWABenchExecutorWithResolver(
		staticWABenchLLMResolver{client: llm}, search, kb, profiles, userStyles, memoryPort,
	)
}

func NewHarnessWABenchExecutorWithResolver(
	llmResolver WABenchLLMResolver,
	search *tools.SearchClient,
	kb tools.KnowledgeSearcher,
	profiles *profile.Loader,
	userStyles *database.UserStyleStore,
	memoryPort memoryport.Port,
) *HarnessWABenchExecutor {
	return &HarnessWABenchExecutor{
		llmResolver: llmResolver, search: search, kb: kb, profiles: profiles,
		userStyles: userStyles, memoryPort: memoryPort,
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
	contextCompilerEnabled := boolFeature(request.Candidate.FeatureFlags, "contextCompilerEnabled", false)
	projectMemoryEnabled := boolFeature(request.Candidate.FeatureFlags, "projectMemoryEnabled", false)
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

	// ── Memory injection (read-only) ──
	// When memoryEnabled=true, prepare a read-only memory injection via the
	// memory consumption contract. WABench must NOT call SubmitOutcome —
	// memory is strictly read-only for evaluation purposes.
	if memoryEnabled && e.memoryPort != nil {
		memReq := memoryport.Request{
			UserID:    userID,
			SessionID: execCtx.SessionID,
			TraceID:   traceID,
			Query:     request.Input,
			Source:    "wabench",
		}
		bundle, memErr := e.memoryPort.PrepareInjection(ctx, memReq)
		if memErr != nil {
			slog.Warn("WABench memory injection failed, continuing without memory",
				"trace_id", traceID, "error", memErr)
		} else if bundle != nil && !bundle.IsEmpty() {
			execCtx.MemoryContext = bundle
		}
	}

	// ── Context compiler ──
	// When contextCompilerEnabled=true, compile a context envelope from the
	// case data and inject blocks into the execution context. This mirrors
	// the EngineStepRunner pattern in writingruntime/executor_adapters.go.
	if contextCompilerEnabled {
		ccInput := buildWABenchContextInput(request, projectMemoryEnabled)
		envelope, ccErr := contextcompiler.Compile(ccInput)
		if ccErr != nil {
			slog.Warn("WABench context compilation failed, continuing without envelope",
				"trace_id", traceID, "error", ccErr)
		} else {
			injectContextEnvelope(execCtx, &envelope)
		}
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
	// RunCore (not Harness.Run): the evaluation executor owns its run
	// identity, frozen sources, candidate config, timing and output — it
	// must not drive session persistence, memory writes, or the legacy
	// agent_traces lifecycle. RunCore produces provisional output only;
	// every authoritative commit stays with WABench itself (⑥B).
	harness := agent.NewHarness(llm, search, kb, styleProfile)

	started := time.Now()
	coreOutput, runErr := harness.RunCore(ctx, execCtx, writingSession)
	latency := time.Since(started).Milliseconds()
	err = runErr
	// RunCore never touches execCtx.Status terminally; map the provisional
	// output onto the trace WABench persists itself.
	if err == nil {
		execCtx.Article, execCtx.ArticleTitle = coreOutput.Article, coreOutput.ArticleTitle
		execCtx.TotalTokens = coreOutput.TotalTokens
		writingSession.SearchResults = coreOutput.SearchResults
		execCtx.Status = engine.StatusCompleted
	} else {
		execCtx.Status = engine.StatusFailed
	}

	events, emitterErrors := emitter.snapshot()
	trace := &WABenchAgentTrace{
		TraceID: traceID, Article: execCtx.Article, ArticleTitle: execCtx.ArticleTitle,
		Status: execCtx.Status, TotalTokens: execCtx.TotalTokens, LatencyMs: latency,
		ToolEvents: events, SearchResults: append([]engine.SearchResult(nil), writingSession.SearchResults...),
		StepHistory: append([]engine.StepRecord(nil), execCtx.StepHistory...),
		// The executor no longer writes the legacy agent_traces lifecycle
		// (⑥B); WABench persists its own run/evidence records.
		TracePersisted: false,
	}
	if execCtx.TaskIntent != nil {
		trace.TaskIntent = execCtx.TaskIntent.TaskMode
	}
	// Tool-level observability without the harness emitter: RunCore never
	// emits events (⑥B — the governed core produces provisional output
	// only), so retrieval signals are inferred from the captured search
	// results instead of emitter ToolEvents.
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
		if strings.HasPrefix(result.Source, "local_kb") {
			trace.KnowledgeTriggered = true
		}
		if strings.HasPrefix(result.Source, "local_kb") || strings.HasPrefix(result.Source, "frozen_fixture:") {
			provider := strings.TrimPrefix(result.Source, "frozen_fixture:")
			if provider == "local_kb" || provider == "" {
				provider = "local-pg-kb"
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

// buildWABenchContextInput constructs a minimal contextcompiler.Input from
// WABench case data. The "document" is the case input text; the "contract"
// is the case metadata (task type, difficulty, case ID). When
// projectMemoryEnabled is true, the Wanted list includes project-memory
// blocks — but since WABench has no project store, those blocks will land
// in the envelope's Missing channel (auditable, not fatal).
func buildWABenchContextInput(request WABenchAgentRequest, projectMemoryEnabled bool) contextcompiler.Input {
	input := contextcompiler.Input{
		ContractDigest: fmt.Sprintf("wabench.case %s | task %s | difficulty %s",
			request.Case.CaseID, request.Case.TaskType, request.Case.Difficulty),
		DocumentState: request.Input,
	}
	// The draft capability's context contract (core.draft.generate):
	//   required: contract_digest, document_state
	//   optional: through_line_anchor, canon_facts, terminology, open_decisions,
	//             entities_cards, source_evidence, style_directives, review_guard
	input.Wanted = []string{
		"contract_digest",
		"document_state",
	}
	if projectMemoryEnabled {
		// When project memory is enabled, request the blocks that would
		// normally come from the project store. In WABench there is no
		// real project, so these will appear as Missing entries in the
		// envelope — which is the correct audit trail.
		input.Wanted = append(input.Wanted,
			"through_line_anchor",
			"canon_facts",
			"terminology",
			"open_decisions",
			"entities_cards",
			"source_evidence",
		)
	}
	return input
}

// injectContextEnvelope maps compiled context envelope blocks onto the
// ExecutionContext fields, following the same pattern as EngineStepRunner.Run
// in writingruntime/executor_adapters.go.
func injectContextEnvelope(execCtx *engine.ExecutionContext, envelope *contextcompiler.Envelope) {
	if envelope == nil {
		return
	}
	for _, block := range envelope.Blocks {
		switch block.Name {
		case "review_guard":
			if block.Body != "" {
				execCtx.ReviewGuardLines = strings.Split(block.Body, "\n")
			}
		case "style_directives":
			if block.Body != "" {
				// Project style directives into MemoryContext as a synthetic
				// bundle so the harness's AddMemory path can consume them
				// without touching memoryport directly.
				directives := make([]memoryport.Directive, 0)
				for _, line := range strings.Split(block.Body, "\n") {
					if line != "" {
						directives = append(directives, memoryport.Directive{
							Value: line, Kind: memoryport.KindPreference,
						})
					}
				}
				if len(directives) > 0 {
					// Merge with existing MemoryContext if present (from
					// memory injection above).
					if existing, ok := execCtx.MemoryContext.(*memoryport.Bundle); ok && existing != nil {
						existing.WriteDirectives = append(existing.WriteDirectives, directives...)
					} else {
						execCtx.MemoryContext = &memoryport.Bundle{WriteDirectives: directives}
					}
				}
			}
		}
	}
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
func (e *wabenchCaptureEmitter) Compaction(int, int, string, uint64, string)        {}

func (e *wabenchCaptureEmitter) snapshot() ([]WABenchToolEvent, []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]WABenchToolEvent(nil), e.events...), append([]string(nil), e.errors...)
}
