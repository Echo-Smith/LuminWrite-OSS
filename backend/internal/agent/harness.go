package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine/steps"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	worldstate "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/worldstate"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── Harness: 单层 LLM 执行核 ──────────────────────────────
//
// Harness 是执行核（⑥D 后）：RunCore 是唯一入口，产出 provisional value。
// 会话持久化、记忆写入、终态/UI 事件、扣费等用户侧副作用已随 Legacy
// 写作路径移除——权威提交归 governed runtime / WABench / 实验各自所有。
//
// 设计原则（继承 dsh/Pi 理念 [[memory:178679655388010121476]]）：
//   - 意图路由、工具集选择、断路器、超时、断线检测
//   - LLM 执行：在单轮中自主决定调用什么工具、何时写作、何时修正
//   - 单层：不存在外层 ReAct + 内层 agent loop 的嵌套
//
// 核心特点：
//   - 意图判定走规则（毫秒级），不走 LLM
//   - 工具粒度细（search_web, read_source, write_article, review_article, revise_section）
//   - 会话状态仅在单次 run 内保留（文章、素材）

// Harness 依赖项。
type Harness struct {
	llm        *tools.LLMClient
	search     *tools.SearchClient
	kbSearcher tools.KnowledgeSearcher
	profile    *profile.StyleProfile

	maxIterations int

	// memoryPort 记忆消费契约（P1）：retrieve_context/remember 工具后端。
	// nil = 记忆功能关闭。
	memoryPort memoryport.Port

	// WorldState 管理（借鉴 Codex ContextManager + WorldState）
	// 跨轮保留 section 基线，实现增量 diff 推送
	worldState     *worldstate.WorldState
	historyVersion uint64 // 对话历史版本号，compaction/rollback 时递增

	// Token 预算追踪（借鉴 Codex TokenBudgetContext）
	tokenBudget *worldstate.TokenBudget
	autoCompact *worldstate.AutoCompactFallback
}

// NewHarness creates the execution core.
func NewHarness(llm *tools.LLMClient, search *tools.SearchClient, kb tools.KnowledgeSearcher, p *profile.StyleProfile) *Harness {
	return &Harness{
		llm:            llm,
		search:         search,
		kbSearcher:     kb,
		profile:        p,
		maxIterations:  12,
		worldState:     worldstate.NewWorldState(),
		tokenBudget:    &worldstate.TokenBudget{ContextWindowID: ""},
		autoCompact:    worldstate.NewAutoCompactFallback(),
	}
}

// SetMemoryPort 注入记忆消费契约（P1）。nil = 关闭记忆功能。
func (h *Harness) SetMemoryPort(p memoryport.Port) {
	h.memoryPort = p
}

// HarnessCoreOutput is provisional. RunCore never reads or writes session
// history and never emits terminal/UI events, allowing writingruntime to own
// every authoritative commit.
type HarnessCoreOutput struct {
	Article       string
	ArticleTitle  string
	TotalTokens   int
	SearchResults []engine.SearchResult
	ReviewResult  *engine.ReviewResult
}

func (h *Harness) RunCore(ctx context.Context, execCtx *engine.ExecutionContext, session *WritingSession) (HarnessCoreOutput, error) {
	if h == nil || execCtx == nil || session == nil {
		return HarnessCoreOutput{}, fmt.Errorf("harness core requires execution context and isolated session")
	}
	if err := h.runCore(ctx, execCtx, session); err != nil {
		return HarnessCoreOutput{}, err
	}
	return HarnessCoreOutput{Article: execCtx.Article, ArticleTitle: execCtx.ArticleTitle,
		TotalTokens: execCtx.TotalTokens, SearchResults: append([]engine.SearchResult(nil), session.SearchResults...),
		ReviewResult: session.ReviewResult}, nil
}

