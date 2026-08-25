package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── 研究 Agent 执行器 ───────────────────────────────────
//
// 使用 RoleAgentRunner 让 LLM 自主调用 search_web / search_knowledge / read_source
// 产出 Artifact: research_brief + source_pack + fact_claims

// ResearchAgentExecutor 研究 Agent 执行器
type ResearchAgentExecutor struct {
	llmResolver  LLMResolver
	search        *tools.SearchClient
	embedding     *tools.EmbeddingClient
	kbSearcher    tools.KnowledgeSearcher
	store          *Store
	toolRegistry  *EditorialToolRegistry
	*emitterHolder
}

// NewResearchAgentExecutor 创建研究 Agent 执行器
func NewResearchAgentExecutor(llmResolver LLMResolver, search *tools.SearchClient, embedding *tools.EmbeddingClient, store *Store, kbSearcher tools.KnowledgeSearcher, toolRegistry *EditorialToolRegistry) *ResearchAgentExecutor {
	return &ResearchAgentExecutor{
		llmResolver: llmResolver, search: search, embedding: embedding, store: store, kbSearcher: kbSearcher,
		toolRegistry:  toolRegistry,
		emitterHolder: newEmitterHolder(),
	}
}

func (e *ResearchAgentExecutor) Role() AgentRole { return RoleResearch }

