package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ExperimentRunner 对照实验运行器
type ExperimentRunner struct {
	store     *Store
	llm       *tools.LLMClient
	search    *tools.SearchClient
	embedding *tools.EmbeddingClient
	profiles  *profile.Loader
	orch      *Orchestrator

	// 运行中的实验 cancel 函数集合，支持外部取消
	activeRuns   map[string]context.CancelFunc
	activeRunsMu sync.Mutex
}

// NewExperimentRunner 创建实验运行器
func NewExperimentRunner(
	store *Store,
	llm *tools.LLMClient,
	search *tools.SearchClient,
	embedding *tools.EmbeddingClient,
	profiles *profile.Loader,
	orch *Orchestrator,
) *ExperimentRunner {
	return &ExperimentRunner{
		store: store, llm: llm, search: search, embedding: embedding,
		profiles: profiles, orch: orch,
		activeRuns: make(map[string]context.CancelFunc),
	}
}

// CancelExperiment 取消正在运行的实验
func (r *ExperimentRunner) CancelExperiment(experimentID string) bool {
	r.activeRunsMu.Lock()
	defer r.activeRunsMu.Unlock()
	if cancel, ok := r.activeRuns[experimentID]; ok {
		cancel()
		delete(r.activeRuns, experimentID)
		slog.Info("experiment cancelled", "id", experimentID)
		return true
	}
	return false
}

// RunExperiment 运行对照实验（三组并行，带速率限制保护）
func (r *ExperimentRunner) RunExperiment(ctx context.Context, exp *Experiment) error {
	slog.Info("experiment started", "id", exp.ID, "title", exp.Title)

	// 创建可取消的 context
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// 注册 cancel 函数，支持外部取消
	r.activeRunsMu.Lock()
	r.activeRuns[exp.ID] = cancel
	r.activeRunsMu.Unlock()
	defer func() {
		r.activeRunsMu.Lock()
		delete(r.activeRuns, exp.ID)
		r.activeRunsMu.Unlock()
	}()

	// 更新状态为 running
	if err := r.store.UpdateExperimentStatus(runCtx, exp.ID, "running", nil); err != nil {
		slog.Error("failed to update experiment status", "id", exp.ID, "error", err)
	}

	topic := exp.Title
	if exp.Description != "" {
		topic = exp.Title + ": " + exp.Description
	}

	var wg sync.WaitGroup
	results := make(map[string]ExperimentMetrics)
	var mu sync.Mutex

	// 速率限制：错开启动时间，避免三组同时请求 LLM API 导致 429
	// Pipeline 先启动，10s 后 Unified，20s 后 Editorial
	modes := []struct {
		name     string
		delay    time.Duration
		runFn    func(ctx context.Context, topic, styleSlug string) ExperimentMetrics
	}{
		{name: "pipeline", delay: 0, runFn: r.runPipelineMode},
		{name: "unified", delay: 10 * time.Second, runFn: r.runUnifiedMode},
		{name: "editorial", delay: 20 * time.Second, runFn: r.runEditorialMode},
	}

	for _, m := range modes {
		m := m // capture
		wg.Add(1)
		go func() {
			defer wg.Done()

			// 速率限制延迟
			if m.delay > 0 {
				select {
				case <-runCtx.Done():
					mu.Lock()
					results[m.name] = ExperimentMetrics{Mode: m.name, Error: "cancelled before start"}
					mu.Unlock()
					return
				case <-time.After(m.delay):
				}
			}

			metrics := m.runFn(runCtx, topic, exp.StyleSlug)
			mu.Lock()
			results[m.name] = metrics
			mu.Unlock()

			resultJSON, _ := json.Marshal(metrics)
			if err := r.store.UpdateExperimentResult(runCtx, exp.ID, m.name, resultJSON); err != nil {
				slog.Error("failed to update experiment result", "id", exp.ID, "mode", m.name, "error", err)
			}
			slog.Info("experiment mode done", "id", exp.ID, "mode", m.name, "tokens", metrics.TokenCost, "duration_ms", metrics.DurationMs)
		}()
	}

	// 等待所有模式完成或 context 取消
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()

	select {
	case <-waitCh:
		// 正常完成
	case <-runCtx.Done():
		// context 取消，等待 goroutine 退出
		slog.Warn("experiment context cancelled, waiting for goroutines", "id", exp.ID)
		wg.Wait()
	}

	// 生成汇总
	summary := r.buildSummary(results)
	summaryJSON, _ := json.Marshal(summary)

	// 判断最终状态
	finalStatus := "completed"
	if runCtx.Err() != nil {
		finalStatus = "failed"
		summary["cancel_reason"] = runCtx.Err().Error()
	}

	if err := r.store.UpdateExperimentStatus(context.Background(), exp.ID, finalStatus, summaryJSON); err != nil {
		slog.Error("failed to update experiment final status", "id", exp.ID, "error", err)
	}

	slog.Info("experiment finished", "id", exp.ID, "status", finalStatus, "summary", string(summaryJSON))
	return nil
}