// runCore is the single execution path: no session persistence, no memory
// retrieval, no compaction, no emitter, no settle callbacks. Everything it
// touches lives in the caller-provided execCtx/session for this run only.
func (h *Harness) runCore(ctx context.Context, execCtx *engine.ExecutionContext, session *WritingSession) error {
	execCtx.Status = engine.StatusRunning

	slog.Info("harness core started",
		"trace_id", execCtx.TraceID,
		"conversation_id", session.ConversationID,
		"user_input", execCtx.UserInput,
		"has_article", session.HasArticle(),
		"search_results", len(session.SearchResults),
	)

	// 1. 意图判定（规则，不调 LLM）
	intent := ClassifyIntent(execCtx.UserInput, session)
	execCtx.TaskIntent = &engine.TaskIntent{
		TaskMode:        string(intent),
		Confidence:      0.9,
		Source:          "rules",
		NormalizedInput: execCtx.UserInput,
	}

	slog.Info("harness: intent classified",
		"trace_id", execCtx.TraceID,
		"intent", intent,
	)

	// 2. 构建 guided 标志（供 buildMessages 和 ToolsForIntent 使用）
	isGuided := execCtx.Mode == "guided"

	// 3. 构建消息
	messages := h.buildMessages(execCtx, session, intent, isGuided)

	// 4. 选择工具集
	hasSearch := h.search != nil && h.search.HasSources()
	hasKB := h.kbSearcher != nil
	toolDefs := ToolsForIntent(intent, hasSearch, isGuided, hasKB)

	// 5. 构建工具执行器（含声明式 MaxCalls guard）。
	// SettleFunc 恒为 nil：扣费属于用户侧副作用，执行核绝不消耗积分。
	executorCfg := ToolExecutorConfig{
		Search:     h.search,
		KBSearcher: h.kbSearcher,
		Session:    session,
		ExecCtx:    execCtx,
		Profile:    h.profile,
		LLM:        h.llm,
		MemoryPort: h.memoryPort,
		MaxCalls:   defaultMaxCalls(intent),
		Intent:     intent,
	}
	executor := BuildToolExecutor(executorCfg)

	// 6. 构建 LLM 选项
	opts := h.buildLLMOptions(intent, session)

	// 7. 流式状态管理
	var bodyBuf strings.Builder
	var articleTitle string
	articleIntent := isArticleIntent(intent)

	// savedArticle 保存正文内容。
	// 当 LLM 输出正文后调用 review_article 等工具时，onReset 会触发，
	// 此时 bodyBuf 中已有正文。我们在 onReset 中将正文保存到 savedArticle，
	// 防止后续 LLM 输出的评审说明覆盖正文内容。
	var savedArticle string
	var savedTitle string
	var savedProtocol steps.ArticleOutputProtocol

	// 断线检测：创建可取消的 context，在 onDelta 中检查断线
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	disconnected := false

	// streamBody 写入最终正文缓冲区（执行核不向前端转发）。
	streamBody := func(text string) {
		bodyBuf.WriteString(text)
	}

	confirmedTitle := strings.TrimSpace(session.ArticleTitle)
	if session.Outline != nil && strings.TrimSpace(session.Outline.Title) != "" {
		confirmedTitle = strings.TrimSpace(session.Outline.Title)
	}

	// 文章类任务统一使用增量解析器：规范输出为 Markdown 标题，旧 JSON
	// 协议仍可读取。对话回复不经过解析，避免误删普通 Markdown 标题。
	var articleParser *steps.ArticleStreamParser
	if articleIntent {
		articleParser = steps.NewArticleStreamParser(steps.ArticleStreamParserConfig{
			ConfirmedTitle: confirmedTitle,
			OnTitle: func(title string) {
				articleTitle = title
				execCtx.ArticleTitle = title
			},
			OnBody: streamBody,
		})
	}

	// Unified LLM trace capture for the harness agent-loop/stream call.
	// Derives TTFT from the first streamed delta and total tokens from the
	// returned count, so harness runs populate agent_traces.llm_calls too.
	harnessLLMStart := time.Now()
	var harnessTTFT *time.Duration
	harnessFirstToken := false

	onDelta := func(delta string) {
		if !harnessFirstToken && delta != "" {
			harnessFirstToken = true
			d := time.Since(harnessLLMStart)
			harnessTTFT = &d
		}
		// 检查客户端是否已断开
		if !disconnected && execCtx.IsDisconnected() {
			disconnected = true
			slog.Info("harness: client disconnected during streaming, cancelling",
				"trace_id", execCtx.TraceID,
				"buffered_chars", bodyBuf.Len(),
			)
			streamCancel()
			return
		}
		if disconnected {
			return // 已断开，丢弃后续 delta
		}

		if articleParser != nil {
			articleParser.Push(delta)
		} else {
			streamBody(delta)
		}
	}

	onReasoning := func(delta string) {
		// 执行核不转发推理增量（无 UI 通道）。
		_ = delta
	}

	onReset := func() {
		// 如果 bodyBuf 中已有正文内容，先保存它。
		// 这样在 LLM 后续调用 review_article 等工具时，
		// 正文不会被后续的评审说明覆盖。
		if bodyBuf.Len() > 0 {
			savedArticle = bodyBuf.String()
			savedTitle = articleTitle
			if articleParser != nil {
				savedProtocol = articleParser.Protocol()
			}
			slog.Info("harness: saving article body before stream reset",
				"trace_id", execCtx.TraceID,
				"article_chars", len([]rune(savedArticle)),
			)
		}
		bodyBuf.Reset()
		if articleParser != nil {
			articleParser.Reset()
		}
		// 下一轮流式输出会重新解析标题。
		articleTitle = ""
	}

	// 8. 启动 LLM 持续会话
	var fullText string
	var tokens int
	var err error

	// Capture prompt/completion/cache splits that the streaming return omits, so
	// write-draft cost and output-throughput are accurate (not just total).
	callCtx, usage := tools.WithUsageCapture(streamCtx)

	if len(toolDefs) > 0 {
		fullText, tokens, err = h.llm.ChatWithTools(
			callCtx, messages,
			onDelta, onReasoning, onReset,
			toolDefs, executor,
			opts...,
		)
	} else {
		// 纯流式对话
		fullText, tokens, err = h.llm.ChatStreamWithReasoning(
			callCtx, messages,
			onDelta, onReasoning,
			opts...,
		)
	}

	// Record the generation call onto the unified LLM trace (fills
	// agent_traces.llm_calls so harness runs report token/cost/latency/TTFT).
	provider := "unknown"
	if strings.Contains(h.llm.Model(), "deepseek") {
		provider = "deepseek"
	}
	apiEndpoint := "harness_agent_loop"
	if len(toolDefs) == 0 {
		apiEndpoint = "harness_stream"
	}
	generationStep := engine.StepChat
	if articleIntent {
		generationStep = engine.StepWrite
	}
	generationRecord := engine.LLMCallRecord{
		CallID:        fmt.Sprintf("harness-%s-%d", execCtx.TraceID, harnessLLMStart.UnixNano()),
		Step:          generationStep,
		Model:         h.llm.Model(),
		Provider:      provider,
		TotalTokens:   tokens,
		StartedAt:     harnessLLMStart,
		CompletedAt:   time.Now(),
		LatencyMs:     time.Since(harnessLLMStart).Milliseconds(),
		TTFT:          harnessTTFT,
		Success:       err == nil,
		APIEndpoint:   apiEndpoint,
		PromptSummary: fmt.Sprintf("%d messages", len(messages)),
	}
	if usage != nil && usage.HasUsage {
		generationRecord.PromptTokens = usage.Prompt
		generationRecord.CompletionTokens = usage.Completion
		generationRecord.ReasoningTokens = usage.Reasoning
		generationRecord.CacheHitTokens = usage.CacheHit
		generationRecord.CacheMissTokens = usage.CacheMiss
		if usage.Total > 0 {
			generationRecord.TotalTokens = usage.Total
		}
	}
	if err != nil {
		generationRecord.Error = err.Error()
	} else {
		generationRecord.ResponseSummary = fmt.Sprintf("Generated %d chars", len([]rune(fullText)))
	}
	execCtx.RecordLLMCall(generationRecord)

	// 断线处理：LLM 调用因断线取消，标记为 Paused 而非 Failed
	if disconnected {
		slog.Info("harness: stream cancelled due to client disconnect",
			"trace_id", execCtx.TraceID,
			"intent", intent,
			"buffered_chars", bodyBuf.Len(),
		)
		execCtx.Status = engine.StatusPaused
		return nil
	}

	// 流式客户端在读取中断（取消/超时/断连）时返回部分文本和 nil error。
	// governed Core 必须稳定失败，否则被取消或超时的节点会以截断正文
	// 走完 canonical 提交。
	if coreErr := streamCtx.Err(); coreErr != nil {
		execCtx.Status = engine.StatusFailed
		return fmt.Errorf("harness core stream cancelled: %w", coreErr)
	}

	if err != nil {
		// 配额/断路器检查
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "quota") || strings.Contains(errMsg, "402") {
			execCtx.Status = engine.StatusFailed
			return engine.ErrQuotaExceeded
		}
		return fmt.Errorf("harness LLM call failed: %w", err)
	}

	// 9. 收尾
	execCtx.TotalTokens = tokens
	articleBody := bodyBuf.String()
	var outputProtocol steps.ArticleOutputProtocol
	if articleParser != nil {
		parsed := articleParser.Finalize(fullText)
		articleBody = parsed.Body
		articleTitle = parsed.Title
		outputProtocol = parsed.Protocol
	} else if articleBody == "" {
		articleBody = fullText
	}
	// 如果 session.Reviewed 为 true 且 savedArticle 有值，
	// 说明 LLM 在输出正文后调用了 review_article，
	// onReset 保存了正文到 savedArticle，
	// 而 bodyBuf 中的内容是评审说明/写作分析，不是正文。
	// 此时优先使用 savedArticle 作为正文内容。
	// 但如果 bodyBuf 的内容比 savedArticle 长很多（如 revise_section 后的新文章），
	// 说明 bodyBuf 是新正文，应该用 bodyBuf。
	useSavedArticle := false
	if session.Reviewed && savedArticle != "" {
		// 新一轮若重新输出了可识别标题和足量正文，则它是 revise_section
		// 后的新文章；否则最终一轮通常只是评审说明，应保留 reset 前的正文。
		if outputProtocol != steps.ArticleProtocolMissingTitle && articleTitle != "" && len([]rune(articleBody)) > 200 {
			// 当前缓冲区是新文章，使用它。
		} else {
			useSavedArticle = true
		}
	}
	if articleBody == "" && savedArticle != "" {
		useSavedArticle = true
	}
	if useSavedArticle {
		articleBody = savedArticle
		articleTitle = savedTitle
		outputProtocol = savedProtocol
	}
	if articleBody == "" && articleParser == nil {
		articleBody = fullText
	}
	if articleParser != nil {
		engine.RecordArticleOutputProtocol(execCtx, string(outputProtocol))
		slog.Info("harness: article output protocol resolved",
			"trace_id", execCtx.TraceID,
			"protocol", outputProtocol,
			"deviated", outputProtocol != steps.ArticleProtocolMarkdown,
		)
	}

	// 写作/修改意图：更新文章
	if articleIntent {
		// 保存旧版本
		if session.HasArticle() {
			session.PushArticleVersion(session.CurrentArticle)
		}
		session.CurrentArticle = articleBody
		execCtx.Article = articleBody
		if articleTitle != "" {
			session.ArticleTitle = articleTitle
			execCtx.ArticleTitle = articleTitle
		} else if session.ArticleTitle != "" {
			execCtx.ArticleTitle = session.ArticleTitle
		}
	} else {
		// 对话意图：articleBody 就是对话回复
		execCtx.Article = articleBody
	}

	// 执行核收尾：无 StreamDone / StoreMessage / 记忆提取 / Completed 事件
	// ——这些用户侧副作用随 Legacy 交互路径移除（⑥D）。

	execCtx.Status = engine.StatusCompleted
	slog.Info("harness core completed",
		"trace_id", execCtx.TraceID,
		"intent", intent,
		"article_length", len([]rune(articleBody)),
		"total_tokens", execCtx.TotalTokens,
	)

	return nil
}

