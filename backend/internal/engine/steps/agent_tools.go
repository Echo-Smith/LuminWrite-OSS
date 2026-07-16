package steps

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── Agent Loop Tool Definitions ─────────────────────────
//
// P2: Tool Calls + thinking mode for adaptive Agent Loop.
// When writing mode is active and search results are available,
// the WriteStep can optionally use ChatWithTools to let the model
// autonomously request more context via tool calls.

// WritingTools returns tool definitions available to the writing agent loop.
// Currently defines:
//   - search_web: search for additional information on a topic
//   - get_topic_context: retrieve full-text content for a specific search result
func WritingTools() []tools.ToolDef {
	return []tools.ToolDef{
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "search_web",
				Description: "搜索网络获取更多关于某个话题的信息。当已有素材不足以支撑写作时调用。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "搜索关键词",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: tools.ToolDefFunction{
				Name:        "get_topic_context",
				Description: "获取某个已有搜索结果的详细内容。传入搜索结果的序号(1-based)来获取更多信息。",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"index": map[string]any{
							"type":        "integer",
							"description": "搜索结果的序号(从1开始)",
						},
					},
					"required": []string{"index"},
				},
			},
		},
	}
}

// WritingToolExecutor creates a ToolExecutor for the writing agent loop.
// It closes over the search client and current search results so the model
// can autonomously fetch more context during writing.
func WritingToolExecutor(
	search *tools.SearchClient,
	searchResults []engine.SearchResult,
) tools.ToolExecutor {
	return func(name string, arguments string) (string, error) {
		switch name {
		case "search_web":
			var args struct {
				Query string `json:"query"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if args.Query == "" {
				return "Error: query is required", nil
			}
			if search == nil || !search.HasSources() {
				return "Error: search not available", nil
			}
			results := search.Search(context.Background(), args.Query, 5)
			if len(results) == 0 {
				return "No results found", nil
			}
			var sb strings.Builder
			for i, r := range results {
				sb.WriteString(fmt.Sprintf("%d. %s\n   %s\n", i+1, r.Title, r.Snippet))
			}
			return sb.String(), nil

		case "get_topic_context":
			var args struct {
				Index int `json:"index"`
			}
			if err := json.Unmarshal([]byte(arguments), &args); err != nil {
				return "", fmt.Errorf("invalid arguments: %w", err)
			}
			if args.Index < 1 || args.Index > len(searchResults) {
				return fmt.Sprintf("Error: index out of range (1-%d)", len(searchResults)), nil
			}
			r := searchResults[args.Index-1]
			return fmt.Sprintf("标题: %s\n来源: %s\n内容: %s\nURL: %s", r.Title, r.Source, r.Snippet, r.URL), nil

		default:
			return fmt.Sprintf("Error: unknown tool '%s'", name), nil
		}
	}
}

// ShouldUseAgentLoop determines whether the agent loop (tool calls) should be
// used for the current writing task. It returns true only when:
//   - Task mode is "writing" (not polish/shorten/expand)
//   - Search client is available with sources
//   - There are already some search results to reference
func ShouldUseAgentLoop(execCtx *engine.ExecutionContext, search *tools.SearchClient) bool {
	if execCtx.TaskIntent == nil || execCtx.TaskIntent.TaskMode != "writing" {
		return false
	}
	if search == nil || !search.HasSources() {
		return false
	}
	// Only use agent loop when there are initial search results
	// (the model can then request more context via tools)
	return len(execCtx.SearchResults) >= 3
}
