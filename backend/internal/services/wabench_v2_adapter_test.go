package services

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func TestLuminbuddyV2AdapterExecutesRealHarness(t *testing.T) {
	var sawHarnessTools atomic.Bool
	var resolvedModel atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request tools.LLMRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode LLM request: %v", err)
		}
		if request.Stream && len(request.Tools) > 0 {
			sawHarnessTools.Store(true)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"## Harness 真实输出\\n\\n这是通过真实 Agent Harness 生成的正文。\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":37}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := tools.NewLLMClient(server.URL, "test-key", "test-model", 2048, 0.1, 5*time.Second)
	resolver := recordingWABenchLLMResolver{client: llm, resolvedModel: &resolvedModel}
	executor := NewHarnessWABenchExecutorWithResolver(resolver, nil, nil, profile.NewLoader(), nil, nil, nil)
	trace, err := executor.Execute(context.Background(), WABenchAgentRequest{
		RunID: "run_contract",
		Input: "请写一篇测试文章",
		Case: database.WABenchCase{
			CaseID: "case_contract", TaskType: "writing", SourceMode: "none",
			InputHash:       "sha256:" + strings.Repeat("a", 64),
			RuleProfileRefs: []string{"luminbuddy.builtin-style.yinyue"},
		},
		Candidate: database.WABenchCandidate{
			ModelManifest: map[string]interface{}{"model": "frozen-candidate-model"},
			FeatureFlags:  map[string]interface{}{"memoryEnabled": false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if trace.Status != engine.StatusCompleted || !strings.Contains(trace.Article, "通过真实 Agent Harness") {
		t.Fatalf("unexpected Harness trace: %+v", trace)
	}
	if trace.ArticleTitle != "Harness 真实输出" || strings.Contains(trace.Article, "## Harness 真实输出") {
		t.Fatalf("article title was not separated from body: %+v", trace)
	}
	if len(trace.StepHistory) != 1 || trace.StepHistory[0].Step != engine.StepArticleOutput {
		t.Fatalf("article output protocol was not recorded: %+v", trace.StepHistory)
	}
	if trace.TaskIntent != "writing" || trace.TotalTokens != 37 {
		t.Fatalf("intent/tokens were not captured from Harness: %+v", trace)
	}
	if !sawHarnessTools.Load() {
		t.Fatal("adapter bypassed Harness tool-enabled execution")
	}
	if model, _ := resolvedModel.Load().(string); model != "frozen-candidate-model" {
		t.Fatalf("candidate model was not used, resolved %q", model)
	}
}

type fixtureWABenchKnowledgeSearcher struct{}

func (fixtureWABenchKnowledgeSearcher) SearchKB(_ context.Context, _ string, _ string, _ int) ([]engine.SearchResult, error) {
	return []engine.SearchResult{{Title: "fixture", Snippet: "fixture", Source: "local_kb"}}, nil
}

func (searcher fixtureWABenchKnowledgeSearcher) SearchKBInKB(ctx context.Context, userID, _ string, query string, limit int) ([]engine.SearchResult, error) {
	return searcher.SearchKB(ctx, userID, query, limit)
}

func TestLuminbuddyV2AdapterLabelsLocalKnowledgeProvider(t *testing.T) {
	var rounds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if rounds.Add(1) == 1 {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_kb\",\"type\":\"function\",\"function\":{\"name\":\"search_knowledge\",\"arguments\":\"{\\\"query\\\":\\\"fixture\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n")
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"## 完成\\n\\n受控输出\"},\"finish_reason\":\"stop\"}],\"usage\":{\"total_tokens\":2}}\n\n")
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	llm := tools.NewLLMClient(server.URL, "test-key", "test-model", 2048, 0.1, 5*time.Second)
	executor := NewHarnessWABenchExecutor(llm, nil, fixtureWABenchKnowledgeSearcher{}, profile.NewLoader(), nil, nil, nil)
	trace, err := executor.Execute(context.Background(), WABenchAgentRequest{
		RunID: "run_local_kb",
		Input: "请参考内部知识库写作",
		Case: database.WABenchCase{
			CaseID: "case_local_kb", TaskType: "writing", SourceMode: "live",
			InputHash: "sha256:" + strings.Repeat("b", 64), RuleProfileRefs: []string{"luminbuddy.builtin-style.yinyue"},
		},
		Candidate: database.WABenchCandidate{ModelManifest: map[string]interface{}{"model": "test-model"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !trace.KnowledgeTriggered || len(trace.KnowledgeProviders) != 1 || trace.KnowledgeProviders[0] != "local-pg-kb" {
		t.Fatalf("local KB routing label = triggered:%v providers:%v", trace.KnowledgeTriggered, trace.KnowledgeProviders)
	}
}

type recordingWABenchLLMResolver struct {
	client        *tools.LLMClient
	resolvedModel *atomic.Value
}

func (r recordingWABenchLLMResolver) GetClient(_ context.Context, model string) *tools.LLMClient {
	r.resolvedModel.Store(model)
	return r.client
}

func TestWABenchCustomStyleReferenceResolvesImmutableVersionIntegration(t *testing.T) {
	databaseURL := os.Getenv("WABENCH_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("set WABENCH_TEST_DATABASE_URL to run PostgreSQL integration test")
	}
	db, err := database.NewPostgres(databaseURL, 5, 2)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := database.Migrate(db); err != nil {
		t.Fatal(err)
	}
	loader := profile.NewLoader()
	builtin, ok := loader.Get("yinyue")
	if !ok {
		t.Fatal("builtin profile missing")
	}
	custom := *builtin
	custom.Slug = "custom-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	custom.Name = "WABench custom style"
	custom.Version = 1
	config, _ := json.Marshal(custom)
	store := database.NewUserStyleStore(db)
	record, err := store.CreateProfile(context.Background(), "00000000-0000-0000-0000-000000000001", custom.Slug, custom.Name, "integration")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveVersion(context.Background(), record.ID, config, "frozen benchmark version"); err != nil {
		t.Fatal(err)
	}
	ref, err := database.UserWABenchRuleProfileRef(record.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	executor := NewHarnessWABenchExecutor(nil, nil, nil, loader, store, nil, nil)
	resolved, err := executor.resolveProfile(context.Background(), []string{ref})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Slug != custom.Slug || resolved.Version != 1 {
		t.Fatalf("resolved custom profile = %s v%d", resolved.Slug, resolved.Version)
	}
}

func TestWABenchPublicRuleProfilesResolveToFrozenBuiltinStyles(t *testing.T) {
	executor := NewHarnessWABenchExecutor(nil, nil, nil, profile.NewLoader(), nil, nil, nil)
	wants := map[string]string{
		"wabench.public.general-writing": "yinyue",
		"wabench.public.deep-commentary": "yinyue",
		"wabench.public.policy-essay":    "shenlun",
		"wabench.public.social-note":     "xiaohongshu",
	}
	for ref, want := range wants {
		got, err := executor.resolveProfile(context.Background(), []string{ref})
		if err != nil {
			t.Fatalf("resolve %s: %v", ref, err)
		}
		if got.Slug != want {
			t.Fatalf("resolve %s = %s, want %s", ref, got.Slug, want)
		}
	}
}