// runPipelineMode 运行 Pipeline 模式
func (r *ExperimentRunner) runPipelineMode(ctx context.Context, topic, styleSlug string) ExperimentMetrics {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("pipeline mode panicked", "error", r)
		}
	}()

	execCtx := engine.NewExecutionContext("exp_pipeline_"+time.Now().Format("150405"), "experiment", topic)
	execCtx.StyleSlug = styleSlug
	execCtx.Mode = "guided"
	execCtx.MaxTokens = 300000

	// 设置意图为 writing
	execCtx.TaskIntent = &engine.TaskIntent{TaskMode: "writing"}

	emitter := &noopEmitter{}

	// 构建 Pipeline steps
	var pipelineSteps []engine.Step
	pipelineSteps = append(pipelineSteps,
		steps.NewQueryPlanStep(r.llm),
		steps.NewSearchStep(r.llm, r.search),
		steps.NewRelevanceStepWithEmbedding(r.embedding),
		steps.NewCompressStep(r.llm),
	)
	if r.profiles != nil {
		if p, ok := r.profiles.Get(styleSlug); ok {
			pipelineSteps = append(pipelineSteps,
				steps.NewOutlineStep(r.llm),
				steps.NewWriteStepWithSearch(r.llm, p, r.search),
			)
		}
	}

	eng := engine.NewAgentEngine(emitter, pipelineSteps)
	execCtx.ConfirmTimeout = 1 * time.Second // 自动确认提纲

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := eng.Run(runCtx, execCtx); err != nil {
		return ExperimentMetrics{
			Mode:      "pipeline",
			DurationMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}

	wordCount := len([]rune(execCtx.Article))
	excerpt := execCtx.Article
	if len([]rune(excerpt)) > 200 {
		excerpt = string([]rune(excerpt)[:200])
	}

	return ExperimentMetrics{
		Mode:           "pipeline",
		TokenCost:      execCtx.TotalTokens,
		DurationMs:     time.Since(start).Milliseconds(),
		WordCount:      wordCount,
		SourceCount:    len(execCtx.SearchResults),
		ArticleTitle:   execCtx.ArticleTitle,
		ArticleExcerpt: excerpt,
		QualityScore:   0.5, // Pipeline 无审校，默认中等
	}
}

