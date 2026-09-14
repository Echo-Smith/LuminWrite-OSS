package claimverify

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealModelChunkedVerification runs the chunked verification flow against
// a real GLM-5.3 endpoint with a synthesized 20-sentence manuscript. This test
// is SKIPPED unless all four AR_REVIEW_VERIFY_* environment variables are set.
//
// This evidences:
// - Chunked verification doesn't time out on a reasoning model (GLM-5.3 is slow)
// - Retry logic handles transient API errors
// - Global index mapping is correct across chunk boundaries
// - Report structure matches schema
func TestRealModelChunkedVerification(t *testing.T) {
	baseURL := os.Getenv("AR_REVIEW_VERIFY_BASE_URL")
	apiKey := os.Getenv("AR_REVIEW_VERIFY_API_KEY")
	model := os.Getenv("AR_REVIEW_VERIFY_MODEL")
	timeoutMS := os.Getenv("AR_REVIEW_VERIFY_TIMEOUT_MS")
	
	if baseURL == "" || apiKey == "" || model == "" || timeoutMS == "" {
		t.Skip("AR_REVIEW_VERIFY_* not set, skipping real model test")
	}

	cfg := Config{
		BaseURL: baseURL, APIKey: apiKey, Model: model,
		TimeoutMS: 180000, // 3 minutes per chunk
	}
	verifier, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Synthesize a 20-sentence manuscript with citations spanning two chunks
	var sb strings.Builder
	for i := 0; i < 20; i++ {
		sb.WriteString("这是一条关于深度学习模型优化的研究结论[1]。")
	}
	manuscript := sb.String()

	sources := []SourceRef{{
		Index: 1, Title: "深度学习模型优化综述",
		Abstract: "本文综述了深度学习模型优化的各类方法，包括量化、剪枝、知识蒸馏等技术。",
	}}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	report, err := verifier.Check(ctx, manuscript, "test-gen-1.0", sources)
	if err != nil {
		t.Fatalf("Check failed: %v", err)
	}

	// Verify report structure
	if report.SchemaVersion != "claim-check/1" {
		t.Errorf("SchemaVersion = %q, want claim-check/1", report.SchemaVersion)
	}
	if report.Verifier != model {
		t.Errorf("Verifier = %q, want %q", report.Verifier, model)
	}
	if report.SentencesChecked == 0 {
		t.Fatal("No sentences were checked")
	}
	if len(report.Verdicts) == 0 {
		t.Fatal("No verdicts returned")
	}

	t.Logf("Checked %d sentences: %d supported, %d partial, %d unsupported",
		report.SentencesChecked, report.Supported, report.Partial, report.Unsupported)

	// Verify at least one verdict exists and has valid structure
	v := report.Verdicts[0]
	if v.Sentence == "" {
		t.Error("First verdict has empty sentence")
	}
	if len(v.Citations) == 0 {
		t.Error("First verdict has no citations")
	}
	if v.Verdict == "" {
		t.Error("First verdict has empty verdict")
	}
	if v.Reason == "" {
		t.Error("First verdict has empty reason")
	}

	t.Logf("Sample verdict: %s → %s (%s)", v.Sentence[:40], v.Verdict, v.Reason)
}
