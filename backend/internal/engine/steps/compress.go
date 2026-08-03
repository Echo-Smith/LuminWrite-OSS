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

// maxSnippetCharsToInject caps the total raw text sent to the compression LLM.
// 20 results × ~200 chars each = ~4000 chars, well within limits.
const maxSnippetCharsToInject = 6000

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
//   - temperature: 0.2 (low variance, faithful compression)
//   - Skips for non-writing intents (chat/polish/shorten/expand/extract_points)
//   - Falls back gracefully: if LLM fails, WriteStep uses raw SearchResults
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

	// Build the raw search text to compress
	var rawBuilder strings.Builder
	totalChars := 0
	for i, r := range relevantResults {
		entry := fmt.Sprintf("%d. [%s] %s\n   %s\n", i+1, r.Source, r.Title, r.Snippet)
		if totalChars+len(entry) > maxSnippetCharsToInject {
			rawBuilder.WriteString(fmt.Sprintf("... (还有 %d 条结果省略)\n", len(relevantResults)-i))
			break
		}
		rawBuilder.WriteString(entry)
		totalChars += len(entry)
	}

	// Build the compression prompt with injection defense directive
	systemMsg := "你是搜索结果压缩器。将搜索结果压缩为结构化的研究简报，保留关键事实和多元视角，删除冗余信息。只输出简报内容，不要解释。" + engine.PromptInjectionDefenseDirective

	userMsg := fmt.Sprintf(`请将以下搜索结果压缩为结构化的研究简报。

话题：%s

搜索结果：
%s

要求：
1. 提取核心事实（数据、事件、政策等客观信息）
2. 提取多元视角（不同立场、观点、争议）
3. 提取关键数据（数字、比例、时间等）
4. 删除重复信息、广告内容、无关细节
5. 每条信息尽量简短（一行以内）
6. 总字数控制在 300-500 字

输出格式：
## 核心事实
- 事实1
- 事实2
...

## 多元视角
- 视角1
- 视角2
...

## 关键数据
- 数据1
- 数据2
...`,
		execCtx.UserInput,
		rawBuilder.String(),
	)

	messages := []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}

	// Non-thinking, low temperature: fast mechanical summarization
	resp, llmResp, err := s.llm.Chat(ctx, messages,
		tools.WithTemperature(0.2),
		tools.WithThinking(false),
	)
	if err != nil {
		// Compression failed — non-fatal, WriteStep will use raw search results
		slog.Warn("compress step: LLM call failed, falling back to raw results",
			"error", err,
			"trace_id", execCtx.TraceID,
		)
		return nil
	}

	// Track token usage
	if llmResp != nil {
		execCtx.TotalTokens += llmResp.Usage.TotalTokens
	}

	compressed := strings.TrimSpace(resp)
	if compressed == "" {
		slog.Warn("compress step: empty response from LLM", "trace_id", execCtx.TraceID)
		return nil
	}

	execCtx.CompressedContext = compressed

	slog.Info("compress step: completed",
		"trace_id", execCtx.TraceID,
		"raw_results", len(relevantResults),
		"raw_chars", totalChars,
		"compressed_chars", len(compressed),
		"compression_ratio", fmt.Sprintf("%.1f%%", float64(len(compressed))/float64(totalChars)*100),
		"tokens", llmResp.Usage.TotalTokens,
	)

	return nil
}