func isArticleIntent(intent Intent) bool {
	switch intent {
	case IntentWriting, IntentPolish, IntentShorten, IntentExpand:
		return true
	default:
		return false
	}
}

// buildMessages 构建 LLM 消息列表。
func (h *Harness) buildMessages(execCtx *engine.ExecutionContext, session *WritingSession, intent Intent, isGuided bool) []tools.LLMMessage {
	var messages []tools.LLMMessage

	// System message
	systemPrompt := h.buildSystemPrompt(session, intent, isGuided)
	messages = append(messages, tools.LLMMessage{
		Role:    "system",
		Content: systemPrompt,
	})

	// 对话历史（最近 6 条，避免 token 溢出）
	// 重要：assistant 的 article 类型消息用轻量摘要替代，全文已在 system prompt 中。
	// 这样 messages 中不携带冗长文章副本，大幅减少 token 消耗。
	for _, msg := range session.RecentMessages(6) {
		content := msg.Content

		// assistant 的文章回复用轻量摘要替代
		if msg.Role == memory.RoleAssistant && msg.ContentType == memory.ContentArticle {
			articleLen := len([]rune(content))
			content = fmt.Sprintf("[已输出文章 %d 字，见当前文章全文]", articleLen)
		} else {
			// 安全截断
			maxLen := 800
			if len(content) > maxLen {
				content = content[:maxLen] + "...（已截断）"
			}
		}

		messages = append(messages, tools.LLMMessage{
			Role:    string(msg.Role),
			Content: content,
		})
	}

	// 当前用户输入。文章类任务在上下文最末端再次声明输出契约，避免长上下文、
	// 工具调用或多轮修订后，模型遗忘 system prompt 中较早出现的格式要求。
	currentInput := execCtx.UserInput
	switch intent {
	case IntentWriting, IntentPolish, IntentShorten, IntentExpand:
		currentInput += "\n\n" + profile.MarkdownArticleOutputReminder
	}
	messages = append(messages, tools.LLMMessage{
		Role:    "user",
		Content: currentInput,
	})

	return messages
}