func (e *ResearchAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
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

	// ── 获取角色配置（优先使用 DAG Planner 生成的，否则用 BuiltinRoles）──
	agentCfg := getAgentConfig(ac, "researcher")

	// ── 启动 RoleAgentRunner ──
	llmClient := e.llmResolver.GetClient(ctx, "")
	runner := NewRoleAgentRunner(llmClient, e.search, e.kbSearcher, nil, e.currentOrNoop(), e.toolRegistry)
	result, err := runner.Run(ctx, RoleRunConfig{
		AgentConfig:      agentCfg,
		Task:             task,
		AgentContext:     ac,
		ExecutionContext: execCtx,
	})
	if err != nil {
		return nil, fmt.Errorf("research agent runner: %w", err)
	}

	tokenUsed := result.Tokens

	// ── 从信号工具参数中提取研究简报 ──
	researchBriefText := result.Output
	var briefSources []map[string]interface{}
	factClaims := make([]map[string]interface{}, 0)

	if briefArgs, ok := result.SignalToolArgs["submit_research_brief"]; ok {
		var parsed struct {
			Summary string                   `json:"summary"`
			Sources []map[string]interface{} `json:"sources"`
			Claims  []map[string]interface{} `json:"claims"`
		}
		if err := json.Unmarshal([]byte(briefArgs), &parsed); err == nil {
			if parsed.Summary != "" {
				researchBriefText = parsed.Summary
			}
			if len(parsed.Sources) > 0 {
				briefSources = parsed.Sources
			}
			if len(parsed.Claims) > 0 {
				factClaims = parsed.Claims
			}
		}
	}

	// Fallback: 如果信号工具没被调用，从搜索结果中构建
	if len(briefSources) == 0 {
		for _, sr := range result.SearchResults {
			briefSources = append(briefSources, map[string]interface{}{
				"url":       sr.URL,
				"source":    sr.Source,
				"relevance": sr.Relevance,
			})
		}
	}
	if len(factClaims) == 0 {
		for _, sr := range result.SearchResults {
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
	}

	if researchBriefText == "" {
		researchBriefText = "无可用研究简报"
	}

	// 构建 brief JSON
	briefData, _ := json.Marshal(map[string]interface{}{
		"summary": researchBriefText,
		"sources": briefSources,
		"claims":  factClaims,
		"gaps":    []string{},
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

	// 信源包
	type sourceEntry struct {
		Title     string `json:"title"`
		Snippet   string `json:"snippet"`
		URL       string `json:"url,omitempty"`
		Source    string `json:"source"`
		Relevance string `json:"relevance,omitempty"`
		Score     float64 `json:"score,omitempty"`
	}
	sources := make([]sourceEntry, 0, len(result.SearchResults))
	for _, sr := range result.SearchResults {
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

	// 自动批准所有研究交付物
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
	for _, sr := range result.SearchResults {
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
			Verified:     false,
			Refuted:      false,
		}); err != nil {
			slog.Warn("failed to record source usage", "domain", domain, "error", err)
		}
	}

	slog.Info("research agent completed",
		"task_id", task.ID, "sources", len(result.SearchResults), "fact_claims", len(factClaims), "tokens", tokenUsed)

	return briefArtifact, nil
}

// ─── 写作 Agent 执行器 ───────────────────────────────────
//
// 使用 RoleAgentRunner 让 LLM 自主调用 search_knowledge / read_source / generate_outline / write_article
// 产出 Artifact: outline → draft / revised_draft

// WritingAgentExecutor 写作 Agent 执行器
type WritingAgentExecutor struct {
	llmResolver    LLMResolver
	profileLoader  *profile.Loader
	userStyleStore *database.UserStyleStore
	search         *tools.SearchClient
	kbSearcher     tools.KnowledgeSearcher
	store          *Store
	toolRegistry   *EditorialToolRegistry
	*emitterHolder
}

// NewWritingAgentExecutor 创建写作 Agent 执行器（动态加载 Profile）
func NewWritingAgentExecutor(llmResolver LLMResolver, loader *profile.Loader, userStyleStore *database.UserStyleStore, search *tools.SearchClient, store *Store, kbSearcher tools.KnowledgeSearcher, toolRegistry *EditorialToolRegistry) *WritingAgentExecutor {
	return &WritingAgentExecutor{llmResolver: llmResolver, profileLoader: loader, userStyleStore: userStyleStore, search: search, store: store, kbSearcher: kbSearcher, toolRegistry: toolRegistry, emitterHolder: newEmitterHolder()}
}

func (e *WritingAgentExecutor) Role() AgentRole { return RoleWriting }

func (e *WritingAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	execCtx := engine.NewExecutionContext("task_"+task.ID, task.OwnerID, task.Title)
	execCtx.StyleSlug = task.StyleSlug
	execCtx.Mode = "guided"
	execCtx.MaxTokens = task.TokenBudget - task.TokenUsed

	// ── 构建角色特定的 system prompt 额外内容 ──
	var promptExtra strings.Builder

	// 注入研究简报
	if brief := ac.GetArtifact(ArtifactResearchBrief); brief != nil {
		promptExtra.WriteString("--- 研究简报 ---\n")
		promptExtra.WriteString(brief.Content)
		promptExtra.WriteString("\n\n")
	}

	// 注入审查报告（修改场景）
	hasReviewReport := false
	if report := ac.GetArtifact(ArtifactReviewReport); report != nil {
		hasReviewReport = true
		promptExtra.WriteString("--- 审查报告 ---\n")
		promptExtra.WriteString(report.Content)
		promptExtra.WriteString("\n\n")
	}

	// 修改场景：注入上一版稿件
	if hasReviewReport {
		prevDraft := ac.GetArtifact(ArtifactRevisedDraft)
		if prevDraft == nil {
			prevDraft = ac.GetArtifact(ArtifactDraft)
		}
		if prevDraft != nil {
			var prevData struct {
				Title   string `json:"title"`
				Content string `json:"content"`
				Body    string `json:"body"`
			}
			if err := json.Unmarshal([]byte(prevDraft.Content), &prevData); err == nil {
				prevBody := prevData.Content
				if prevBody == "" {
					prevBody = prevData.Body
				}
				if prevBody != "" {
					execCtx.Article = prevBody
					execCtx.ArticleTitle = prevData.Title
					promptExtra.WriteString("--- 上一版稿件 ---\n")
					promptExtra.WriteString(fmt.Sprintf("标题：%s\n", prevData.Title))
					promptExtra.WriteString(prevBody)
					promptExtra.WriteString("\n\n请基于审查报告修改以上稿件。\n")
				}
			}
		}
	} else {
		promptExtra.WriteString("请根据选题和研究简报，撰写一篇完整文章。\n")
	}

	// ── Fallback: 直接从 Store 查询栏目偏好 ──
	if ac.GetOrgKnowledge() == nil && len(task.Tags) > 0 {
		columnTag := task.Tags[0]
		if cp, err := e.store.GetColumnPreference(ctx, columnTag); err == nil && cp != nil {
			ac.LocalMemory = &OrgKnowledge{ColumnPref: cp}
		}
	}

	// ── 获取角色配置 ──
	agentCfg := getAgentConfig(ac, "writer")

	// 动态加载写作风格 Profile（支持用户自定义风格 my_ 前缀）
	styleProfile := e.resolveStyleProfile(ctx, task.StyleSlug, task.OwnerID)

	// ── 启动 RoleAgentRunner ──
	llmClient := e.llmResolver.GetClient(ctx, "")
	runner := NewRoleAgentRunner(llmClient, e.search, e.kbSearcher, styleProfile, e.currentOrNoop(), e.toolRegistry)
	result, err := runner.Run(ctx, RoleRunConfig{
		AgentConfig:          agentCfg,
		Task:                 task,
		AgentContext:         ac,
		ExecutionContext:     execCtx,
		RoleSystemPromptExtra: promptExtra.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("writing agent runner: %w", err)
	}

	tokenUsed := result.Tokens

	// 从 LLM 输出中提取文章内容
	articleBody := result.Output
	articleTitle := steps.ExtractTitleFromMarkdown(articleBody)
	if articleTitle == "" {
		articleTitle = execCtx.ArticleTitle
	}

	// 存储提纲 Artifact（如果有）
	if execCtx.Outline != nil {
		outlineData, _ := json.Marshal(execCtx.Outline)
		e.store.CreateArtifact(ctx, SubmitArtifactInput{
			Type:       ArtifactOutline,
			Content:    string(outlineData),
			ProducedBy: "writing_agent",
			TokenCost:  0,
		}, task.ID)
	}

	// 判断是初稿还是修改稿
	artType := ArtifactDraft
	if hasReviewReport {
		artType = ArtifactRevisedDraft
	}

	wordCount := len([]rune(articleBody))
	outlineSections := make([]map[string]interface{}, 0)
	if execCtx.Outline != nil {
		for _, s := range execCtx.Outline.Outline {
			outlineSections = append(outlineSections, map[string]interface{}{
				"section": s.Point,
			})
		}
	}
	articleData, _ := json.Marshal(map[string]interface{}{
		"title":      articleTitle,
		"content":    articleBody,
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

	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "writing_agent",
		ReviewNote: "初稿自动提交审校",
	})

	slog.Info("writing agent completed",
		"task_id", task.ID, "type", artType, "length", wordCount, "tokens", tokenUsed)

	return art, nil
}

// ─── 审校 Agent 执行器 ───────────────────────────────────
//
// 使用 RoleAgentRunner 让 LLM 自主调用 fact_check / search_knowledge / read_source
// 产出 Artifact: review_report
// 上下文隔离：只看 Artifact，不看写作过程

// ReviewAgentExecutor 审校 Agent 执行器
type ReviewAgentExecutor struct {
	llmResolver    LLMResolver
	profileLoader  *profile.Loader
	userStyleStore *database.UserStyleStore
	search         *tools.SearchClient
	kbSearcher     tools.KnowledgeSearcher
	store          *Store
	toolRegistry   *EditorialToolRegistry
	*emitterHolder
}

// NewReviewAgentExecutor 创建审校 Agent 执行器（动态加载 Profile）
func NewReviewAgentExecutor(llmResolver LLMResolver, loader *profile.Loader, userStyleStore *database.UserStyleStore, search *tools.SearchClient, store *Store, kbSearcher tools.KnowledgeSearcher, toolRegistry *EditorialToolRegistry) *ReviewAgentExecutor {
	return &ReviewAgentExecutor{llmResolver: llmResolver, profileLoader: loader, userStyleStore: userStyleStore, search: search, store: store, kbSearcher: kbSearcher, toolRegistry: toolRegistry, emitterHolder: newEmitterHolder()}
}

func (e *ReviewAgentExecutor) Role() AgentRole { return RoleReview }

func (e *ReviewAgentExecutor) Execute(ctx context.Context, ac *AgentContext, task *Task) (*Artifact, error) {
	// 上下文隔离：审校 Agent 只看交付物，不看写作过程
	draft := ac.GetArtifact(ArtifactRevisedDraft)
	if draft == nil {
		draft = ac.GetArtifact(ArtifactDraft)
	}
	if draft == nil {
		return nil, fmt.Errorf("no draft artifact to review")
	}

	var articleData struct {
		Title   string `json:"title"`
		Content string `json:"content"`
		Body    string `json:"body"`
	}
	if err := json.Unmarshal([]byte(draft.Content), &articleData); err != nil {
		return nil, fmt.Errorf("parse draft content: %w", err)
	}
	articleBody := articleData.Content
	if articleBody == "" {
		articleBody = articleData.Body
	}

	execCtx := engine.NewExecutionContext("review_"+task.ID, task.OwnerID, articleBody)
	execCtx.StyleSlug = task.StyleSlug
	execCtx.Article = articleBody
	execCtx.ArticleTitle = articleData.Title
	execCtx.MaxTokens = task.TokenBudget - task.TokenUsed

	// ── 构建角色特定的 system prompt 额外内容 ──
	var promptExtra strings.Builder
	promptExtra.WriteString("--- 待审文章 ---\n")
	promptExtra.WriteString(fmt.Sprintf("标题：%s\n", articleData.Title))
	promptExtra.WriteString(articleBody)
	promptExtra.WriteString("\n\n")

	// 注入信源包
	if sourcePack := ac.GetArtifact(ArtifactSourcePack); sourcePack != nil {
		promptExtra.WriteString("--- 信源包 ---\n")
		promptExtra.WriteString(sourcePack.Content)
		promptExtra.WriteString("\n\n")
	}

	// 注入事实声明
	if factClaims := ac.GetArtifact(ArtifactFactClaims); factClaims != nil {
		promptExtra.WriteString("--- 事实声明 ---\n")
		promptExtra.WriteString(factClaims.Content)
		promptExtra.WriteString("\n\n")
	}

	// ── 获取角色配置 ──
	agentCfg := getAgentConfig(ac, "reviewer")

	// 动态加载写作风格 Profile（支持用户自定义风格 my_ 前缀）
	styleProfile := e.resolveStyleProfile(ctx, task.StyleSlug, task.OwnerID)

	// ── 启动 RoleAgentRunner ──
	llmClient := e.llmResolver.GetClient(ctx, "")
	runner := NewRoleAgentRunner(llmClient, e.search, e.kbSearcher, styleProfile, e.currentOrNoop(), e.toolRegistry)
	result, err := runner.Run(ctx, RoleRunConfig{
		AgentConfig:          agentCfg,
		Task:                 task,
		AgentContext:         ac,
		ExecutionContext:     execCtx,
		RoleSystemPromptExtra: promptExtra.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("review agent runner: %w", err)
	}

	tokenUsed := result.Tokens

	// ── 从信号工具参数中提取审查报告 ──
	passed := false
	severity := "low"
	var issues []engine.ReviewIssue
	scores := map[string]float64{}

	if reportArgs, ok := result.SignalToolArgs["submit_review_report"]; ok {
		var parsed struct {
			Passed   bool                   `json:"passed"`
			Severity string                 `json:"severity"`
			Issues   []engine.ReviewIssue   `json:"issues"`
			Scores   map[string]float64     `json:"scores"`
		}
		if err := json.Unmarshal([]byte(reportArgs), &parsed); err == nil {
			passed = parsed.Passed
			if parsed.Severity != "" {
				severity = parsed.Severity
			}
			if len(parsed.Issues) > 0 {
				issues = parsed.Issues
			}
			if len(parsed.Scores) > 0 {
				scores = parsed.Scores
			}
		}
	}

	reportData, _ := json.Marshal(map[string]interface{}{
		"passed":   passed,
		"severity": severity,
		"issues":   issues,
		"scores":   scores,
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

	e.store.ReviewArtifact(ctx, art.ID, ReviewArtifactInput{
		Status:     ArtifactStatusApproved,
		ReviewerID: "review_agent",
		ReviewNote: "审查报告已生成",
	})

	// ── 组织记忆：自动沉淀审校知识 ──
	if len(issues) > 0 {
		columnTag := ""
		if len(task.Tags) > 0 {
			columnTag = task.Tags[0]
		}
		for _, issue := range issues {
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
				Confidence:       0.7,
			}); err != nil {
				slog.Warn("failed to create knowledge from review issue", "error", err)
			}
		}
		slog.Info("review agent: knowledge deposited", "task_id", task.ID, "issues", len(issues))
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
				if err := e.store.RecordSourceUsage(ctx, RecordSourceInput{
					SourceDomain: domain,
					SourceName:   sr.Source,
					Category:     categorizeSource(sr.Source),
					TaskID:       task.ID,
					Verified:     passed,
					Refuted:      false,
				}); err != nil {
					slog.Warn("failed to update source credibility", "domain", domain, "error", err)
				}
			}
		}
	}

	slog.Info("review agent completed",
		"task_id", task.ID, "passed", passed, "issues", len(issues), "tokens", tokenUsed)

	return art, nil
}

// resolveStyleProfile 加载写作风格 Profile，支持内置风格和用户自定义风格（my_ 前缀）。
// 对于 my_ 前缀的 slug，从 user_style_profiles 表加载用户自定义风格配置。
// 对于普通 slug，从全局 profile.Loader 加载内置/DB 全局风格。
// 如果找不到，返回 nil（RoleAgentRunner 会跳过风格注入）。
func resolveStyleProfile(loader *profile.Loader, userStyleStore *database.UserStyleStore, slug, ownerID string) *profile.StyleProfile {
	if slug == "" {
		return nil
	}

	// 用户自定义风格（my_ 前缀）
	if strings.HasPrefix(slug, "my_") && userStyleStore != nil && ownerID != "" && ownerID != "anonymous" {
		rawSlug := strings.TrimPrefix(slug, "my_")
		userProfile, err := userStyleStore.GetProfileBySlugAndOwner(context.Background(), rawSlug, ownerID)
		if err != nil {
			slog.Warn("failed to load user custom style for editorial, falling back to global",
				"slug", slug, "error", err, "user_id", ownerID)
		} else if userProfile.CurrentVersion > 0 {
			version, err := userStyleStore.GetLatestVersion(context.Background(), userProfile.ID)
			if err != nil {
				slog.Warn("failed to load user style version for editorial, falling back to global",
					"slug", slug, "error", err, "user_id", ownerID)
			} else {
				var sp profile.StyleProfile
				if err := json.Unmarshal([]byte(version.Config), &sp); err != nil {
					slog.Warn("failed to unmarshal user style config for editorial, falling back to global",
						"slug", slug, "error", err, "user_id", ownerID)
				} else {
					slog.Info("loaded user custom style for editorial",
						"slug", slug, "name", sp.Name, "version", sp.Version, "user_id", ownerID)
					return &sp
				}
			}
		}
		// 加载失败时继续 fallback 到全局 loader
	}

	// 全局风格（内置或 DB 中的全局 profile）
	if loader != nil {
		if p, ok := loader.Get(slug); ok {
			return p
		}
	}

	return nil
}

// resolveStyleProfile 是 WritingAgentExecutor 的便捷方法
func (e *WritingAgentExecutor) resolveStyleProfile(_ context.Context, slug, ownerID string) *profile.StyleProfile {
	return resolveStyleProfile(e.profileLoader, e.userStyleStore, slug, ownerID)
}

// resolveStyleProfile 是 ReviewAgentExecutor 的便捷方法
func (e *ReviewAgentExecutor) resolveStyleProfile(_ context.Context, slug, ownerID string) *profile.StyleProfile {
	return resolveStyleProfile(e.profileLoader, e.userStyleStore, slug, ownerID)
}

// getAgentConfig 获取角色配置：优先使用 DAGExecutor 注入的 AgentConfig，
// 否则使用 BuiltinRoles 中的预设角色。
func getAgentConfig(ac *AgentContext, defaultRole string) *AgentConfig {
	// DAG 模式：DAGExecutor 在 executeNode 中直接注入 AgentConfig 到 AgentContext
	if ac != nil && ac.AgentConfig != nil {
		return ac.AgentConfig
	}
	// 兼容：检查 LocalMemory 是否直接携带 AgentConfig（旧路径）
	if ac != nil && ac.LocalMemory != nil {
		if cfg, ok := ac.LocalMemory.(*AgentConfig); ok && cfg != nil {
			return cfg
		}
	}
	// Fallback: 使用 BuiltinRoles
	if role, ok := BuiltinRoles[defaultRole]; ok {
		return role.ToAgentConfig()
	}
	// 最终 fallback
	return &AgentConfig{
		Name:    defaultRole,
		Role:    defaultRole,
		Persona: "你是一个编辑部 Agent。",
	}
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
func (n *noopEmitter) Compaction(originalMessages, savedTokens int, summaryPreview string, historyVersion uint64, triggerReason string) {}

// ─── emitterHolder: goroutine 安全的 emitter 传递 mixin ──────────
//
// 问题：DAGExecutor 的 executors map 中，执行器按 BaseRole 注册并共享。
// 当两个同角色节点并行执行时，直接在共享实例上写 emitter 字段会产生数据竞态。
//
// 解决方案：emitterHolder 使用 atomic.Pointer 存储 emitter，每次 Execute 调用
// 独立设置和清除。currentOrNoop() 读取时使用 atomic load，保证并发安全。
//
// 使用方式：执行器结构体内嵌 *emitterHolder，实现 EmitterHolder 接口。
type emitterHolder struct {
	val atomic.Pointer[engine.EventEmitter]
}

func newEmitterHolder() *emitterHolder {
	return &emitterHolder{}
}

func (h *emitterHolder) SetCurrentEmitter(em engine.EventEmitter) {
	h.val.Store(&em)
}

func (h *emitterHolder) ClearCurrentEmitter() {
	h.val.Store(nil)
}

func (h *emitterHolder) currentOrNoop() engine.EventEmitter {
	ptr := h.val.Load()
	if ptr != nil && *ptr != nil {
		return *ptr
	}
	return &noopEmitter{}
}
