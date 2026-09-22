package engine

import (
	"time"
)

// ─── LLM Call Tracing ────────────────────────────────────────

// LLMCallRecord captures detailed information about a single LLM API call.
// This enables cost attribution, performance analysis, and debugging of LLM interactions.
type LLMCallRecord struct {
	// Identification
	CallID string   `json:"call_id"`           // Unique identifier for this call
	Step   StepName `json:"step"`              // Which pipeline step made this call
	
	// Model configuration
	Model       string  `json:"model"`                  // e.g., "deepseek-chat", "deepseek-reasoner"
	Provider    string  `json:"provider"`               // e.g., "deepseek", "openai"
	Temperature float64 `json:"temperature,omitempty"`
	TopP        float64 `json:"top_p,omitempty"`
	MaxTokens   int     `json:"max_tokens,omitempty"`
	
	// Thinking mode (for reasoning models)
	ThinkingEnabled bool   `json:"thinking_enabled,omitempty"`
	ReasoningEffort string `json:"reasoning_effort,omitempty"` // "low" | "medium" | "high" | "max"
	
	// Token usage
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	ReasoningTokens  int `json:"reasoning_tokens,omitempty"` // Thinking mode reasoning tokens
	TotalTokens      int `json:"total_tokens"`
	CacheHitTokens   int `json:"cache_hit_tokens,omitempty"`  // prompt cache read
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"` // prompt cache write
	CacheMissTokens  int `json:"cache_miss_tokens,omitempty"`  // non-cached prompt tokens
	
	// Timing
	StartedAt   time.Time      `json:"started_at"`
	TTFT        *time.Duration `json:"ttft,omitempty"`        // Time To First Token (streaming only)
	CompletedAt time.Time      `json:"completed_at"`
	LatencyMs   int64          `json:"latency_ms"`
	
	// Execution result
	Success      bool   `json:"success"`
	Error        string `json:"error,omitempty"`
	FinishReason string `json:"finish_reason,omitempty"` // "stop" | "length" | "tool_calls" | "content_filter"
	
	// Retry information
	RetryAttempt int `json:"retry_attempt,omitempty"` // 0 = first attempt, 1+ = retry
	
	// Optional: Content summaries (to avoid storing full prompts)
	PromptSummary   string `json:"prompt_summary,omitempty"`   // e.g., "System + 3 user messages"
	ResponseSummary string `json:"response_summary,omitempty"` // e.g., "Generated 450 words"
	
	// Tool calls made by the model
	ToolCallsMade []string `json:"tool_calls_made,omitempty"` // List of tool names called
	
	// API endpoint used (for A/B testing)
	APIEndpoint string `json:"api_endpoint,omitempty"` // "chat_completions" | "responses"
}

// ─── Tool Call Tracing ───────────────────────────────────────

// ToolCallRecord captures information about a tool/function call during execution.
// This includes both LLM-invoked tools (search_knowledge) and direct function calls.
type ToolCallRecord struct {
	// Identification
	CallID   string   `json:"call_id"`            // Unique identifier for this call
	Step     StepName `json:"step"`               // Which pipeline step made this call
	ToolName string   `json:"tool_name"`          // e.g., "search_knowledge", "web_search", "tavily_search"
	
	// Call context
	InvokedBy string `json:"invoked_by,omitempty"` // "llm" | "direct" | "auto"
	LLMCallID string `json:"llm_call_id,omitempty"` // Link to the LLM call that triggered this (if applicable)
	
	// Arguments (sanitized, no sensitive data)
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	
	// Timing
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at"`
	DurationMs  int64     `json:"duration_ms"`
	
	// Execution result
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
	
	// Result summary (avoid storing full results)
	ResultSummary string `json:"result_summary,omitempty"` // e.g., "Found 5 articles, 3 relevant"
	ResultCount   int    `json:"result_count,omitempty"`   // Number of items returned
	
	// Quality metrics (if applicable)
	RelevanceScore *float64 `json:"relevance_score,omitempty"` // Average relevance of results
	
	// Cost information (if tool has external API costs)
	ExternalAPICost *float64 `json:"external_api_cost,omitempty"` // In USD
}

// ─── Pipeline DAG Metadata ───────────────────────────────────