// runUnifiedMode 运行 Unified Agent 模式
func (r *ExperimentRunner) runUnifiedMode(ctx context.Context, topic, styleSlug string) ExperimentMetrics {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("unified mode panicked", "error", r)
		}
	}()

	execCtx := engine.NewExecutionContext("exp_unified_"+time.Now().Format("150405"), "experiment", topic)
	execCtx.StyleSlug = styleSlug
	execCtx.Mode = "guided"
	execCtx.MaxTokens = 300000
	execCtx.TaskIntent = &engine.TaskIntent{TaskMode: "writing"}

	emitter := &noopEmitter{}

	// 构建工具注册表
	registry := engine.NewToolRegistry()
	registry.Register(engine.NewStepTool(
		steps.NewQueryPlanStep(r.llm),
		"检索规划：从用户输入提取话题和搜索查询",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewSearchStep(r.llm, r.search),
		"多源搜索：并发执行搜索，返回结果",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewRelevanceStepWithEmbedding(r.embedding),
		"相关性过滤：对搜索结果评分和去重",
		false,
	))
	registry.Register(engine.NewStepTool(
		steps.NewCompressStep(r.llm),
		"素材压缩：将搜索结果压缩为研究简报",
		false,
	))
	if r.profiles != nil {
		if p, ok := r.profiles.Get(styleSlug); ok {
			registry.Register(engine.NewStepTool(
				steps.NewOutlineStep(r.llm),
				"提纲生成：为引导模式生成文章提纲",
				false,
			))
			registry.Register(engine.NewStepTool(
				steps.NewWriteStepWithSearch(r.llm, p, r.search),
				"文章生成：按风格生成文章",
				true,
			))
		}
	}

	unifiedAgent := agent.NewUnifiedAgent(registry, r.llm, emitter)
	execCtx.ConfirmTimeout = 1 * time.Second

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := unifiedAgent.Run(runCtx, execCtx); err != nil {
		return ExperimentMetrics{
			Mode:      "unified",
			DurationMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}

	wordCount := len([]rune(execCtx.Article))
	excerpt := execCtx.Article
	if len([]rune(excerpt)) > 200 {
		excerpt = string([]rune(excerpt)[:200])
	}

	return ExperimentMetrics{
		Mode:           "unified",
		TokenCost:      execCtx.TotalTokens,
		DurationMs:     time.Since(start).Milliseconds(),
		WordCount:      wordCount,
		SourceCount:    len(execCtx.SearchResults),
		ArticleTitle:   execCtx.ArticleTitle,
		ArticleExcerpt: excerpt,
		QualityScore:   0.5,
	}
}

// runEditorialMode 运行 Editorial 模式
func (r *ExperimentRunner) runEditorialMode(ctx context.Context, topic, styleSlug string) ExperimentMetrics {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("editorial mode panicked", "error", r)
		}
	}()

	if r.orch == nil {
		return ExperimentMetrics{Mode: "editorial", Error: "orchestrator not available"}
	}

	// 创建编辑部任务
	task, err := r.store.CreateTask(ctx, CreateTaskInput{
		Title:       topic,
		Description: "对照实验自动创建",
		StyleSlug:   styleSlug,
		TokenBudget: 300000,
		Priority:    1,
	}, "experiment")
	if err != nil {
		return ExperimentMetrics{Mode: "editorial", Error: fmt.Sprintf("create task: %v", err)}
	}

	// 创建并自动批准选题卡（研究 Agent 需要 approved 状态的选题卡作为输入）
	topicCardContent, _ := json.Marshal(map[string]interface{}{
		"title":       task.Title,
		"description": task.Description,
		"style_slug":  task.StyleSlug,
	})
	topicCard, err := r.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactTopicCard,
		Content:    string(topicCardContent),
		ProducedBy: "human",
	}, task.ID)
	if err != nil {
		return ExperimentMetrics{Mode: "editorial", Error: fmt.Sprintf("create topic card: %v", err)}
	}
	r.store.ReviewArtifact(ctx, topicCard.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "system",
		ReviewNote: "实验自动批准",
	})

	// 推进状态链：draft → pending_approval → research
	if err := r.orch.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusPendingApproval,
		DecidedBy:    "experiment",
		Rationale:    "实验自动提交审批",
	}); err != nil {
		return ExperimentMetrics{Mode: "editorial", Error: fmt.Sprintf("advance to pending_approval: %v", err)}
	}
	if err := r.orch.AdvanceTask(ctx, task.ID, AdvanceTaskInput{
		TargetStatus: StatusResearch,
		DecidedBy:    "experiment",
		Rationale:    "实验自动批准立项",
	}); err != nil {
		return ExperimentMetrics{Mode: "editorial", Error: fmt.Sprintf("advance to research: %v", err)}
	}

	// 等待任务完成（轮询，最多 5 分钟）
	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	var finalStatus TaskStatus
	for {
		select {
		case <-runCtx.Done():
			return ExperimentMetrics{
				Mode:      "editorial",
				DurationMs: time.Since(start).Milliseconds(),
				Error:     "timeout waiting for editorial task to complete",
			}
		case <-ticker.C:
			t, err := r.store.GetTask(runCtx, task.ID)
			if err != nil {
				continue
			}
			finalStatus = t.Status
			// 等待到终态
			if t.Status == StatusPendingPublish || t.Status == StatusPublished ||
				t.Status == StatusPendingApproval || t.Status == StatusDraft {
				goto done
			}
		}
	}

