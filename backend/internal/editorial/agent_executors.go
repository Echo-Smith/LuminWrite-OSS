package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── 研究 Agent 执行器 ───────────────────────────────────
//
// 复用 V2 的 QueryPlanStep + SearchStep + RelevanceStep + CompressStep
// 产出 Artifact: research_brief + source_pack + fact_claims

// ResearchAgentExecutor 研究 Agent 执行器
type ResearchAgentExecutor struct {
	llm      *tools.LLMClient
	search   *tools.SearchClient
	embedding *tools.EmbeddingClient
	store    *Store
}

// NewResearchAgentExecutor 创建研究 Agent 执行器
func NewResearchAgentExecutor(llm *tools.LLMClient, search *tools.SearchClient, embedding *tools.EmbeddingClient, store *Store) *ResearchAgentExecutor {
	return &ResearchAgentExecutor{
		llm: llm, search: search, embedding: embedding, store: store,
	}
}

func (e *ResearchAgentExecutor) Role() AgentRole { return RoleResearch }

func (e *ResearchAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	// 构建一个临时的 ExecutionContext 用于复用 V2 Steps
	execCtx := engine.NewExecutionContext("task_"+task.ID, task.OwnerID, task.Description)
	execCtx.StyleSlug = task.StyleSlug
	execCtx.Mode = "auto"
	execCtx.MaxTokens = task.TokenBudget - task.TokenUsed

	// 加载选题卡作为输入
	if topicCard := ac.GetArtifact(ArtifactTopicCard); topicCard != nil {
		execCtx.UserInput = topicCard.Content
	} else {
		execCtx.UserInput = task.Title + ": " + task.Description
	}

	// ── 注入组织知识 ──
	if org := ac.GetOrgKnowledge(); org != nil {
		// 注入高可信度信源提示
		if len(org.TopSources) > 0 {
			var sourceHints []string
			for _, s := range org.TopSources {
				sourceHints = append(sourceHints, fmt.Sprintf("%s (可信度: %.1f, 已验证: %d次)",
					s.SourceDomain, s.CredibilityScore, s.VerifiedCount))
			}
			execCtx.UserInput += "\n\n[高可信度信源] 优先参考以下信源：\n" + strings.Join(sourceHints, "\n")
		}
		// 注入活跃知识
		if len(org.ActiveKnowledge) > 0 {
			var knowledgeHints []string
			for _, k := range org.ActiveKnowledge {
				knowledgeHints = append(knowledgeHints, fmt.Sprintf("- %s: %s", k.Title, k.Content))
			}
			execCtx.UserInput += "\n\n[组织知识] 以下为编辑部已确认的知识：\n" + strings.Join(knowledgeHints, "\n")
		}
		// 注入栏目偏好
		if org.ColumnPref != nil && org.ColumnPref.Tone != "" {
			execCtx.UserInput += "\n\n[栏目偏好] 语气风格：" + org.ColumnPref.Tone
		}
		slog.Info("research agent: org knowledge injected",
			"task_id", task.ID, "knowledge_count", len(org.ActiveKnowledge), "sources_count", len(org.TopSources))
	}

	emitter := &noopEmitter{}

	// 1. Query Plan
	queryPlanStep := steps.NewQueryPlanStep(e.llm)
	if err := queryPlanStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("query plan: %w", err)
	}

	// 2. Search
	searchStep := steps.NewSearchStep(e.llm, e.search)
	if err := searchStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}

	// 3. Relevance
	relevanceStep := steps.NewRelevanceStepWithEmbedding(e.embedding)
	if err := relevanceStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("relevance: %w", err)
	}

	// 4. Compress
	compressStep := steps.NewCompressStep(e.llm)
	if err := compressStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("compress: %w", err)
	}

	// 5. 产出交付物
	tokenUsed := execCtx.TotalTokens

	// 事实声明表 — 从搜索结果中提取关键事实声明（先构建，brief 中需引用）
	factClaims := make([]map[string]interface{}, 0)
	for _, sr := range execCtx.SearchResults {
		if sr.Relevance == "strong" || sr.Relevance == "medium" {
			factClaims = append(factClaims, map[string]interface{}{
				"claim":      sr.Snippet,
				"source_url": sr.URL,
				"source":     sr.Source,
				"relevance":  sr.Relevance,
				"verified":   false, // 待审校 Agent 验证
			})
		}
	}

	// 研究简报 — 存为结构化 JSON 供动态路由评估
	researchBriefText := execCtx.CompressedContext
	if researchBriefText == "" {
		researchBriefText = "无可用研究简报"
	}

	// 构建 brief JSON: { summary, sources, claims, gaps }
	briefSources := make([]map[string]interface{}, 0, len(execCtx.SearchResults))
	for _, sr := range execCtx.SearchResults {
		briefSources = append(briefSources, map[string]interface{}{
			"url":       sr.URL,
			"source":    sr.Source,
			"relevance": sr.Relevance,
		})
	}
	briefData, _ := json.Marshal(map[string]interface{}{
		"summary": researchBriefText,
		"sources": briefSources,
		"claims":  factClaims,
		"gaps":    []string{}, // 研究 Agent 当前不产 gaps
	})
	researchBriefContent := string(briefData)
	briefArtifact, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactResearchBrief,
		Content:    researchBriefContent,
		ProducedBy: "research_agent",
		TokenCost:  tokenUsed,
	}, task.ID)
	if err != nil {
		return nil, fmt.Errorf("create research brief: %w", err)
	}

	// 信源包 — 包含分级和可信度
	type sourceEntry struct {
		Title     string `json:"title"`
		Snippet   string `json:"snippet"`
		URL       string `json:"url,omitempty"`
		Source    string `json:"source"`
		Relevance string `json:"relevance,omitempty"`
		Score     float64 `json:"score,omitempty"`
	}
	sources := make([]sourceEntry, 0, len(execCtx.SearchResults))
	for _, sr := range execCtx.SearchResults {
		sources = append(sources, sourceEntry{
			Title: sr.Title, Snippet: sr.Snippet, URL: sr.URL,
			Source: sr.Source, Relevance: sr.Relevance, Score: sr.Score,
		})
	}
	sourcePackData, _ := json.Marshal(map[string]interface{}{
		"sources": sources,
		"count":   len(sources),
	})
	sourcePackArtifact, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactSourcePack,
		Content:    string(sourcePackData),
		ProducedBy: "research_agent",
		TokenCost:  0,
	}, task.ID)
	if err != nil {
		slog.Warn("failed to create source pack", "task_id", task.ID, "error", err)
	}

	// 事实声明表 Artifact
	if len(factClaims) > 0 {
		factClaimsData, _ := json.Marshal(map[string]interface{}{
			"claims": factClaims,
			"count":  len(factClaims),
		})
		if _, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
			Type:       ArtifactFactClaims,
			Content:    string(factClaimsData),
			ProducedBy: "research_agent",
			TokenCost:  0,
		}, task.ID); err != nil {
			slog.Warn("failed to create fact claims", "task_id", task.ID, "error", err)
		}
	}

	// 自动批准所有研究交付物（研究 Agent 的产出默认可信，由审校 Agent 验证）
	autoApprove := func(artID string, note string) {
		if artID == "" {
			return
		}
		if _, err := e.store.ReviewArtifact(ctx, artID, ReviewArtifactInput{
			Status:     ArtifactStatusApproved,
			ReviewerID: "research_agent",
			ReviewNote: note,
		}); err != nil {
			slog.Warn("failed to auto-approve artifact", "artifact_id", artID, "error", err)
		}
	}
	autoApprove(briefArtifact.ID, "研究简报自动批准，待审校验证")
	if sourcePackArtifact != nil {
		autoApprove(sourcePackArtifact.ID, "信源包自动批准，待审校验证")
	}

	// ── 组织记忆：记录信源使用情况 ──
	for _, sr := range execCtx.SearchResults {
		domain := extractDomain(sr.URL)
		if domain == "" {
			domain = sr.Source
		}
		if domain == "" {
			continue
		}
		if err := e.store.RecordSourceUsage(ctx, RecordSourceInput{
			SourceDomain: domain,
			SourceName:   sr.Source,
			Category:     categorizeSource(sr.Source),
			TaskID:       task.ID,
			Verified:     false, // 研究 Agent 阶段未验证，待审校 Agent 确认
			Refuted:      false,
		}); err != nil {
			slog.Warn("failed to record source usage", "domain", domain, "error", err)
		}
	}

	slog.Info("research agent completed",
		"task_id", task.ID, "sources", len(execCtx.SearchResults), "fact_claims", len(factClaims), "tokens", tokenUsed)

	return briefArtifact, nil
}

