package steps

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── CompressStep ─────────────────────────────────────────

// minResultsToCompress is the threshold below which compression is skipped.
// With fewer results, the raw injection is small enough that an extra LLM
// call would cost more (latency + tokens) than it saves.
const minResultsToCompress = 4

// CompressStep takes the deduplicated, scored search results from RelevanceStep
// and compresses them into a structured "research brief" using a single LLM call.
//
// The brief replaces raw search snippets in the WriteStep prompt, delivering:
//   - ~60% reduction in prompt token consumption for the writing call
//   - Cleaner, structured context (facts / perspectives / data) vs. noisy snippets
//   - Better generation quality: the model focuses on writing, not re-filtering
//
// Design decisions:
//   - thinking: disabled (mechanical summarization, no deep reasoning needed)
//   - temperature: 0.1 (low variance, faithful compression)
//   - Skips for non-writing intents (chat/polish/shorten/expand/extract_points)
//   - Falls back gracefully: if LLM fails, WriteStep uses raw SearchResults
//
// Compression logic is delegated to tools.CompressSearchResults (shared with
// Harness and Editorial modes), which implements source-aware budget allocation.
type CompressStep struct {
	llm *tools.LLMClient
}

// NewCompressStep creates a CompressStep with the given LLM client.
func NewCompressStep(llm *tools.LLMClient) *CompressStep {
	return &CompressStep{llm: llm}
}

func (s *CompressStep) Name() engine.StepName { return engine.StepCompress }
func (s *CompressStep) CanPause() bool         { return false }
func (s *CompressStep) Timeout() time.Duration { return 60 * time.Second }
func (s *CompressStep) Critical() bool         { return false }

// ShouldSkip returns true for intents that don't produce or need search results.
// Same skip set as RelevanceStep — if there are no search results, no compression.
func (s *CompressStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.TaskIntent == nil {
		return false
	}
	switch execCtx.TaskIntent.TaskMode {
	case "chat", "polish", "shorten", "expand", "extract_points":
		return true
	}
	return false
}

func (s *CompressStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	// Skip if no search results or too few to warrant compression
	if len(execCtx.SearchResults) < minResultsToCompress {
		slog.Debug("compress step: skipped, too few results",
			"result_count", len(execCtx.SearchResults),
			"min_required", minResultsToCompress,
			"trace_id", execCtx.TraceID,
		)
		return nil
	}

	if s.llm == nil {
		slog.Debug("compress step: LLM not available, skipping")
		return nil
	}

	// Filter to strong + medium relevance only; weak results add noise
	// Also apply prompt injection sanitization to search result content
	var relevantResults []engine.SearchResult
	for _, r := range execCtx.SearchResults {
		if r.Relevance == "strong" || r.Relevance == "medium" || r.Relevance == "" {
			// Sanitize each result's title and snippet before use
			r.Title = engine.SanitizeExternalContent(r.Title, "compress_step:search_title")
			r.Snippet = engine.SanitizeExternalContent(r.Snippet, "compress_step:search_snippet")
			relevantResults = append(relevantResults, r)
		}
	}
	if len(relevantResults) < minResultsToCompress {
		slog.Debug("compress step: too few relevant results after filtering",
			"total", len(execCtx.SearchResults),
			"relevant", len(relevantResults),
		)
		return nil
	}

	// Call the unified source-aware compression function (shared with Harness & Editorial)
	compressed, rawChars, llmResp := tools.CompressSearchResults(ctx, s.llm, execCtx.UserInput, relevantResults)

	// Track token usage
	if llmResp != nil {
		execCtx.TotalTokens += llmResp.Usage.TotalTokens
	}

	compressed = strings.TrimSpace(compressed)
	if compressed == "" {
		slog.Warn("compress step: empty response from LLM", "trace_id", execCtx.TraceID)
		return nil
	}

	execCtx.CompressedContext = compressed

	if rawChars > 0 && llmResp != nil {
		slog.Info("compress step: completed",
			"trace_id", execCtx.TraceID,
			"raw_results", len(relevantResults),
			"raw_chars", rawChars,
			"compressed_chars", len(compressed),
			"compression_ratio", fmt.Sprintf("%.1f%%", float64(len(compressed))/float64(rawChars)*100),
			"tokens", llmResp.Usage.TotalTokens,
		)
	} else {
		slog.Info("compress step: completed (fallback to raw formatting)",
			"trace_id", execCtx.TraceID,
			"raw_results", len(relevantResults),
		)
	}

	return nil
}