// buildSystemPrompt 构建 system prompt。
//
// v3.0 架构（借鉴 Codex WorldState diff 模式）：
//   - system prompt 由多个 WorldStateSection 组成
//   - 每轮只推送变化的 section（增量 diff），不变的不重发
//   - 跨轮保留 section 基线，配合 history_version 追踪
//
// 保留的设计：
//   - P2 按需上下文：LLM 通过 retrieve_context 工具主动获取信息
//   - 文章全文放 system prompt（配合 prompt caching）
//   - chat 意图精简注入
//
// 新增的设计：
//   - WorldState diff：只推送变化的 section，减少 Token 消耗
//   - AutoCompactFallback：Token 预算不足时自动触发压缩
func (h *Harness) buildSystemPrompt(session *WritingSession, intent Intent, isGuided bool) string {
	intentStr := string(intent)

	// 构建 WorldState（每轮重建 section，但基线跨轮保留在 h.worldState 中）
	_ = worldstate.BuildWorldStateForHarness(
		h.profile,
		intentStr,
		isGuided,
		session.CurrentArticle,
		session.UserMaterials,
		len(session.SearchResults),
	)

	// 复用 Harness 持有的基线（跨轮保留）
	// 将 section 注册到 h.worldState 中（替换内容但保留基线）
	h.worldState.Register(worldstate.NewProfileSection(h.profile, intentStr, isGuided))
	h.worldState.Register(worldstate.NewArticleSection(session.CurrentArticle))
	h.worldState.Register(worldstate.NewDateSection())
	h.worldState.Register(worldstate.NewMaterialsSection(session.UserMaterials, len(session.SearchResults)))
	h.worldState.Register(worldstate.NewRulesSectionWithDetails(h.profile, intentStr, isGuided, 0))
	h.worldState.Register(worldstate.NewTaskInstructionsSection(intentStr, isGuided))
	h.worldState.Register(worldstate.NewSecuritySection())
	// P1-1: 用户记忆偏好进 system prompt（此前只靠 LLM 主动拉取）
	if bundle, ok := session.MemoryContext.(*memoryport.Bundle); ok {
		h.worldState.Register(worldstate.NewMemorySection(bundle))
	}

	// 增量推送：只返回变化的 section
	fragments := h.worldState.UpdateWorldState()

	var sb strings.Builder
	for _, frag := range fragments {
		sb.WriteString(frag.Body)
	}

	// ── P2 按需上下文：retrieve_context 指引 ──
	// 这部分每轮都需要（因为 LLM 需要被提醒可用工具）
	// 但因为内容固定，在 WorldState 中由 SecuritySection 处理去重
	sb.WriteString("\n--- 上下文查询指引 ---\n")
	sb.WriteString("当你需要以下信息时，请调用 retrieve_context 工具按需获取，而非猜测：\n")
	sb.WriteString("- 当前文章的特定段落 → retrieve_context(source=\"article\", query=\"段落描述\")\n")
	sb.WriteString("- 用户的写作偏好/历史记忆 → retrieve_context(source=\"memory\", query=\"偏好描述\")\n")
	sb.WriteString("- 已收集的搜索素材 → retrieve_context(source=\"search\", query=\"素材关键词\")\n")
	sb.WriteString("- 当前风格配置详情 → retrieve_context(source=\"profile\", query=\"配置项\")\n")
	sb.WriteString("- 对话历史中的关键信息 → retrieve_context(source=\"history\", query=\"信息描述\")\n")
	sb.WriteString("retrieve_context 预算有限（最多 3 次），一次查询尽量具体；预算用尽后不要再调用。\n")

	if session.HasArticle() {
		sb.WriteString(fmt.Sprintf("\n当前已有文章（%d 字）。如需查看内容，请调用 retrieve_context(source=\"article\", query=\"文章相关描述\")。\n",
			len([]rune(session.CurrentArticle))))
	}
	if len(session.SearchResults) > 0 {
		sb.WriteString(fmt.Sprintf("\n已有 %d 条搜索素材。如需查看，请调用 retrieve_context(source=\"search\", query=\"素材关键词\")。\n",
			len(session.SearchResults)))
	}

	// ── 已确认提纲（Guided 模式，常驻）──
	if session.Outline != nil && session.Outline.Title != "" {
		sb.WriteString(fmt.Sprintf("\n【标题（必须原样使用，不得修改）】：%s\n", session.Outline.Title))
		sb.WriteString("【写作提纲（必须严格按照以下提纲展开，每个要点对应一个段落，不得增删或更改要点顺序）】：\n")
		// 开放类型标签：已知类型翻译为中文，未知类型原样展示
		typeLabels := map[string]string{
			"opening":    "开头",
			"argument":   "分论点",
			"conclusion": "结尾",
			"intro":      "引言",
			"method":     "方法",
			"experiment": "实验",
			"discussion": "讨论",
			"abstract":   "摘要",
		}
		for i, item := range session.Outline.Outline {
			label := typeLabels[item.Type]
			if label == "" {
				label = item.Type
			}
			sb.WriteString(fmt.Sprintf("%d. [%s] %s\n", i+1, label, item.Point))
		}
		sb.WriteString("\n")
	}

	// ── AutoCompactFallback 检查 ──
	// 如果 Token 预算不足，注入压缩降级提示
	if h.autoCompact != nil && h.tokenBudget != nil {
		if h.autoCompact.ShouldCompact(h.tokenBudget) {
			sb.WriteString(h.autoCompact.CompactPrompt(0))
		}
	}

	promptStr := sb.String()
	slog.Debug("harness: system prompt built (WorldState diff)",
		"intent", intent,
		"prompt_chars", len([]rune(promptStr)),
		"history_version", h.worldState.Version(),
		"fragments_pushed", len(fragments),
	)
	return promptStr
}

// buildLLMOptions 构建 LLM 调用选项。
// reasoning_effort 不再硬编码——由 LLMClient 的 client 级别默认值决定，
// 该默认值从数据库 model_configs.reasoning_effort 读取。
// 如果需要按意图动态调整，可以在这里覆盖。
func (h *Harness) buildLLMOptions(intent Intent, session *WritingSession) []tools.ChatOption {
	opts := []tools.ChatOption{}

	switch intent {
	case IntentWriting:
		opts = append(opts,
			tools.WithThinking(true),
		)
	case IntentPolish, IntentShorten, IntentExpand:
		opts = append(opts,
			tools.WithThinking(true),
		)
	case IntentChat:
		opts = append(opts, tools.WithThinking(false))
	}

	// 随机温度调整
	if session != nil {
		// 简单的随机温度（后续可以接入 StochasticState）
	}

	return opts
}

// extractTitleFromMarkdown 已统一为 steps.ExtractTitleFromMarkdown，
// 支持 ## 、# 标题，以及短行回退。
//
// 收尾 fallback：如果流式过程中未能提取到标题，
// 在最终正文上再尝试一次（在 Run 方法收尾时调用）。