// ─── 写作 Agent 执行器 ───────────────────────────────────
//
// 复用 V2 的 OutlineStep + WriteStep + StyleProfile
// 产出 Artifact: outline → draft / revised_draft

// WritingAgentExecutor 写作 Agent 执行器
type WritingAgentExecutor struct {
	llm     *tools.LLMClient
	profile *profile.StyleProfile
	search  *tools.SearchClient
	store   *Store
}

// NewWritingAgentExecutor 创建写作 Agent 执行器
func NewWritingAgentExecutor(llm *tools.LLMClient, p *profile.StyleProfile, search *tools.SearchClient, store *Store) *WritingAgentExecutor {
	return &WritingAgentExecutor{llm: llm, profile: p, search: search, store: store}
}

func (e *WritingAgentExecutor) Role() AgentRole { return RoleWriting }

func (e *WritingAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	execCtx := engine.NewExecutionContext("task_"+task.ID, task.OwnerID, task.Title)
	execCtx.StyleSlug = task.StyleSlug
	execCtx.Mode = "guided"
	execCtx.MaxTokens = task.TokenBudget - task.TokenUsed

	// ── 注入组织知识：活跃知识 + 栏目偏好 ──
	if org := ac.GetOrgKnowledge(); org != nil {
		// 注入活跃知识（审校经验、拒稿原因等）
		if len(org.ActiveKnowledge) > 0 {
			var knowledgeHints []string
			for _, k := range org.ActiveKnowledge {
				knowledgeHints = append(knowledgeHints, fmt.Sprintf("- %s: %s", k.Title, k.Content))
			}
			execCtx.UserInput += "\n\n[组织知识] 以下为编辑部已确认的知识：\n" + strings.Join(knowledgeHints, "\n")
		}
		// 注入栏目偏好
		if org.ColumnPref != nil {
			cp := org.ColumnPref
			if cp.Tone != "" {
				execCtx.UserInput += "\n\n[栏目偏好] 语气风格：" + cp.Tone
			}
			if len(cp.ForbiddenWords) > 0 {
				execCtx.UserInput += "\n[栏目偏好] 禁用词：" + strings.Join(cp.ForbiddenWords, ", ")
			}
			if cp.PreferredLengthMin > 0 || cp.PreferredLengthMax > 0 {
				execCtx.UserInput += fmt.Sprintf("\n[栏目偏好] 建议字数：%d-%d", cp.PreferredLengthMin, cp.PreferredLengthMax)
			}
			if cp.ReviewCriteria != "" {
				execCtx.UserInput += "\n[栏目偏好] 审校标准：" + cp.ReviewCriteria
			}
			slog.Info("writing agent: loaded column preference from org knowledge",
				"column_tag", cp.ColumnTag, "tone", cp.Tone)
		}
	} else {
		// Fallback: 直接从 Store 查询栏目偏好（兼容旧路径）
		if len(task.Tags) > 0 {
			columnTag := task.Tags[0]
			if cp, err := e.store.GetColumnPreference(ctx, columnTag); err == nil && cp != nil {
				if cp.Tone != "" {
					execCtx.UserInput += "\n\n[栏目偏好] 语气风格：" + cp.Tone
				}
				if len(cp.ForbiddenWords) > 0 {
					execCtx.UserInput += "\n[栏目偏好] 禁用词：" + strings.Join(cp.ForbiddenWords, ", ")
				}
				if cp.PreferredLengthMin > 0 || cp.PreferredLengthMax > 0 {
					execCtx.UserInput += fmt.Sprintf("\n[栏目偏好] 建议字数：%d-%d", cp.PreferredLengthMin, cp.PreferredLengthMax)
				}
				if cp.ReviewCriteria != "" {
					execCtx.UserInput += "\n[栏目偏好] 审校标准：" + cp.ReviewCriteria
				}
				slog.Info("writing agent: loaded column preference (fallback)", "column_tag", columnTag, "tone", cp.Tone)
			}
		}
	}

	// 注入研究简报
	if brief := ac.GetArtifact(ArtifactResearchBrief); brief != nil {
		execCtx.CompressedContext = brief.Content
	}

	// 注入审查报告（修改场景）
	hasReviewReport := false
	if report := ac.GetArtifact(ArtifactReviewReport); report != nil {
		hasReviewReport = true
		// 将审查报告作为用户材料注入
		execCtx.UserMaterials = []string{report.Content}
	}

	// 修改场景：注入上一版稿件作为基础文本，避免"重新写一篇"
	if hasReviewReport {
		// 优先取修改稿，没有则取初稿
		prevDraft := ac.GetArtifact(ArtifactRevisedDraft)
		if prevDraft == nil {
			prevDraft = ac.GetArtifact(ArtifactDraft)
		}
		if prevDraft != nil {
			// 将上一版稿件内容解析并注入到执行上下文
			var prevData struct {
				Title   string `json:"title"`
				Content string `json:"content"`
				Body    string `json:"body"` // 兼容旧格式
			}
			if err := json.Unmarshal([]byte(prevDraft.Content), &prevData); err == nil {
				prevBody := prevData.Content
				if prevBody == "" {
					prevBody = prevData.Body
				}
				if prevBody != "" {
					// 将上一版稿件作为已有文章注入，让 WriteStep 基于它修改
					execCtx.Article = prevBody
					execCtx.ArticleTitle = prevData.Title
				}
			}
		}
	}

	emitter := &noopEmitter{}

	// 1. Outline（仅首次写作，修改时跳过）
	if !hasReviewReport {
		outlineStep := steps.NewOutlineStep(e.llm)
		// 自动确认提纲（编辑部模式下，提纲自动通过，由审校 Agent 把关）
		execCtx.ConfirmTimeout = 1 * time.Second
		if err := outlineStep.Execute(ctx, execCtx, emitter); err != nil {
			slog.Warn("outline step failed, continuing", "error", err)
		}

		// 存储提纲 Artifact
		if execCtx.Outline != nil {
			outlineData, _ := json.Marshal(execCtx.Outline)
			e.store.CreateArtifact(ctx, SubmitArtifactInput{
				Type:       ArtifactOutline,
				Content:    string(outlineData),
				ProducedBy: "writing_agent",
				TokenCost:  0,
			}, task.ID)
		}
	}

	// 2. Write
	writeStep := steps.NewWriteStepWithSearch(e.llm, e.profile, e.search)
	if err := writeStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}

	// 3. 产出交付物
	tokenUsed := execCtx.TotalTokens

	// 判断是初稿还是修改稿
	artType := ArtifactDraft
	if hasReviewReport {
		artType = ArtifactRevisedDraft
	}

	// 将文章内容包装为 JSON（字段名与 routeAfterWriting 解析保持一致）
	// 统计正文字数（中文按 rune 计数）
	wordCount := len([]rune(execCtx.Article))
	outlineSections := make([]map[string]interface{}, 0)
	if execCtx.Outline != nil {
		for _, s := range execCtx.Outline.Outline {
			outlineSections = append(outlineSections, map[string]interface{}{
				"section": s.Point,
			})
		}
	}
	articleData, _ := json.Marshal(map[string]interface{}{
		"title":      execCtx.ArticleTitle,
		"content":    execCtx.Article,
		"word_count": wordCount,
		"outline":    outlineSections,
	})

	art, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       artType,
		Content:    string(articleData),
		ProducedBy: "writing_agent",
		TokenCost:  tokenUsed,
	}, task.ID)
	if err != nil {
		return nil, fmt.Errorf("create draft artifact: %w", err)
	}

	// 自动批准写作交付物
	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "writing_agent",
		ReviewNote: "初稿自动提交审校",
	})

	slog.Info("writing agent completed",
		"task_id", task.ID, "type", artType, "length", len(execCtx.Article), "tokens", tokenUsed)

	return art, nil
}

