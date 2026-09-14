package claimverify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSplitClaimsStripsReferencesAndKeepsCitedSentences(t *testing.T) {
	manuscript := "# 标题\n\n## 引言\n\n模型达到了通过阈值[1]。这一发现很重要。\n\n" +
		"> 证据边界：本文由测试生成。\n\n# 参考文献\n\n1. 某文献。2013."
	claims := SplitClaims(manuscript)
	// Only cited sentences enter claim verification — uncited synthesis is a
	// different (non-citation) problem and the reference list must not leak.
	if len(claims) != 1 || !strings.Contains(claims[0], "[1]") {
		t.Fatalf("claims = %v, want only the cited sentence", claims)
	}
	joined := strings.Join(claims, "")
	if strings.Contains(joined, "参考文献") || strings.Contains(joined, "证据边界") || strings.Contains(joined, "这一发现很重要") {
		t.Fatal("reference list, boundary marker, or uncited sentence leaked into claims")
	}
}

func TestCheckReportsVerdictsAndSkipsInvalid(t *testing.T) {
	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&capturedBody)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{
				"message": map[string]any{"content": `{"verdicts":[` +
					`{"index":0,"verdict":"supported","reason":"摘要原话"},` +
					`{"index":1,"verdict":"bogus","reason":"必须跳过"},` +
					`{"index":2,"verdict":"partial","reason":"方向一致强度超出"},` +
					`{"index":99,"verdict":"unsupported","reason":"越界"}` +
					`]}`},
			}},
		})
	}))
	defer server.Close()

	// Same-package construction with a tiny backoff keeps the failure path
	// fast; production New() retries 5× with 30s steps.
	verifier := &Verifier{cfg: Config{Model: "verify-model"}, client: &llmClient{
		baseURL: server.URL, apiKey: "k", model: "m",
		timeout: 5 * time.Second, maxAttempts: 1, backoff: time.Second,
		client: &http.Client{Timeout: 5 * time.Second},
	}}
	sources := []SourceRef{
		{Index: 1, Title: "来源一", Abstract: "摘要一"},
		{Index: 2, Title: "来源二", Abstract: "摘要二"},
		{Index: 3, Title: "来源三", Abstract: "摘要三"},
	}
	manuscript := "模型达到了通过阈值[1]。中间无引用句子被跳过。效果是部分的[2]。未知来源句子[3]。不存在的来源[9]。"
	report, err := verifier.Check(context.Background(), manuscript, "lumin.7-test", sources)
	if err != nil {
		t.Fatal(err)
	}
	// Cited sentences: [1] supported, [2] partial, [3] valid-but-out-of-range
	// index (dropped), and the uncited sentence never enters the list.
	if report.SentencesChecked != 2 {
		t.Fatalf("sentences checked = %d, want 2 (valid verdicts only)", report.SentencesChecked)
	}
	if report.Supported != 1 || report.Partial != 1 || report.Unsupported != 0 {
		t.Fatalf("counts wrong: %+v", report)
	}
	if got := report.Verdicts[0].Sources; len(got) != 1 || got[0] != "[1] 来源一" {
		t.Fatalf("source resolution wrong: %v", got)
	}
	if !strings.Contains(report.Verifier, "verify-model") {
		t.Fatalf("verifier model not recorded: %q", report.Verifier)
	}
	if !strings.Contains(report.GeneratorVersion, "lumin") {
		t.Fatalf("generator version not recorded: %q", report.GeneratorVersion)
	}
	// The prompt must carry the catalog and the numbered sentences.
	prompt, _ := capturedBody["messages"].([]any)[1].(map[string]any)["content"].(string)
	if !strings.Contains(prompt, "来源一") || !strings.Contains(prompt, "达到") {
		t.Fatal("verifier prompt missing catalog or sentences")
	}
}

func TestCheckFailsOnServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()
	verifier := &Verifier{cfg: Config{Model: "m"}, client: &llmClient{
		baseURL: server.URL, apiKey: "k", model: "m",
		timeout: time.Second, maxAttempts: 1, backoff: time.Second,
		client: &http.Client{Timeout: 5 * time.Second},
	}}
	if _, err := verifier.Check(context.Background(), "一句带引用[1]。", "gen", []SourceRef{{Index: 1, Title: "t", Abstract: "a"}}); err == nil {
		t.Fatal("server error must propagate")
	}
}

func TestCheckChunksAndMapsGlobalIndices(t *testing.T) {
	// 12 cited sentences → two chunks of ≤10. The fake relay always answers
	// "supported" for local index 0, so the merged report must carry the two
	// GLOBAL indices 0 and 10 (chunk offset applied), not 0 and 0.
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{
			"message": map[string]any{"content": `{"verdicts":[{"index":0,"verdict":"supported","reason":"r"}]}`},
		}}})
	}))
	defer server.Close()
	verifier := &Verifier{cfg: Config{Model: "m"}, client: &llmClient{
		baseURL: server.URL, apiKey: "k", model: "m",
		timeout: 5 * time.Second, maxAttempts: 1, backoff: time.Second,
		client: &http.Client{Timeout: 5 * time.Second},
	}}
	var sb strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&sb, "这是第%d个带引用的事实句子[1]。", i)
	}
	report, err := verifier.Check(context.Background(), sb.String(), "gen",
		[]SourceRef{{Index: 1, Title: "t", Abstract: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 chunk calls, got %d", calls)
	}
	if report.SentencesChecked != 2 {
		t.Fatalf("checked = %d, want 2 (one per chunk)", report.SentencesChecked)
	}
	if !strings.Contains(report.Verdicts[1].Sentence, "第10个") {
		t.Fatalf("second chunk verdict not offset to global index 10: %q", report.Verdicts[1].Sentence)
	}
}

func TestNewRequiresFullConfig(t *testing.T) {
	if _, err := New(Config{BaseURL: "x"}); err == nil {
		t.Fatal("partial config must be rejected")
	}
	if _, err := New(Config{BaseURL: "x", APIKey: "k", Model: "m"}); err != nil {
		t.Fatalf("full config must pass: %v", err)
	}
}

func TestValidateBaseURLRejectsUnsafeHosts(t *testing.T) {
	for _, bad := range []string{
		"http://localhost:8020", "http://127.0.0.1/v1", "http://10.0.0.5/v1",
		"http://192.168.1.1/v1", "http://169.254.1.1/v1", "ftp://verify.example.com",
		"file:///etc/passwd", "http://foo.internal/v1",
	} {
		if err := ValidateBaseURL(bad); err == nil {
			t.Fatalf("ValidateBaseURL(%q) must fail", bad)
		}
	}
	for _, good := range []string{"https://aispot.example.com/v1", "https://api.vendor.example/v1"} {
		if err := ValidateBaseURL(good); err != nil {
			t.Fatalf("ValidateBaseURL(%q) must pass: %v", good, err)
		}
	}
}

func TestExtractJSONObjectStripsFences(t *testing.T) {
	got, err := extractJSONObject("```json\n{\"verdicts\":[]}\n```")
	if err != nil || got != `{"verdicts":[]}` {
		t.Fatalf("extract = %q err = %v", got, err)
	}
	if _, err := extractJSONObject("no json here"); err == nil {
		t.Fatal("expected error for content without JSON")
	}
	_ = errors.Is
}