// PipelineMetadata captures the structure and execution plan of the pipeline.
// This enables visualization and analysis of the execution flow.
type PipelineMetadata struct {
	// Agent configuration
	AgentMode string `json:"agent_mode"` // "pipeline" | "unified" | "harness"
	
	// DAG structure
	StepDAG map[StepName][]StepName `json:"step_dag"` // step -> list of prerequisite steps
	
	// Execution plan
	ExecutionPlan []StepName `json:"execution_plan"` // Planned execution order
	ActualOrder   []StepName `json:"actual_order"`   // Actual execution order (may differ due to skips/errors)
	
	// Parallelization info
	Parallelized [][]StepName `json:"parallelized,omitempty"` // Groups of steps executed in parallel
	
	// Conditional branches
	ConditionalBranches map[StepName]string `json:"conditional_branches,omitempty"` // step -> condition that triggered it
	
	// Skipped steps
	SkippedSteps []StepName `json:"skipped_steps,omitempty"` // Steps that were planned but skipped
}

// ─── Helper functions ────────────────────────────────────────

// RecordLLMCall adds an LLM call record to the execution context and forwards it
// on the unified RunEvent stream (see trace_run_event.go). Callers use this as
// the single instrumentation point for an LLM call.
func (ctx *ExecutionContext) RecordLLMCall(record LLMCallRecord) {
	ctx.traceMu.Lock()
	if ctx.LLMCalls == nil {
		ctx.LLMCalls = []LLMCallRecord{}
	}
	ctx.LLMCalls = append(ctx.LLMCalls, record)
	ctx.traceMu.Unlock()

	// NOTE: execCtx.TotalTokens is intentionally NOT incremented here — call
	// sites (steps/harness) already account for it, and CompleteTrace sums the
	// per-call arrays into the summary columns. Doing both would double-count.
	ctx.emitRunEvent(runEventFromLLM(record))
}

// RecordToolCall adds a tool call record to the execution context and forwards
// it on the unified RunEvent stream.
func (ctx *ExecutionContext) RecordToolCall(record ToolCallRecord) {
	ctx.traceMu.Lock()
	if ctx.ToolCalls == nil {
		ctx.ToolCalls = []ToolCallRecord{}
	}
	ctx.ToolCalls = append(ctx.ToolCalls, record)
	ctx.traceMu.Unlock()

	ctx.emitRunEvent(runEventFromTool(record))
}

// SetPipelineMetadata sets the pipeline metadata for this execution.
func (ctx *ExecutionContext) SetPipelineMetadata(meta PipelineMetadata) {
	ctx.PipelineMeta = &meta
}

// GetLLMCallCount returns the total number of LLM calls made.
func (ctx *ExecutionContext) GetLLMCallCount() int {
	return len(ctx.LLMCalls)
}

// GetToolCallCount returns the total number of tool calls made.
func (ctx *ExecutionContext) GetToolCallCount() int {
	return len(ctx.ToolCalls)
}

// GetTotalLatency returns the sum of all LLM call latencies in milliseconds.
func (ctx *ExecutionContext) GetTotalLatency() int64 {
	var total int64
	for _, call := range ctx.LLMCalls {
		total += call.LatencyMs
	}
	return total
}

// GetCostEstimate returns a rough cost estimate based on token usage.
// This is a placeholder; actual pricing should be loaded from configuration.
func (ctx *ExecutionContext) GetCostEstimate() float64 {
	// Simplified cost model (DeepSeek pricing as of 2024):
	// Input: $0.27 per 1M tokens
	// Output: $1.10 per 1M tokens
	// Cache hit: $0.014 per 1M tokens
	const (
		inputCostPer1M  = 0.27
		outputCostPer1M = 1.10
		cacheCostPer1M  = 0.014
	)
	
	var totalCost float64
	for _, call := range ctx.LLMCalls {
		inputTokens := call.PromptTokens - call.CacheHitTokens
		totalCost += float64(inputTokens) / 1_000_000 * inputCostPer1M
		totalCost += float64(call.CompletionTokens) / 1_000_000 * outputCostPer1M
		totalCost += float64(call.CacheHitTokens) / 1_000_000 * cacheCostPer1M
	}
	
	// Add external tool costs
	for _, tool := range ctx.ToolCalls {
		if tool.ExternalAPICost != nil {
			totalCost += *tool.ExternalAPICost
		}
	}
	
	return totalCost
}