// ─── 审校 Agent 执行器 ───────────────────────────────────
//
// 复用 V2 的 PostReviewStep + 事实核查(jiaozhen)
// 产出 Artifact: review_report
// 上下文隔离：只看 Artifact，不看写作过程

// ReviewAgentExecutor 审校 Agent 执行器
type ReviewAgentExecutor struct {
	llm     *tools.LLMClient
	profile *profile.StyleProfile
	search  *tools.SearchClient
	store   *Store
}

// NewReviewAgentExecutor 创建审校 Agent 执行器
func NewReviewAgentExecutor(llm *tools.LLMClient, p *profile.StyleProfile, search *tools.SearchClient, store *Store) *ReviewAgentExecutor {
	return &ReviewAgentExecutor{llm: llm, profile: p, search: search, store: store}
}

func (e *ReviewAgentExecutor) Role() AgentRole { return RoleReview }

func (e *ReviewAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	// 上下文隔离：审校 Agent 只看交付物，不看写作过程
	// 优先审查修改稿（如果存在），否则审查初稿
	draft := ac.GetArtifact(ArtifactRevisedDraft)
	if draft == nil {
		draft = ac.GetArtifact(ArtifactDraft)
	}
	if draft == nil {
		return nil, fmt.Errorf("no draft artifact to review")
	}

	// 解析文章内容（与写作 Agent 产出的 JSON 字段对齐）
	var articleData struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Body    string `json:"body"` // 兼容旧格式
	}
	if err := json.Unmarshal([]byte(draft.Content), &articleData); err != nil {
		return nil, fmt.Errorf("parse draft content: %w", err)
	}
	// 优先使用 content，旧格式回退到 body
	articleBody := articleData.Content
	if articleBody == "" {
		articleBody = articleData.Body
	}

	// 构建独立 ExecutionContext（隔离上下文）
	execCtx := engine.NewExecutionContext("review_"+task.ID, task.OwnerID, articleBody)
	execCtx.StyleSlug = task.StyleSlug
	execCtx.Article = articleBody
	execCtx.ArticleTitle = articleData.Title
	execCtx.MaxTokens = task.TokenBudget - task.TokenUsed

	// ── 注入组织知识：活跃知识 + 栏目偏好 + 信源可信度 ──
	if org := ac.GetOrgKnowledge(); org != nil {
		// 注入活跃知识（历史审校经验、拒稿原因等）
		if len(org.ActiveKnowledge) > 0 {
			var knowledgeHints []string
			for _, k := range org.ActiveKnowledge {
				knowledgeHints = append(knowledgeHints, fmt.Sprintf("- %s: %s", k.Title, k.Content))
			}
			execCtx.UserInput += "\n\n[组织知识] 以下为编辑部已确认的审校经验：\n" + strings.Join(knowledgeHints, "\n")
		}
		// 注入栏目偏好（审校标准）
		if org.ColumnPref != nil {
			cp := org.ColumnPref
			if cp.ReviewCriteria != "" {
				execCtx.UserInput += "\n\n[栏目审校标准] " + cp.ReviewCriteria
			}
			if len(cp.ForbiddenWords) > 0 {
				execCtx.UserInput += "\n[栏目禁用词] " + strings.Join(cp.ForbiddenWords, ", ")
			}
			if cp.PreferredLengthMin > 0 || cp.PreferredLengthMax > 0 {
				execCtx.UserInput += fmt.Sprintf("\n[栏目建议字数] %d-%d", cp.PreferredLengthMin, cp.PreferredLengthMax)
			}
		}
		slog.Info("review agent: org knowledge injected",
			"task_id", task.ID, "knowledge_count", len(org.ActiveKnowledge))
	}

	// 注入信源包和事实声明（用于事实核查）
	if sourcePack := ac.GetArtifact(ArtifactSourcePack); sourcePack != nil {
		// 信源包格式为 {"sources": [...], "count": N}
		var sourceData struct {
			Sources []engine.SearchResult `json:"sources"`
		}
		if err := json.Unmarshal([]byte(sourcePack.Content), &sourceData); err == nil {
			execCtx.SearchResults = sourceData.Sources
		}
	}

	emitter := &noopEmitter{}

	// 执行审查 Step
	// 注意：这里只复用 PostReviewStep 的审查逻辑，不复用 AutoFixStep
	// 审校 Agent 只负责发现问题并报告，不负责修改
	reviewStep := steps.NewPostReviewStep(e.llm)
	if err := reviewStep.Execute(ctx, execCtx, emitter); err != nil {
		return nil, fmt.Errorf("post review: %w", err)
	}

	// 产出审查报告
	tokenUsed := execCtx.TotalTokens
	reviewResult := execCtx.ReviewResult

	reportData, _ := json.Marshal(map[string]interface{}{
		"passed":   reviewResult.Passed,
		"severity": severityFromReview(reviewResult),
		"issues":   reviewResult.Issues,
		"scores":   reviewResult.Scores,
	})

	art, err := e.store.CreateArtifact(ctx, SubmitArtifactInput{
		Type:       ArtifactReviewReport,
		Content:    string(reportData),
		ProducedBy: "review_agent",
		TokenCost:  tokenUsed,
	}, task.ID)
	if err != nil {
		return nil, fmt.Errorf("create review report: %w", err)
	}

	// 自动批准审查报告
	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "review_agent",
		ReviewNote: "审查报告已生成",
	})

	// ── 组织记忆：自动沉淀审校知识 ──
	if len(reviewResult.Issues) > 0 {
		columnTag := ""
		if len(task.Tags) > 0 {
			columnTag = task.Tags[0]
		}
		for _, issue := range reviewResult.Issues {
			category := "review_tip"
			if issue.Severity == "high" {
				category = "rejection_reason"
			}
			if _, err := e.store.CreateKnowledge(ctx, EditorialKnowledge{
				Category:         category,
				ColumnTag:        columnTag,
				Title:            fmt.Sprintf("[%s] %s", issue.Type, issue.Severity),
				Content:          issue.Message,
				SourceTaskID:     task.ID,
				SourceArtifactID: art.ID,
				Confidence:       0.7, // 审校 Agent 产出的知识默认中等置信度
			}); err != nil {
				slog.Warn("failed to create knowledge from review issue", "error", err)
			}
		}
		slog.Info("review agent: knowledge deposited", "task_id", task.ID, "issues", len(reviewResult.Issues))
	}

	// ── 组织记忆：更新信源可信度（基于审校结果） ──
	if sourcePack := ac.GetArtifact(ArtifactSourcePack); sourcePack != nil {
		var sourceData struct {
			Sources []engine.SearchResult `json:"sources"`
		}
		if err := json.Unmarshal([]byte(sourcePack.Content), &sourceData); err == nil {
			for _, sr := range sourceData.Sources {
				domain := extractDomain(sr.URL)
				if domain == "" {
					domain = sr.Source
				}
				if domain == "" {
					continue
				}
				// 审校通过 → 信源验证为真；不通过 → 未验证（不直接证伪，除非有明确证据）
				if err := e.store.RecordSourceUsage(ctx, RecordSourceInput{
					SourceDomain: domain,
					SourceName:   sr.Source,
					Category:     categorizeSource(sr.Source),
					TaskID:       task.ID,
					Verified:     reviewResult.Passed,
					Refuted:      false,
				}); err != nil {
					slog.Warn("failed to update source credibility", "domain", domain, "error", err)
				}
			}
		}
	}

	slog.Info("review agent completed",
		"task_id", task.ID, "passed", reviewResult.Passed, "issues", len(reviewResult.Issues), "tokens", tokenUsed)

	return art, nil
}

