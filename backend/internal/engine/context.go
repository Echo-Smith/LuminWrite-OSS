package engine

import (
	"context"
	"time"
)

// ExecutionStatus represents the current status of an execution.
type ExecutionStatus string

const (
	StatusIdle      ExecutionStatus = "idle"
	StatusRunning   ExecutionStatus = "running"
	StatusPaused    ExecutionStatus = "paused"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusCancelled ExecutionStatus = "cancelled"
)

// StepName is the identifier for a pipeline step.
type StepName string

const (
	StepIntent       StepName = "intent"
	StepMemoryGate   StepName = "memory_gate"
	StepQueryPlan    StepName = "query_plan"
	StepSearch       StepName = "search"
	StepRelevance    StepName = "relevance"
	StepOutline      StepName = "outline"
	StepWrite        StepName = "write"
	StepPostReview   StepName = "post_review"
	StepAutoFix      StepName = "auto_fix"
	StepMemoryExtract StepName = "memory_extract"
)

// StepRecord records the execution of a single step.
type StepRecord struct {
	Step        StepName        `json:"step"`
	Status      string          `json:"status"`
	StartedAt   *time.Time      `json:"started_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
	DurationMs  int64           `json:"duration_ms,omitempty"`
	Result      interface{}     `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
}

// WritingTask holds parsed writing task info.
type WritingTask struct {
	Topic              string   `json:"topic"`
	SearchQueries      []string `json:"search_queries"`
	PrimarySearchQuery string   `json:"primary_search_query"`
	WordLimit          int      `json:"word_limit"`
}

// SearchResult is a single search result from any source.
type SearchResult struct {
	Title     string  `json:"title"`
	Snippet   string  `json:"snippet"`
	URL       string  `json:"url,omitempty"`
	Source    string  `json:"source"`
	Relevance string  `json:"relevance,omitempty"` // strong | medium | weak | conflict
	Score     float64 `json:"score,omitempty"`
}

// SearchPlanEntry is a single entry in the search plan.
type SearchPlanEntry struct {
	Query  string `json:"query"`
	Source string `json:"source"` // zhihu | ima | tavily | tencent | weibo
}

// ReviewIssue is a single issue found in post-review.
type ReviewIssue struct {
	Severity string `json:"severity"` // high | medium | low
	Type     string `json:"type"`
	Message  string `json:"message"`
}

// ReviewResult holds the post-write review results.
type ReviewResult struct {
	Scores map[string]float64 `json:"scores"`
	Issues []ReviewIssue       `json:"issues"`
	Passed bool                `json:"passed"`
}

// OutlineItem is a single point in the outline.
type OutlineItem struct {
	Point string `json:"point"`
	Type  string `json:"type"` // opening | argument | conclusion
}

// OutlineData holds the outline for guided mode.
type OutlineData struct {
	Title   string        `json:"title"`
	Outline []OutlineItem `json:"outline"`
}

// ExecutionContext holds all state for a single writing execution.
type ExecutionContext struct {
	TraceID       string         `json:"trace_id"`
	UserID        string         `json:"user_id"`
	SessionID     string         `json:"session_id"`
	StyleSlug     string         `json:"style_slug"`
	Mode          string         `json:"mode"` // auto | writing | guided | polish
	UserInput     string         `json:"user_input"`
	NormalizedInput string       `json:"normalized_input"`
	UserMaterials []string       `json:"user_materials"`
	WordLimit     int            `json:"word_limit"`

	// Populated during execution
	TaskIntent    *TaskIntent     `json:"task_intent,omitempty"`
	WritingTask   *WritingTask    `json:"writing_task,omitempty"`
	SearchPlan    []SearchPlanEntry `json:"search_plan,omitempty"`
	SearchResults []SearchResult  `json:"search_results,omitempty"`
	Outline       *OutlineData    `json:"outline,omitempty"`
	Article       string         `json:"article,omitempty"`
	ReviewResult  *ReviewResult  `json:"review_result,omitempty"`

	// Memory context (populated by MemoryGateStep)
	MemoryContext interface{}    `json:"memory_context,omitempty"`

	// Execution state
	Status        ExecutionStatus `json:"status"`
	CurrentStep   StepName        `json:"current_step"`
	StepHistory   []StepRecord    `json:"step_history"`
	StartedAt     time.Time       `json:"started_at"`
	PausedAt      *time.Time      `json:"paused_at,omitempty"`

	// Token usage
	TotalTokens   int             `json:"total_tokens"`

	// Control channels
	pauseCh       chan struct{}
	resumeCh      chan struct{}
	cancelCh      chan struct{}
	confirmCh     chan map[string]interface{}
}

