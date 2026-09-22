package steps

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// Phase 2: traced LLM call wrappers. Every pipeline step routes its model
// calls through these so the run's LLMCalls array (and thus llm_call_count /
// first_token_ms / output_tokens_per_sec / cost breakdown on agent_traces) is
// populated from real usage. The current step is derived from
// execCtx.CurrentStep (engine.Run sets it), so call sites stay unchanged.

// trackedLLMChat wraps llm.Chat with automatic trace recording.
func trackedLLMChat(
	ctx context.Context,
	execCtx *engine.ExecutionContext,
	llm *tools.LLMClient,
	messages []tools.LLMMessage,
	opts ...tools.ChatOption,
) (string, *tools.LLMResponse, error) {
	callID := uuid.New().String()
	startTime := time.Now()
	modelName := modelOf(llm)

	result, resp, err := llm.Chat(ctx, messages, opts...)

	endTime := time.Now()

	record := engine.LLMCallRecord{
		CallID:      callID,
		Step:        execCtx.CurrentStep,
		Model:       modelName,
		Provider:    inferProvider(modelName),
		StartedAt:   startTime,
		CompletedAt: endTime,
		LatencyMs:   endTime.Sub(startTime).Milliseconds(),
		Success:     err == nil,
	}

	if resp != nil {
		record.PromptTokens = resp.Usage.PromptTokens
		record.CompletionTokens = resp.Usage.CompletionTokens
		record.TotalTokens = resp.Usage.TotalTokens
		record.CacheHitTokens = resp.Usage.CacheHitTokens
		record.CacheMissTokens = resp.Usage.CacheMissTokens
		record.ReasoningTokens = resp.Usage.CompletionTokensDetails.ReasoningTokens
		if len(resp.Choices) > 0 {
			record.FinishReason = resp.Choices[0].FinishReason
			if len(resp.Choices[0].Message.ToolCalls) > 0 {
				record.ToolCallsMade = make([]string, len(resp.Choices[0].Message.ToolCalls))
				for i, tc := range resp.Choices[0].Message.ToolCalls {
					record.ToolCallsMade[i] = tc.Function.Name
				}
			}
		}
		record.APIEndpoint = "chat_completions"
	}
	if err != nil {
		record.Error = err.Error()
	}
	record.PromptSummary = summarizeMessages(messages)
	if resp != nil {
		record.ResponseSummary = fmt.Sprintf("Generated %d chars", len(result))
	}

	execCtx.RecordLLMCall(record)
	return result, resp, err
}

// trackedLLMStreamWithReasoning wraps llm.ChatStreamWithReasoning and measures TTFT.
func trackedLLMStreamWithReasoning(
	ctx context.Context,
	execCtx *engine.ExecutionContext,
	llm *tools.LLMClient,
	messages []tools.LLMMessage,
	onDelta func(string),
	onReasoning func(string),
	opts ...tools.ChatOption,
) (string, int, error) {
	callID := uuid.New().String()
	startTime := time.Now()
	modelName := modelOf(llm)
	var ttft *time.Duration
	firstToken := true

	wrappedOnDelta := func(delta string) {
		if firstToken && delta != "" {
			firstToken = false
			d := time.Since(startTime)
			ttft = &d
		}
		if onDelta != nil {
			onDelta(delta)
		}
	}

	// Install a usage capture so the streaming return (aggregate total only) is
	// enriched with prompt/completion/cache splits for cost + throughput.
	callCtx, usage := tools.WithUsageCapture(ctx)

	result, totalTokens, err := llm.ChatStreamWithReasoning(callCtx, messages, wrappedOnDelta, onReasoning, opts...)
	endTime := time.Now()

	record := engine.LLMCallRecord{
		CallID:      callID,
		Step:        execCtx.CurrentStep,
		Model:       modelName,
		Provider:    inferProvider(modelName),
		TTFT:        ttft,
		StartedAt:   startTime,
		CompletedAt: endTime,
		LatencyMs:   endTime.Sub(startTime).Milliseconds(),
		Success:     err == nil,
		APIEndpoint: "chat_completions_stream",
	}
	applyUsageCapture(&record, totalTokens, usage)
	if err != nil {
		record.Error = err.Error()
	}
	record.PromptSummary = summarizeMessages(messages)
	record.ResponseSummary = fmt.Sprintf("Streamed %d chars, %d tokens", len(result), totalTokens)

	execCtx.RecordLLMCall(record)
	return result, totalTokens, err
}

