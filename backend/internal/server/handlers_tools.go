package server

import (
	"net/http"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Tool Graph Handlers ─────────────────────────────────
//
// The tool graph endpoint returns the dependency structure of all
// registered agent tools. This is used by the frontend to render
// a visual dependency diagram showing:
//   - Which tools depend on others (e.g. search depends on query_plan)
//   - Which tools are terminal (can end the agent loop)
//   - Which tools are repeatable vs once-only
//   - Tool categories for grouping in the UI
//
// The graph is built by constructing a temporary ToolRegistry with
// the same tools and descriptors used by the pipeline agent.

// handleToolGraph returns the tool dependency graph.
//
// GET /api/v2/tools/graph
//
// This endpoint constructs a temporary ToolRegistry (the same one used
// by the pipeline agent) and returns its dependency graph. It requires
// no authentication because the tool graph is structural metadata,
// not user-specific data.
//
// Response:
//
//	{
//	  "nodes": [
//	    {
//	      "name": "intent",
//	      "description": "意图分类：...",
//	      "depends_on": null,
//	      "repeatable": false,
//	      "terminal": false,
//	      "category": "planning"
//	    },
//	    {
//	      "name": "search",
//	      "description": "多源搜索：...",
//	      "depends_on": ["query_plan"],
//	      "repeatable": true,
//	      "terminal": false,
//	      "category": "retrieval"
//	    },
//	    ...
//	  ],
//	  "edges": [
//	    {"from": "search", "to": "query_plan"},
//	    {"from": "relevance", "to": "search"},
//	    {"from": "compress", "to": "relevance"},
//	    {"from": "auto_fix", "to": "post_review"},
//	  ]
//	}
func (s *Server) handleToolGraph(w http.ResponseWriter, r *http.Request) {
	// Build a temporary registry with the same tools used by the pipeline agent.
	// We use a nil execCtx (mode="auto") to include all default tools.
	// The guided-only "outline" tool will be included if mode is "guided",
	// but for the graph endpoint we show the full superset.
	llmClient := s.llm
	if s.llmSvc != nil {
		llmClient = s.llmSvc.GetDefaultClient(r.Context())
	}
	if llmClient == nil {
		response.Err(w, http.StatusServiceUnavailable, "llm_unavailable", "LLM client not available")
		return
	}

	// Load default style profile (needed for WriteStep and PostReviewStep)
	var styleProfile *profile.StyleProfile
	if s.profiles != nil {
		if sp, ok := s.profiles.Get("yinyue"); ok {
			styleProfile = sp
		}
	}

	// Build registry with guided mode to include outline tool
	execCtx := engine.NewExecutionContext("tool-graph", "system", "")
	execCtx.Mode = "guided"
	registry := s.buildToolRegistry(llmClient, styleProfile, execCtx)

	graph := registry.ToolGraph()

	response.OK(w, graph)
}
