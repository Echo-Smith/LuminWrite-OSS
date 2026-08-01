package tools

import (
	"sync"
	"time"
)

// ABMetrics collects metrics for A/B testing between Chat Completions and Responses API.
type ABMetrics struct {
	mu              sync.Mutex
	chatCompletions ABMetricsBucket
	responsesAPI    ABMetricsBucket
}

type ABMetricsBucket struct {
	RequestCount     int64
	PromptTokens     int64
	CacheHitTokens   int64
	CompletionTokens int64
	TotalLatencyMs   int64
}

// ABMetricsSnapshot is a point-in-time copy of A/B metrics.
type ABMetricsSnapshot struct {
	ChatCompletions ABMetricsBucket `json:"chat_completions"`
	ResponsesAPI    ABMetricsBucket `json:"responses_api"`
}

func NewABMetrics() *ABMetrics {
	return &ABMetrics{}
}

// RecordChatCompletions records a non-streaming Chat Completions API call.
func (m *ABMetrics) RecordChatCompletions(resp *LLMResponse, latency time.Duration) {
	if resp == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCompletions.RequestCount++
	m.chatCompletions.PromptTokens += int64(resp.Usage.PromptTokens)
	m.chatCompletions.CompletionTokens += int64(resp.Usage.CompletionTokens)
	m.chatCompletions.TotalLatencyMs += latency.Milliseconds()
}

// RecordChatCompletionsStream records a streaming Chat Completions API call.
func (m *ABMetrics) RecordChatCompletionsStream(totalTokens int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.chatCompletions.RequestCount++
	m.chatCompletions.CompletionTokens += int64(totalTokens)
	m.chatCompletions.TotalLatencyMs += latency.Milliseconds()
}

// RecordResponsesAPI records a non-streaming Responses API call.
func (m *ABMetrics) RecordResponsesAPI(resp *LLMResponse, latency time.Duration) {
	if resp == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responsesAPI.RequestCount++
	m.responsesAPI.PromptTokens += int64(resp.Usage.PromptTokens)
	m.responsesAPI.CacheHitTokens += int64(resp.Usage.CacheHitTokens)
	m.responsesAPI.CompletionTokens += int64(resp.Usage.CompletionTokens)
	m.responsesAPI.TotalLatencyMs += latency.Milliseconds()
}

// RecordResponsesAPIStream records a streaming Responses API call.
func (m *ABMetrics) RecordResponsesAPIStream(totalTokens int, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responsesAPI.RequestCount++
	m.responsesAPI.CompletionTokens += int64(totalTokens)
	m.responsesAPI.TotalLatencyMs += latency.Milliseconds()
}

// Snapshot returns a point-in-time copy of the metrics.
func (m *ABMetrics) Snapshot() *ABMetricsSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	return &ABMetricsSnapshot{
		ChatCompletions: m.chatCompletions,
		ResponsesAPI:    m.responsesAPI,
	}
}
