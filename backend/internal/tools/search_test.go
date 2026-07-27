package tools

import (
	"context"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// ═══════════════════════════════════════════════════════════════
// extractDomain 单元测试
// ═══════════════════════════════════════════════════════════════

func Test_ExtractDomain(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"https URL", "https://www.example.com/path/page", "www.example.com"},
		{"http URL", "http://finance.sina.com.cn/article/123", "finance.sina.com.cn"},
		{"URL without scheme", "example.com/page", "example.com"},
		{"URL with port", "https://localhost:8080/api", "localhost"},
		{"empty URL", "", ""},
		{"malformed URL", "://invalid", ""},
		{"URL with query params", "https://www.zhihu.com/search?q=test&type=content", "www.zhihu.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractDomain(tt.url)
			if got != tt.want {
				t.Errorf("extractDomain(%q) = %q, want %q", tt.url, got, tt.want)
			}
		})
	}
}

// ═══════════════════════════════════════════════════════════════
// enrichWithCredibility 单元测试
// ═══════════════════════════════════════════════════════════════

// mockCredibilityLookup mocks engine.CredibilityLookup for testing
type mockCredibilityLookup struct {
	scores map[string]float64
}

func (m *mockCredibilityLookup) GetCredibility(ctx context.Context, domain string) (float64, error) {
	if score, ok := m.scores[domain]; ok {
		return score, nil
	}
	return 0.5, nil // neutral default
}

func Test_EnrichWithCredibility_SortsByCombinedScore(t *testing.T) {
	client := &SearchClient{
		credibilityLookup: &mockCredibilityLookup{
			scores: map[string]float64{
				"trusted.com":     0.95,
				"untrusted.com":   0.1,
				"medium.com":      0.5,
			},
		},
	}

	results := []engine.SearchResult{
		{Title: "Low credibility result", URL: "https://untrusted.com/article1", Source: "test", Score: 0.9},
		{Title: "High credibility result", URL: "https://trusted.com/article2", Source: "test", Score: 0.9},
		{Title: "Medium credibility result", URL: "https://medium.com/article3", Source: "test", Score: 0.9},
	}

	enriched := client.enrichWithCredibility(context.Background(), results)

	// All results should have credibility scores set
	if enriched[0].CredibilityScore == 0 {
		t.Error("expected credibility score to be set")
	}

	// High credibility should be ranked first
	if enriched[0].URL != "https://trusted.com/article2" {
		t.Errorf("expected trusted.com first, got %s (credibility=%.2f)", enriched[0].URL, enriched[0].CredibilityScore)
	}

	// Low credibility should be ranked last
	if enriched[2].URL != "https://untrusted.com/article1" {
		t.Errorf("expected untrusted.com last, got %s (credibility=%.2f)", enriched[2].URL, enriched[2].CredibilityScore)
	}
}

func Test_EnrichWithCredibility_HighScoreLowCredibility_RanksLower(t *testing.T) {
	client := &SearchClient{
		credibilityLookup: &mockCredibilityLookup{
			scores: map[string]float64{
				"low-cred.com":  0.1,
				"high-cred.com": 0.9,
			},
		},
	}

	results := []engine.SearchResult{
		// High relevance score but low credibility
		{Title: "High score, low cred", URL: "https://low-cred.com/a", Score: 1.0},
		// Lower relevance score but high credibility
		{Title: "Low score, high cred", URL: "https://high-cred.com/b", Score: 0.5},
	}

	enriched := client.enrichWithCredibility(context.Background(), results)

	// Combined: 1.0 × (0.5 + 0.5×0.1) = 1.0 × 0.55 = 0.55
	//           0.5 × (0.5 + 0.5×0.9) = 0.5 × 0.95 = 0.475
	// High score low cred should still rank higher (0.55 > 0.475)
	if enriched[0].URL != "https://low-cred.com/a" {
		t.Errorf("expected low-cred.com first (combined score 0.55 > 0.475), got %s", enriched[0].URL)
	}
}

func Test_EnrichWithCredibility_NoURL_GetsNeutralScore(t *testing.T) {
	client := &SearchClient{
		credibilityLookup: &mockCredibilityLookup{},
	}

	results := []engine.SearchResult{
		{Title: "No URL", URL: "", Score: 0.8},
	}

	enriched := client.enrichWithCredibility(context.Background(), results)
	if enriched[0].CredibilityScore != 0.5 {
		t.Errorf("expected neutral credibility 0.5 for no-URL result, got %f", enriched[0].CredibilityScore)
	}
}

func Test_EnrichWithCredibility_UnknownDomain_GetsNeutralScore(t *testing.T) {
	client := &SearchClient{
		credibilityLookup: &mockCredibilityLookup{
			scores: map[string]float64{}, // empty → all unknown
		},
	}

	results := []engine.SearchResult{
		{Title: "Unknown", URL: "https://unknown-domain-xyz.com/page", Score: 0.7},
	}

	enriched := client.enrichWithCredibility(context.Background(), results)
	if enriched[0].CredibilityScore != 0.5 {
		t.Errorf("expected neutral credibility 0.5 for unknown domain, got %f", enriched[0].CredibilityScore)
	}
}