// severityFromReview 从审查结果推断严重程度
func severityFromReview(rr *engine.ReviewResult) string {
	if rr == nil {
		return "low"
	}
	for _, issue := range rr.Issues {
		if issue.Severity == "high" {
			return "high"
		}
	}
	if len(rr.Issues) > 3 {
		return "medium"
	}
	return "low"
}

// extractDomain 从 URL 中提取域名
func extractDomain(rawURL string) string {
	if rawURL == "" {
		return ""
	}
	u := strings.TrimSpace(rawURL)
	u = strings.TrimPrefix(u, "https://")
	u = strings.TrimPrefix(u, "http://")
	u = strings.TrimPrefix(u, "www.")
	if idx := strings.Index(u, "/"); idx > 0 {
		u = u[:idx]
	}
	return strings.ToLower(u)
}

// categorizeSource 根据来源名称推断类别
func categorizeSource(sourceName string) string {
	name := strings.ToLower(sourceName)
	switch {
	case strings.Contains(name, "gov") || strings.Contains(name, "政府") || strings.Contains(name, "官方"):
		return "gov"
	case strings.Contains(name, "edu") || strings.Contains(name, "学术") || strings.Contains(name, "大学"):
		return "academic"
	case strings.Contains(name, "微博") || strings.Contains(name, "微信") || strings.Contains(name, "social"):
		return "social"
	case strings.Contains(name, "blog") || strings.Contains(name, "博客"):
		return "blog"
	default:
		return "news"
	}
}

