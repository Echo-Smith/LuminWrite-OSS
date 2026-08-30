# Markdown Article Output Contract Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Unify article generation on a Markdown title contract while preserving body content under model format drift and legacy JSON output.

**Architecture:** Add one stateful `ArticleStreamParser` in the steps package and use it from both Harness and legacy WriteStep. Reinforce the contract near the final user/tool messages, then record the detected protocol in existing trace step history without exposing it to ordinary users.

**Tech Stack:** Go 1.25, table-driven Go tests, existing LLM streaming callbacks, WorldState, PromptBuilder, Makefile verification gates.

---

### Task 1: Specify the shared streaming parser

**Files:**
- Create: `backend/internal/engine/steps/article_stream_parser_test.go`
- Create: `backend/internal/engine/steps/article_stream_parser.go`
- Modify: `backend/internal/engine/steps/title_extractor.go`

**Step 1: Write the failing table-driven tests**

Cover canonical Markdown in one delta, a heading split across deltas, preamble before a heading, `#` compatibility, legacy JSON plus `---ARTICLE---`, confirmed-title fallback, short-line fallback, missing title, buffer-limit passthrough, and reset. Every case must assert title, body, protocol, emitted title count, and concatenated body deltas.

**Step 2: Run the focused test and verify failure**

Run: `docker run --rm -e GOFLAGS=-mod=readonly -v "$REPO/backend:/src" -w /src golang:1.25 go test ./internal/engine/steps -run ArticleStreamParser -count=1`

Expected: FAIL because `NewArticleStreamParser` and protocol constants do not exist.

**Step 3: Implement the minimal parser**

Define:

```go
type ArticleOutputProtocol string

const (
    ArticleProtocolMarkdown          ArticleOutputProtocol = "markdown"
    ArticleProtocolLegacyJSON        ArticleOutputProtocol = "legacy_json"
    ArticleProtocolShortLineFallback ArticleOutputProtocol = "short_line_fallback"
    ArticleProtocolMissingTitle      ArticleOutputProtocol = "missing_title"
)

type ParsedArticle struct {
    Title    string
    Body     string
    Protocol ArticleOutputProtocol
}
```

The parser API is `Push(delta string)`, `Reset()`, `Body() string`, and `Finalize(fullText string) ParsedArticle`. Callbacks supplied to the constructor emit title and body. A delta may be consumed only once.

**Step 4: Run focused tests**

Expected: PASS with all parser cases.

### Task 2: Make Markdown the only generated protocol

**Files:**
- Modify: `backend/internal/profile/profile_prompt.go`
- Modify: `backend/internal/profile/profile_prompt_test.go`
- Modify: `backend/internal/engine/steps/prompt_builder.go`
- Modify: `backend/internal/engine/steps/prompt_builder_test.go`
- Modify: `backend/internal/worldstate/sections.go`
- Modify: `backend/internal/worldstate/world_state_test.go`

**Step 1: Add failing prompt assertions**

Assert that writing prompts contain `## 文章标题`, contain neither `---ARTICLE---` nor a JSON title example, preserve a confirmed outline title, and keep the output section when the budget is exceeded.

**Step 2: Run profile, steps, and worldstate tests**

Expected: FAIL on the current JSON-format strings.

**Step 3: Replace generated JSON instructions**

Introduce `profile.RenderMarkdownArticleFormat(outlineTitle string)` and use it from `RenderOutputFormat` and `PromptBuilder.AddOutputFormat`. Keep legacy JSON parsing only in the parser; do not keep legacy JSON generation in prompts. Update both guided and automatic WorldState instructions.

**Step 4: Run focused tests**

Expected: PASS, including the over-budget critical-section test.

### Task 3: Reinforce the contract at the end of long contexts

**Files:**
- Modify: `backend/internal/agent/harness.go`
- Modify: `backend/internal/agent/harness_test.go`
- Modify: `backend/internal/agent/tools.go`
- Create or modify: `backend/internal/agent/tools_test.go`

**Step 1: Write failing recency tests**

Build messages with six long history entries and call WorldState twice so unchanged task instructions disappear from the diff. Assert the final user message still ends with a concise article-format reminder for writing/polish/shorten/expand, while chat does not. Assert `write_article` and `revise_section` tool results repeat the same contract.

**Step 2: Run focused agent tests**

Expected: FAIL because the current user message has no near-end reminder and tool results are inconsistent.

**Step 3: Implement the reminder**

Add one shared reminder string/function in the agent package. Append it to the current user message only for article-producing intents using wording conditional on outputting a complete article. Reuse it in signal-tool results so the reminder is the latest tool content before generation.

**Step 4: Run focused agent tests**

Expected: PASS without changing guided outline ordering.

### Task 4: Integrate the parser into both execution paths

**Files:**
- Modify: `backend/internal/agent/harness.go`
- Modify: `backend/internal/agent/harness_test.go`
- Modify: `backend/internal/engine/steps/steps.go`
- Modify: `backend/internal/engine/steps/steps_test.go`

**Step 1: Add failing integration tests**

Use streamed test-server responses where the heading is split across chunks and where title/body arrive together. Assert Harness and WriteStep produce the same title and body and never duplicate the first body delta. Add reset coverage for an intermediate tool-call round.

**Step 2: Run focused integration tests**

Expected: FAIL against the two existing JSON-specific state machines.

**Step 3: Replace duplicated state machines**

Construct `ArticleStreamParser` with emitter callbacks in both paths. On reset, preserve the existing Harness saved-article behavior, reset the parser, and send one stream reset. On completion, call `Finalize`, set `ArticleTitle` and `Article`, and send the parsed body to `StreamDone`.

**Step 4: Run focused integration tests**

Expected: PASS with identical Harness and Pipeline results.

### Task 5: Record protocol deviations in trace data

**Files:**
- Modify: `backend/internal/engine/context.go`
- Modify: `backend/internal/engine/context_test.go`
- Modify: `backend/internal/agent/harness.go`
- Modify: `backend/internal/engine/steps/steps.go`

**Step 1: Write a failing trace-record test**

Assert finalization sets `ExecutionContext.ArticleOutputProtocol` and adds exactly one completed `article_output` step whose result contains `protocol` and `deviated`. `markdown` must set `deviated=false`; every fallback must set it to true.

**Step 2: Implement trace recording**

Add the JSON field to ExecutionContext and a helper that records the protocol once in `StepHistory`. Emit a structured log containing `trace_id`, `protocol`, and `deviated`. Do not add a WebSocket event or ordinary-user UI.

**Step 3: Run engine and integration tests**

Expected: PASS and no database migration, because existing `step_history` JSONB persistence carries the result.

### Task 6: Synchronize editions and run all gates

**Files:**
- Apply every shared source and test change identically to both repositories.

**Step 1: Compare shared files**

Run `cmp` for each modified shared path and `git diff --check` in both repositories.

Expected: no mismatch and no whitespace error.

**Step 2: Run quality gates in both repositories**

Run: `make verify-frontend`

Run in Go 1.25 container: `make verify-backend`

Expected: both editions pass lint with zero errors, frontend build, WABench 6/6, Go build, and all Go tests. Existing lint and chunk-size warnings may remain unchanged.

**Step 3: Review without pushing**

Confirm the original mixed worktree still matches its recovery snapshot. Leave stabilization branches unpushed for user comparison.
