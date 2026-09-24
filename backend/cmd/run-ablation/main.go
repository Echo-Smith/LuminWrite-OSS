package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	memportadapter "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport/adapter"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// defaultRunIDs 返回消融重跑的默认四个 run ID（旧命名，与 seed 脚本
// seedAblationRuns 生成的 run_id 一致）。
func defaultRunIDs() []string {
	return []string{
		"ablation-run-ablation-A-baseline",
		"ablation-run-ablation-B-context",
		"ablation-run-ablation-C-context-memory",
		"ablation-run-ablation-D-full",
	}
}

func main() {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://postgres:postgres@localhost:5432/writing_agent_v2?sslmode=disable"
	}

	db, err := database.NewPostgres(dbURL, 10, 4)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	repo := database.NewWABenchRepo(db)

	// 凭据只从环境注入：LLM_API_KEY 必填（fail-closed），绝不定死在源码里。
	apiKey := os.Getenv("LLM_API_KEY")
	if apiKey == "" {
		log.Fatal("LLM_API_KEY must be set in the environment")
	}
	llm := tools.NewLLMClient(
		envDefault("LLM_BASE_URL", "https://api.xiaomimimo.com/v1"),
		apiKey,
		envDefault("LLM_MODEL", "mimo-v2.5"),
		32768, 0.7, 120*time.Second,
	)

	// ── Memory port（修复上次消融的关键缺陷）──
	// 上次消融把 memoryPort 传成 nil，adapter（wabench_v2_adapter.go 的
	// `memoryEnabled && e.memoryPort != nil` 分支）对 nil port 会静默跳过
	// 注入，导致 C/D 变体的用户记忆实际上从未生效。这里按服务端 WABench
	// 路径（server.go / server_adapters.go）的同款方式构造真实 port；
	// 只有构造失败（DB 不可达 / 服务不可用）才允许降级 nil 并打印显著 WARN。
	memoryPort := buildMemoryPort(db, llm)

	adapter := services.NewHarnessWABenchExecutor(llm, nil, nil, profile.NewLoader(), nil, memoryPort)
	judge := services.NewLLMWABenchJudge(llm)
	svc := services.NewWABenchEvaluationService(repo, adapter, judge)

	// ── Run ID 选择 ──
	// 优先级：RUN_IDS（逗号分隔，可一次跑多个）> RUN_ID（单个，旧兼容）>
	// 默认四个 ID。
	runIDs := defaultRunIDs()
	if raw := os.Getenv("RUN_IDS"); raw != "" {
		ids := parseRunIDList(raw)
		if len(ids) > 0 {
			runIDs = ids
		}
	} else if rid := strings.TrimSpace(os.Getenv("RUN_ID")); rid != "" {
		runIDs = []string{rid}
	}

	// ── 超时 ──
	// ABLATION_TIMEOUT 使用 Go duration 格式（如 "2h"、"90m"、"1h30m"），
	// 由 time.ParseDuration 解析；未设置时保持默认 2 小时。
	timeout := 2 * time.Hour
	if raw := strings.TrimSpace(os.Getenv("ABLATION_TIMEOUT")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			log.Fatalf("invalid ABLATION_TIMEOUT %q: want a Go duration like 2h / 90m / 1h30m", raw)
		}
		timeout = d
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var wg sync.WaitGroup
	errs := make([]error, len(runIDs))
	for i, rid := range runIDs {
		wg.Add(1)
		go func(i int, rid string) {
			defer wg.Done()
			fmt.Printf("Starting run %s...\n", rid)
			if err := svc.ExecuteRun(ctx, rid); err != nil {
				errs[i] = err
				fmt.Printf("FAILED %s: %v\n", rid, err)
			} else {
				fmt.Printf("COMPLETED %s\n", rid)
			}
		}(i, rid)
	}
	wg.Wait()
	failed := false
	for i, err := range errs {
		if err != nil {
			failed = true
			fmt.Printf("  %s: %v\n", runIDs[i], err)
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("ALL RUNS COMPLETED")
}

// buildMemoryPort 按服务端 WABench 路径的同款构造组装只读记忆消费 Port
// （PrepareInjection 只读，绝不 SubmitOutcome）：
//
//	memsvc.NewService(db, llm, embeddingClient, sensitiveSvc)
//	memportadapter.NewServiceAdapter(memSvc, nil) // 与 server.memoryPort() 一致，不带实体画像
//
// embedder 说明：DashscopeEmbedder 包着 EmbeddingClient；当 DASHSCOPE_API_KEY
// 未配置时传 nil client——其 Embed 返回 (nil, nil)，pkg/memory 的 Gate 对空
// queryVector 会跳过语义检索、union 召回（关键词 ∪ 最近活跃）仍然工作，
// 即语义降级但不 panic、不注入失败。只有 DB 不可达或服务不可用这种真正的
// 构造失败才降级返回 nil（禁用注入）并打印显著 WARN。
func buildMemoryPort(db *database.DB, llm *tools.LLMClient) memoryport.Port {
	embeddingClient := tools.NewEmbeddingClient(
		os.Getenv("DASHSCOPE_API_KEY"),
		envDefault("DASHSCOPE_BASE_URL", "https://dashscope.aliyuncs.com/compatible-mode/v1"),
		envDefault("DASHSCOPE_MODEL", "text-embedding-v3"),
		1024,
	)
	if !embeddingClient.IsConfigured() {
		log.Println("WARN [run-ablation] DASHSCOPE_API_KEY not set: " +
			"memory semantic recall degrades to keyword/recency union recall (no panic, injection still works)")
		embeddingClient = nil
	}
	// 敏感词检查器依赖 adminRepo（服务端专用），CLI 侧传 nil：
	// memsvc.NewService 对 nil sensitiveCheck 的处理是跳过 PII 检查器，
	// 不影响只读注入路径。
	memSvc := memsvc.NewService(db, llm, embeddingClient, nil)
	if memSvc == nil || !memSvc.IsAvailable() {
		log.Println("WARN [run-ablation] MEMORY PORT UNAVAILABLE (db unreachable or service disabled): " +
			"falling back to nil memoryPort; memoryEnabled candidates (C/D) will run WITHOUT memory injection")
		return nil
	}
	log.Println("memory port ready: read-only injection enabled for memoryEnabled candidates")
	return memportadapter.NewServiceAdapter(memSvc, nil)
}

// parseRunIDList 解析逗号分隔的 run ID 列表，去除空白并跳过空项。
func parseRunIDList(raw string) []string {
	var ids []string
	for _, part := range strings.Split(raw, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func envDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