// applyUsageCapture fills a record's token splits from a stream usage snapshot,
// falling back to the aggregate total the streaming call returned.
func applyUsageCapture(record *engine.LLMCallRecord, totalTokens int, usage *tools.UsageSnapshot) {
	if usage == nil || !usage.HasUsage {
		record.TotalTokens = totalTokens
		return
	}
	record.PromptTokens = usage.Prompt
	record.CompletionTokens = usage.Completion
	record.ReasoningTokens = usage.Reasoning
	record.CacheHitTokens = usage.CacheHit
	record.CacheMissTokens = usage.CacheMiss
	record.TotalTokens = usage.Total
	if record.TotalTokens == 0 {
		record.TotalTokens = totalTokens
	}
}

// trackedLLMWithTools wraps the agent-loop call (ChatWithTools) with trace recording.
func trackedLLMWithTools(
	ctx context.Context,
	execCtx *engine.ExecutionContext,
	llm *tools.LLMClient,
	messages []tools.LLMMessage,
	onDelta func(string),
	onReasoning func(string),
	onStreamReset func(),
	toolSchemas []tools.ToolDef,
	toolExecutor tools.ToolExecutor,
	opts ...tools.ChatOption,
) (string, int, error) {
	callID := uuid.New().String()
	startTime := time.Now()
	modelName := modelOf(llm)
	var ttft *time.Duration
	firstToken := true

	wrappedOnDelta := func(delta string) {
		if firstToken && delta != "" {
			firstToken = false
			d := time.Since(startTime)
			ttft = &d
		}
		if onDelta != nil {
			onDelta(delta)
		}
	}

	// Accumulate per-round usage across the whole agent loop so write-draft cost
	// and throughput are accurate (ChatWithTools returns only an aggregate total).
	callCtx, usage := tools.WithUsageCapture(ctx)

	result, totalTokens, err := llm.ChatWithTools(
		callCtx, messages, wrappedOnDelta, onReasoning, onStreamReset,
		toolSchemas, toolExecutor, opts...,
	)
	endTime := time.Now()

	record := engine.LLMCallRecord{
		CallID:      callID,
		Step:        execCtx.CurrentStep,
		Model:       modelName,
		Provider:    inferProvider(modelName),
		TTFT:        ttft,
		StartedAt:   startTime,
		CompletedAt: endTime,
		LatencyMs:   endTime.Sub(startTime).Milliseconds(),
		Success:     err == nil,
		APIEndpoint: "agent_loop",
	}
	applyUsageCapture(&record, totalTokens, usage)
	if err != nil {
		record.Error = err.Error()
	}
	record.PromptSummary = summarizeMessages(messages)
	record.ResponseSummary = fmt.Sprintf("Agent loop: %d chars, %d tokens", len(result), totalTokens)

	execCtx.RecordLLMCall(record)
	return result, totalTokens, err
}

// ─── helpers ────────────────────────────────────────────────────────────────

// modelOf returns the client's configured model name for accurate labeling.
func modelOf(llm *tools.LLMClient) string {
	if llm == nil {
		return ""
	}
	return llm.Model()
}

func inferProvider(model string) string {
	switch {
	case model == "":
		return "unknown"
	case strings.Contains(model, "deepseek"):
		return "deepseek"
	case strings.Contains(model, "gpt"):
		return "openai"
	case strings.Contains(model, "claude"):
		return "anthropic"
	default:
		return "unknown"
	}
}

func summarizeMessages(messages []tools.LLMMessage) string {
	if len(messages) == 0 {
		return "Empty conversation"
	}
	var systemCount, userCount, assistantCount, toolCount int
	for _, msg := range messages {
		switch msg.Role {
		case "system":
			systemCount++
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		case "tool":
			toolCount++
		}
	}
	parts := make([]string, 0, 4)
	if systemCount > 0 {
		parts = append(parts, fmt.Sprintf("System:%d", systemCount))
	}
	if userCount > 0 {
		parts = append(parts, fmt.Sprintf("User:%d", userCount))
	}
	if assistantCount > 0 {
		parts = append(parts, fmt.Sprintf("Assistant:%d", assistantCount))
	}
	if toolCount > 0 {
		parts = append(parts, fmt.Sprintf("Tool:%d", toolCount))
	}
	return strings.Join(parts, " ")
}
