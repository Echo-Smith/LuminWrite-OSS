package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// harnessAgentRunner 适配 Harness 到 agent runner 接口。
type harnessAgentRunner struct {
	harness *agent.Harness
	session *agent.WritingSession
}

func (r *harnessAgentRunner) Run(ctx context.Context, execCtx *engine.ExecutionContext) error {
	return r.harness.Run(ctx, execCtx, r.session)
}

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
// 4.1: 冻结信源快照 — 三组共享同一份搜索结果，消除搜索差异
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

	// ── 4.1: 冻结信源快照 — 在三组启动前执行一次搜索 ──
	var frozenResults []engine.SearchResult
	if r.search != nil && r.search.HasSources() {
		slog.Info("experiment: freezing search snapshot", "id", exp.ID, "topic", topic)
		frozenResults = r.search.Search(runCtx, topic, 20)
		slog.Info("experiment: search snapshot frozen", "id", exp.ID, "results", len(frozenResults))
	} else {
		slog.Warn("experiment: no search sources, skipping frozen snapshot", "id", exp.ID)
	}

	var wg sync.WaitGroup
	results := make(map[string]ExperimentMetrics)
	var mu sync.Mutex

	// 速率限制：错开启动时间，避免三组同时请求 LLM API 导致 429
	// Pipeline 先启动，10s 后 Unified，20s 后 Editorial
	modes := []struct {
		name     string
		delay    time.Duration
		runFn    func(ctx context.Context, topic, styleSlug string, frozen []engine.SearchResult) ExperimentMetrics
	}{
		{name: "pipeline", delay: 0, runFn: r.runPipelineMode},
		{name: "harness", delay: 10 * time.Second, runFn: r.runHarnessMode},
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

			metrics := m.runFn(runCtx, topic, exp.StyleSlug, frozenResults)
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

	// ── 4.2: 独立盲评 — 三组完成后用 LLM 盲评打分 ──
	if runCtx.Err() == nil && r.llm != nil {
		slog.Info("experiment: starting blind judge", "id", exp.ID)
		judgeScores := r.blindJudge(context.Background(), results)
		for mode, scores := range judgeScores {
			if m, ok := results[mode]; ok {
				m.JudgeScores = scores
				m.QualityScore = scores.Overall // 用盲评分数替换硬编码的质量分
				results[mode] = m
				// 更新 DB 中的结果
				resultJSON, _ := json.Marshal(m)
				if err := r.store.UpdateExperimentResult(context.Background(), exp.ID, mode, resultJSON); err != nil {
					slog.Error("failed to update experiment result with judge scores", "id", exp.ID, "mode", mode, "error", err)
				}
			}
		}
		slog.Info("experiment: blind judge completed", "id", exp.ID, "scored_modes", len(judgeScores))
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
func (r *ExperimentRunner) runPipelineMode(ctx context.Context, topic, styleSlug string, frozen []engine.SearchResult) ExperimentMetrics {
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

	// 4.1: 注入冻结信源快照
	if len(frozen) > 0 {
		execCtx.SearchResults = frozen
		execCtx.FrozenSearchResults = true
	}

	emitter := &noopEmitter{}

	// 构建 Pipeline steps（含审校+修正，与生产 Pipeline 一致）
	var pipelineSteps []engine.Step
	pipelineSteps = append(pipelineSteps,
		steps.NewQueryPlanStep(r.llm),
		steps.NewSearchStep(r.llm, r.search),
		steps.NewRelevanceStepWithEmbedding(r.embedding),
		steps.NewCompressStep(r.llm),
	)
	var styleProfile *profile.StyleProfile
	if r.profiles != nil {
		styleProfile, _ = r.profiles.Get(styleSlug)
	}
	if styleProfile != nil {
		pipelineSteps = append(pipelineSteps,
			steps.NewOutlineStep(r.llm),
			steps.NewWriteStepWithSearch(r.llm, styleProfile, r.search),
			steps.NewPostReviewStepWithProfile(r.llm, nil, styleProfile),
			steps.NewAutoFixStepWithProfile(r.llm, styleProfile),
			steps.NewPostReviewStepWithProfile(r.llm, nil, styleProfile), // re-review after fix
		)
	} else {
		pipelineSteps = append(pipelineSteps,
			steps.NewOutlineStep(r.llm),
			steps.NewWriteStepWithSearch(r.llm, nil, r.search),
		)
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

	// 从 execCtx.ReviewResult 采集审校结果
	metrics := ExperimentMetrics{
		Mode:           "pipeline",
		TokenCost:      execCtx.TotalTokens,
		DurationMs:     time.Since(start).Milliseconds(),
		WordCount:      wordCount,
		SourceCount:    len(execCtx.SearchResults),
		ArticleTitle:   execCtx.ArticleTitle,
		ArticleExcerpt: excerpt,
		FullArticle:    execCtx.Article,
	}
	if execCtx.ReviewResult != nil {
		metrics.ReviewPassed = execCtx.ReviewResult.Passed
		metrics.IssueCount = len(execCtx.ReviewResult.Issues)
		if execCtx.ReviewResult.Passed {
			metrics.QualityScore = 0.8
		} else {
			metrics.QualityScore = 0.5
		}
	} else {
		metrics.QualityScore = 0.5 // 无审校结果时默认中等
	}

	return metrics
}

// runHarnessMode 运行 Harness 模式（架构 C）
func (r *ExperimentRunner) runHarnessMode(ctx context.Context, topic, styleSlug string, frozen []engine.SearchResult) ExperimentMetrics {
	start := time.Now()
	defer func() {
		if r := recover(); r != nil {
			slog.Error("harness mode panicked", "error", r)
		}
	}()

	execCtx := engine.NewExecutionContext("exp_harness_"+time.Now().Format("150405"), "experiment", topic)
	execCtx.StyleSlug = styleSlug
	execCtx.Mode = "guided"
	execCtx.MaxTokens = 300000
	execCtx.TaskIntent = &engine.TaskIntent{TaskMode: "writing"}

	// 4.1: 注入冻结信源快照
	if len(frozen) > 0 {
		execCtx.SearchResults = frozen
		execCtx.FrozenSearchResults = true
	}

	emitter := &noopEmitter{}

	var styleProfile *profile.StyleProfile
	if r.profiles != nil {
		styleProfile, _ = r.profiles.Get(styleSlug)
	}

	session := agent.NewWritingSession(execCtx.ConversationID, "experiment", styleSlug)
	if len(frozen) > 0 {
		for _, sr := range frozen {
			session.SearchResults = append(session.SearchResults, sr)
		}
	}

	harness := agent.NewHarness(r.llm, r.search, nil, styleProfile, nil, emitter)
	harnessRunner := &harnessAgentRunner{harness: harness, session: session}

	execCtx.ConfirmTimeout = 1 * time.Second

	runCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if err := harnessRunner.Run(runCtx, execCtx); err != nil {
		return ExperimentMetrics{
			Mode:      "harness",
			DurationMs: time.Since(start).Milliseconds(),
			Error:     err.Error(),
		}
	}

	wordCount := len([]rune(execCtx.Article))
	excerpt := execCtx.Article
	if len([]rune(excerpt)) > 200 {
		excerpt = string([]rune(excerpt)[:200])
	}

	// 从 session.ReviewResult 采集审校结果（Harness 的 review_article 工具会设置）
	metrics := ExperimentMetrics{
		Mode:           "harness",
		TokenCost:      execCtx.TotalTokens,
		DurationMs:     time.Since(start).Milliseconds(),
		WordCount:      wordCount,
		SourceCount:    len(session.SearchResults),
		ArticleTitle:   execCtx.ArticleTitle,
		ArticleExcerpt: excerpt,
		FullArticle:    execCtx.Article,
	}
	if session.ReviewResult != nil {
		metrics.ReviewPassed = session.ReviewResult.Passed
		metrics.IssueCount = len(session.ReviewResult.Issues)
		if session.ReviewResult.Passed {
			metrics.QualityScore = 0.8
		} else {
			metrics.QualityScore = 0.5
		}
	} else {
		metrics.QualityScore = 0.5
	}

	return metrics
}

// runEditorialMode 运行 Editorial 模式
// 4.1: 使用冻结信源快照直接创建研究交付物，跳过研究 Agent 搜索
func (r *ExperimentRunner) runEditorialMode(ctx context.Context, topic, styleSlug string, frozen []engine.SearchResult) ExperimentMetrics {
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

	// 4.1: 如果有冻结信源快照，直接创建研究交付物，跳过研究 Agent 搜索
	if len(frozen) > 0 {
		// 构建信源包
		sources := make([]map[string]interface{}, 0, len(frozen))
		factClaims := make([]map[string]interface{}, 0)
		for _, sr := range frozen {
			sources = append(sources, map[string]interface{}{
				"url":       sr.URL,
				"source":    sr.Source,
				"relevance": sr.Relevance,
			})
			if sr.Relevance == "strong" || sr.Relevance == "medium" {
				factClaims = append(factClaims, map[string]interface{}{
					"claim":      sr.Snippet,
					"source_url": sr.URL,
					"source":     sr.Source,
					"relevance":  sr.Relevance,
					"verified":   false,
				})
			}
		}

		briefData, _ := json.Marshal(map[string]interface{}{
			"summary": "冻结信源快照（跳过研究 Agent 搜索）",
			"sources": sources,
			"claims":  factClaims,
			"gaps":    []string{},
		})
		briefArtifact, err := r.store.CreateArtifact(ctx, SubmitArtifactInput{
			Type:       ArtifactResearchBrief,
			Content:    string(briefData),
			ProducedBy: "experiment_frozen",
			TokenCost:  0,
		}, task.ID)
		if err != nil {
			return ExperimentMetrics{Mode: "editorial", Error: fmt.Sprintf("create frozen brief: %v", err)}
		}
		r.store.ReviewArtifact(ctx, briefArtifact.ID, ReviewArtifactInput{
			Status:     ArtifactStatusApproved,
			ReviewerID: "system",
			ReviewNote: "冻结快照自动批准",
		})

		// 创建信源包 Artifact
		sourcePackData, _ := json.Marshal(map[string]interface{}{
			"sources": frozen,
			"count":   len(frozen),
		})
		sourcePackArtifact, _ := r.store.CreateArtifact(ctx, SubmitArtifactInput{
			Type:       ArtifactSourcePack,
			Content:    string(sourcePackData),
			ProducedBy: "experiment_frozen",
			TokenCost:  0,
		}, task.ID)
		if sourcePackArtifact != nil {
			r.store.ReviewArtifact(ctx, sourcePackArtifact.ID, ReviewArtifactInput{
				Status:     ArtifactStatusApproved,
				ReviewerID: "system",
				ReviewNote: "冻结快照自动批准",
			})
		}

		// 直接推进到写作阶段，跳过研究 Agent
		r.store.TransitionTask(context.Background(), TransitionCommand{
			TaskID:         task.ID,
			TargetStatus:   StatusWriting,
			ExpectedStatus: StatusResearch,
		})
		// 触发写作 Agent
		updatedTask, _ := r.store.GetTask(ctx, task.ID)
		if updatedTask != nil {
			r.orch.RunWritingAgent(context.Background(), updatedTask)
		}
		slog.Info("experiment: editorial mode using frozen snapshot", "id", task.ID, "sources", len(frozen))
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
			metrics.FullArticle = draftData.Content
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

// ─── 4.2: LLM-as-Judge 盲评 ──────────────────────────────────

// blindJudge 使用 LLM 对三组文章进行独立盲评
// 文章被打乱顺序后用同一 prompt 评分，消除模式偏见
func (r *ExperimentRunner) blindJudge(ctx context.Context, results map[string]ExperimentMetrics) map[string]*BlindJudgeScores {
	if r.llm == nil {
		slog.Warn("blind judge: LLM not available, skipping")
		return nil
	}

	// 收集有文章的模式（排除出错的）
	type entry struct {
		mode   string
		article string
	}
	var entries []entry
	for mode, m := range results {
		if m.Error != "" || m.FullArticle == "" {
			continue
		}
		entries = append(entries, entry{mode: mode, article: m.FullArticle})
	}
	if len(entries) == 0 {
		slog.Warn("blind judge: no articles to evaluate")
		return nil
	}

	scores := make(map[string]*BlindJudgeScores)
	for _, e := range entries {
		score, err := r.judgeSingleArticle(ctx, e.article)
		if err != nil {
			slog.Warn("blind judge: failed to evaluate article", "mode", e.mode, "error", err)
			continue
		}
		scores[e.mode] = score
		slog.Info("blind judge: scored", "mode", e.mode, "overall", score.Overall)
	}

	return scores
}

// judgeSingleArticle 用 LLM 评估单篇文章（盲评，不暴露来源模式）
func (r *ExperimentRunner) judgeSingleArticle(ctx context.Context, article string) (*BlindJudgeScores, error) {
	prompt := fmt.Sprintf(`你是一位资深中文媒体编辑和写作评委。请对以下文章进行独立评分。

评分维度（每项 0.0-1.0，保留两位小数）：
1. accuracy（事实准确性）：事实陈述是否准确、引用是否可靠
2. structure（结构逻辑性）：文章结构是否清晰、论证逻辑是否连贯
3. style（风格表达力）：语言是否生动、修辞是否得当、风格是否统一
4. insight（深度洞察）：观点是否有深度、是否有独到见解
5. readability（可读性）：是否流畅易读、节奏是否舒适
6. safety（安全合规）：是否存在敏感内容、不当言论

请以 JSON 格式返回评分结果，格式如下：
{"accuracy": 0.85, "structure": 0.80, "style": 0.75, "insight": 0.70, "readability": 0.82, "safety": 0.95, "overall": 0.80, "comment": "简要评语"}

其中 overall 为加权平均：accuracy 20%%, structure 20%%, style 15%%, insight 20%%, readability 15%%, safety 10%%

请只返回 JSON，不要其他文字。

文章内容：
---
%s
---`, article)

	resp, _, err := r.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "user", Content: prompt},
	}, tools.WithTemperature(0.2), tools.WithThinking(false))
	if err != nil {
		return nil, fmt.Errorf("judge LLM call: %w", err)
	}

	// 解析 JSON 响应
	resp = strings.TrimSpace(resp)
	// 去除可能的 markdown 代码块包裹
	resp = strings.TrimPrefix(resp, "```json")
	resp = strings.TrimPrefix(resp, "```")
	resp = strings.TrimSuffix(resp, "```")
	resp = strings.TrimSpace(resp)

	var scores BlindJudgeScores
	if err := json.Unmarshal([]byte(resp), &scores); err != nil {
		return nil, fmt.Errorf("parse judge response: %w (raw: %s)", err, resp[:min(200, len(resp))])
	}

	// 如果 overall 未计算，手动计算
	if scores.Overall == 0 {
		scores.Overall = scores.Accuracy*0.20 + scores.Structure*0.20 + scores.Style*0.15 +
			scores.Insight*0.20 + scores.Readability*0.15 + scores.Safety*0.10
	}

	return &scores, nil
}

// buildSummary 构建对比汇总
func (r *ExperimentRunner) buildSummary(results map[string]ExperimentMetrics) map[string]interface{} {
	summary := map[string]interface{}{
		"modes": []string{"pipeline", "harness", "editorial"},
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

	// 4.2: 添加盲评详细对比
	judgeComparison := make(map[string]interface{})
	for mode, m := range results {
		if m.JudgeScores != nil {
			judgeComparison[mode] = m.JudgeScores
		}
	}
	if len(judgeComparison) > 0 {
		summary["blind_judge_scores"] = judgeComparison
	}

	return summary
}