done:
	// 采集指标
	task, _ = r.store.GetTask(ctx, task.ID)
	metrics := ExperimentMetrics{
		Mode:       "editorial",
		TokenCost:  task.TokenUsed,
		DurationMs: time.Since(start).Milliseconds(),
	}

	// 获取初稿
	if draft, err := r.store.GetLatestApprovedArtifact(ctx, task.ID, ArtifactDraft); err == nil && draft != nil {
		var draftData struct {
			Title      string `json:"title"`
			Content    string `json:"content"`
			WordCount  int    `json:"word_count"`
		}
		if json.Unmarshal([]byte(draft.Content), &draftData) == nil {
			metrics.ArticleTitle = draftData.Title
			metrics.WordCount = draftData.WordCount
			if metrics.WordCount == 0 {
				metrics.WordCount = len([]rune(draftData.Content))
			}
			excerpt := draftData.Content
			if len([]rune(excerpt)) > 200 {
				excerpt = string([]rune(excerpt)[:200])
			}
			metrics.ArticleExcerpt = excerpt
		}
	}

	// 获取信源包
	if sp, err := r.store.GetLatestApprovedArtifact(ctx, task.ID, ArtifactSourcePack); err == nil && sp != nil {
		var sourceData struct {
			Count int `json:"count"`
		}
		if json.Unmarshal([]byte(sp.Content), &sourceData) == nil {
			metrics.SourceCount = sourceData.Count
		}
	}

	// 获取审查报告
	if rr, err := r.store.GetLatestApprovedArtifact(ctx, task.ID, ArtifactReviewReport); err == nil && rr != nil {
		var report struct {
			Passed   bool     `json:"passed"`
			Severity string   `json:"severity"`
			Issues   []struct {
				Severity string `json:"severity"`
			} `json:"issues"`
		}
		if json.Unmarshal([]byte(rr.Content), &report) == nil {
			metrics.ReviewPassed = report.Passed
			metrics.IssueCount = len(report.Issues)
			if report.Passed {
				metrics.QualityScore = 0.8
			} else if report.Severity == "high" {
				metrics.QualityScore = 0.3
			} else {
				metrics.QualityScore = 0.5
			}
		}
	}

	_ = finalStatus // 仅用于日志
	return metrics
}

// buildSummary 构建对比汇总
func (r *ExperimentRunner) buildSummary(results map[string]ExperimentMetrics) map[string]interface{} {
	summary := map[string]interface{}{
		"modes": []string{"pipeline", "unified", "editorial"},
	}

	// 找出每种指标的最优值
	var bestTokens, bestDuration, bestQuality string
	minTokens := int(^uint(0) >> 1) // max int
	minDuration := int64(^uint(0) >> 1)
	maxQuality := -1.0

	for mode, m := range results {
		if m.Error != "" {
			continue
		}
		if m.TokenCost < minTokens {
			minTokens = m.TokenCost
			bestTokens = mode
		}
		if m.DurationMs < minDuration {
			minDuration = m.DurationMs
			bestDuration = mode
		}
		if m.QualityScore > maxQuality {
			maxQuality = m.QualityScore
			bestQuality = mode
		}
	}

	summary["best_token_efficiency"] = bestTokens
	summary["best_speed"] = bestDuration
	summary["best_quality"] = bestQuality
	summary["min_tokens"] = minTokens
	summary["min_duration_ms"] = minDuration
	summary["max_quality_score"] = maxQuality

	return summary
}