// TaskIntent holds the result of intent classification.
type TaskIntent struct {
	TaskMode          string  `json:"taskMode"` // writing | polish | chat | shorten | expand | extract_points
	Confidence        float64 `json:"confidence"`
	Source            string  `json:"source"` // rules | llm
	NormalizedInput   string  `json:"normalizedInput"`
}

// NewExecutionContext creates a new context for a writing execution.
func NewExecutionContext(traceID, userID, message string) *ExecutionContext {
	return &ExecutionContext{
		TraceID:     traceID,
		UserID:      userID,
		UserInput:   message,
		Status:      StatusIdle,
		StartedAt:   time.Now(),
		StepHistory: []StepRecord{},
		pauseCh:     make(chan struct{}, 1),
		resumeCh:    make(chan struct{}, 1),
		cancelCh:    make(chan struct{}, 1),
		confirmCh:   make(chan map[string]interface{}, 1),
	}
}

// Pause signals the context to pause.
func (ctx *ExecutionContext) Pause() {
	select {
	case ctx.pauseCh <- struct{}{}:
	default:
	}
}

// Resume signals the context to resume.
func (ctx *ExecutionContext) Resume() {
	select {
	case ctx.resumeCh <- struct{}{}:
	default:
	}
}

// Cancel signals the context to cancel.
func (ctx *ExecutionContext) Cancel() {
	select {
	case ctx.cancelCh <- struct{}{}:
	default:
	}
}

// ConfirmOutline sends confirmed outline data to the context.
func (ctx *ExecutionContext) ConfirmOutline(data map[string]interface{}) {
	select {
	case ctx.confirmCh <- data:
	default:
	}
}

// IsCancelled checks if the context has been cancelled.
func (ctx *ExecutionContext) IsCancelled() bool {
	select {
	case <-ctx.cancelCh:
		return true
	default:
		return false
	}
}

// CheckPause checks if a pause has been requested and blocks until resumed.
// If a pause occurs, it emits Paused/Resumed events via the emitter.
func (ctx *ExecutionContext) CheckPause(ctxGo context.Context, emitter EventEmitter, step StepName) error {
	select {
	case <-ctx.pauseCh:
		now := time.Now()
		ctx.PausedAt = &now
		ctx.Status = StatusPaused
		if emitter != nil {
			emitter.Paused(step, nil)
		}
		// Wait for resume or cancel
		select {
		case <-ctx.resumeCh:
			ctx.Status = StatusRunning
			ctx.PausedAt = nil
			if emitter != nil {
				emitter.Resumed(step)
			}
		case <-ctx.cancelCh:
			return context.Canceled
		case <-ctxGo.Done():
			return ctxGo.Err()
		}
	case <-ctx.cancelCh:
		return context.Canceled
	case <-ctxGo.Done():
		return ctxGo.Err()
	default:
	}
	return nil
}

// WaitForConfirm blocks until the user confirms the outline.
func (ctx *ExecutionContext) WaitForConfirm(ctxGo context.Context) (map[string]interface{}, error) {
	select {
	case data := <-ctx.confirmCh:
		return data, nil
	case <-ctx.cancelCh:
		return nil, context.Canceled
	case <-ctxGo.Done():
		return nil, ctxGo.Err()
	}
}