// ─── Noop Emitter（用于 Agent 执行器内部复用 Steps）────────

type noopEmitter struct{}

func (n *noopEmitter) StepStart(step engine.StepName, stepIndex int)                               {}
func (n *noopEmitter) StepComplete(step engine.StepName, result interface{}, durationMs int64)     {}
func (n *noopEmitter) StreamDelta(delta string)                                                     {}
func (n *noopEmitter) StreamReset()                                                                 {}
func (n *noopEmitter) ReasoningDelta(delta string)                                                  {}
func (n *noopEmitter) ArticleTitle(title string)                                                    {}
func (n *noopEmitter) StreamDone(fullText string)                                                   {}
func (n *noopEmitter) AwaitInput(step engine.StepName, data interface{}, options []string, attempt int, maxAttempts int) {}
func (n *noopEmitter) Paused(step engine.StepName, savedState interface{})                           {}
func (n *noopEmitter) PausedWithReason(step engine.StepName, savedState interface{}, reason string) {}
func (n *noopEmitter) Resumed(step engine.StepName)                                                  {}
func (n *noopEmitter) Error(code, message string, step engine.StepName)                              {}
func (n *noopEmitter) Completed(article string, articleTitle string, review interface{}, tokenUsage interface{}) {}
func (n *noopEmitter) Cancelled()                                                                    {}
func (n *noopEmitter) Compaction(originalMessages, savedTokens int, summaryPreview string)            {}
