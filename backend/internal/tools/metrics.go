package tools

import "sync/atomic"

// ToolMetrics holds lightweight atomic counters for tool-level operations.
// These are read by the server's /metrics endpoint.
var ToolMetrics = struct {
	// Search metrics
	SearchQueriesTotal atomic.Int64
	SearchErrorsTotal  atomic.Int64
	SearchDurationNs   atomic.Int64 // sum of durations in nanoseconds

	// Embedding metrics
	EmbeddingCallsTotal atomic.Int64
	EmbeddingErrorsTotal atomic.Int64
	EmbeddingDurationNs  atomic.Int64 // sum of durations in nanoseconds
}{}

// RecordSearchCall records a search query metric.
func RecordSearchCall(durationNs int64, err error) {
	ToolMetrics.SearchQueriesTotal.Add(1)
	ToolMetrics.SearchDurationNs.Add(durationNs)
	if err != nil {
		ToolMetrics.SearchErrorsTotal.Add(1)
	}
}

// RecordEmbeddingCall records an embedding API call metric.
func RecordEmbeddingCall(durationNs int64, err error) {
	ToolMetrics.EmbeddingCallsTotal.Add(1)
	ToolMetrics.EmbeddingDurationNs.Add(durationNs)
	if err != nil {
		ToolMetrics.EmbeddingErrorsTotal.Add(1)
	}
}
