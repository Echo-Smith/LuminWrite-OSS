package services

import (
	"context"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ─── KbSearchAdapter ────────────────────────────────────
// KbSearchAdapter wraps KbManager to implement the tools.KnowledgeSearcher
// interface, enabling the local knowledge base to participate in the
// multi-source search pipeline alongside Tavily, Zhihu, Bing, etc.
//
// This replaces the old WeKnoraClient.Search() which made an HTTP
// round-trip to an external WeKnora container. The adapter calls
// KbManager.HybridSearch() directly on local PostgreSQL — zero network,
// transparent BM25/Dense/RRF scores, and chunk-level granularity.

// KbSearchAdapter bridges KbManager → tools.KnowledgeSearcher interface.
type KbSearchAdapter struct {
	mgr *KbManager
}

// NewKbSearchAdapter creates a new adapter wrapping the given KbManager.
func NewKbSearchAdapter(mgr *KbManager) *KbSearchAdapter {
	return &KbSearchAdapter{mgr: mgr}
}

// SearchKB implements tools.KnowledgeSearcher.
// It calls KbManager.HybridSearch() (BM25 + Dense + RRF) and converts
// the results to engine.SearchResult for compatibility with the search pipeline.
func (a *KbSearchAdapter) SearchKB(ctx context.Context, query string, limit int) ([]engine.SearchResult, error) {
	if a == nil || a.mgr == nil || !a.mgr.IsConfigured() {
		return nil, nil
	}

	// Use global KB (userID = "" for shared knowledge)
	results, err := a.mgr.HybridSearch(ctx, "", query, limit)
	if err != nil {
		return nil, err
	}

	// Convert KbSearchResult → engine.SearchResult
	engineResults := make([]engine.SearchResult, 0, len(results))
	for _, r := range results {
		snippet := r.Content
		if len([]rune(snippet)) > 500 {
			snippet = string([]rune(snippet)[:500]) + "..."
		}

		engineResults = append(engineResults, engine.SearchResult{
			Title:   r.Title,
			Snippet: r.Title + "：" + snippet,
			URL:     "",
			Source:  "local_kb",
			Score:   r.Score,
		})
	}

	slog.Debug("local KB search adapter completed",
		"query", query,
		"results", len(engineResults),
	)

	return engineResults, nil
}
