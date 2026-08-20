package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/profile"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── Harness: 单层 LLM 编排器 ──────────────────────────────
//
// Harness 是架构 C 的核心编排器。
//
// 设计原则（继承 dsh/Pi 理念 [[memory:178679655388010121476]]）：
//   - Harness 管：意图路由、工具集选择、会话状态、断路器、超时、断线重连
//   - LLM 执行：在持续会话中自主决定调用什么工具、何时写作、何时修正
//   - 单层：不存在外层 ReAct + 内层 agent loop 的嵌套
//
// 核心特点：
//   - 意图判定走规则（毫秒级），不走 LLM
//   - 工具粒度细（search_web, read_source, write_article, review_article, revise_section）
//   - 会话状态跨轮保留（文章、素材、记忆）

// Harness 依赖项。
type Harness struct {
	llm           *tools.LLMClient
	search        *tools.SearchClient
	kbSearcher    tools.KnowledgeSearcher
	profile       *profile.StyleProfile
	sessionStore  SessionStore
	emitter       engine.EventEmitter
	maxIterations int
}

// NewHarness creates a Harness orchestrator.
func NewHarness(llm *tools.LLMClient, search *tools.SearchClient, kb tools.KnowledgeSearcher, p *profile.StyleProfile, store SessionStore, emitter engine.EventEmitter) *Harness {
	return &Harness{
		llm:           llm,
		search:        search,
		kbSearcher:    kb,
		profile:       p,
		sessionStore:  store,
		emitter:       emitter,
		maxIterations: 12,
	}
}

// Run 执行单次写作/对话请求。
// execCtx 持有本次请求的输入和状态，session 持有跨轮的会话状态。
func (h *Harness) Run(ctx context.Context, execCtx *engine.ExecutionContext, session *WritingSession) error {
	execCtx.Status = engine.StatusRunning

	slog.Info("harness started",
		"trace_id", execCtx.TraceID,
		"conversation_id", session.ConversationID,
		"user_input", execCtx.UserInput,
		"has_article", session.HasArticle(),
		"search_results", len(session.SearchResults),
	)

	// 1. 加载对话历史
	session.LoadHistory(ctx, h.sessionStore, 50)

	// 1b. 主动检索记忆（如果 SessionStore 实现了 MemoryRetriever）
	if session.MemoryContext == nil {
		h.retrieveMemory(ctx, execCtx, session)
	}

	// 2. 意图判定（规则，不调 LLM）
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

	// 3. 构建 guided 标志（供 buildMessages 和 ToolsForIntent 使用）
	isGuided := execCtx.Mode == "guided"

	// 3b. 对话历史压缩（借鉴 dsh compaction 模式）
	// 在构建消息前检查是否需要压缩历史，避免 token 溢出
	h.maybeCompact(ctx, execCtx, session, intent)

	// 4. 构建消息
	messages := h.buildMessages(execCtx, session, intent, isGuided)

	// 5. 选择工具集
	hasSearch := h.search != nil && h.search.HasSources()
	hasKB := h.kbSearcher != nil
	toolDefs := ToolsForIntent(intent, hasSearch, isGuided, hasKB)

	// 5. 构建工具执行器（含声明式 MaxCalls guard）
	executorCfg := ToolExecutorConfig{
		Search:     h.search,
		KBSearcher: h.kbSearcher,
		Session:    session,
		ExecCtx:    execCtx,
		Emitter:    h.emitter,
		Profile:    h.profile,
		LLM:        h.llm,
		MaxCalls:   defaultMaxCalls(intent),
	}
	executor := BuildToolExecutor(executorCfg)

	// 6. 构建 LLM 选项
	opts := h.buildLLMOptions(intent, session)

	// 7. 流式状态管理
	var bodyBuf strings.Builder
	titleResolved := false
	var articleTitle string

	// savedArticle 保存正文内容。
	// 当 LLM 输出正文后调用 review_article 等工具时，onReset 会触发，
	// 此时 bodyBuf 中已有正文。我们在 onReset 中将正文保存到 savedArticle，
	// 防止后续 LLM 输出的评审说明覆盖正文内容。
	var savedArticle string

	// 断线检测：创建可取消的 context，在 onDelta 中检查断线
	streamCtx, streamCancel := context.WithCancel(ctx)
	defer streamCancel()

	disconnected := false

	onDelta := func(delta string) {
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

		bodyBuf.WriteString(delta)
		if h.emitter != nil {
			h.emitter.StreamDelta(delta)
		}
		// 在流式输出中提取标题
		if !titleResolved && intent == IntentWriting {
			buf := bodyBuf.String()
			if title := extractTitleFromMarkdown(buf); title != "" {
				articleTitle = title
				titleResolved = true
				execCtx.ArticleTitle = title
				if h.emitter != nil {
					h.emitter.ArticleTitle(title)
				}
			}
		}
	}

	onReasoning := func(delta string) {
		if h.emitter != nil {
			h.emitter.ReasoningDelta(delta)
		}
	}

	onReset := func() {
		// 如果 bodyBuf 中已有正文内容，先保存它。
		// 这样在 LLM 后续调用 review_article 等工具时，
		// 正文不会被后续的评审说明覆盖。
		if bodyBuf.Len() > 0 {
			savedArticle = bodyBuf.String()
			slog.Info("harness: saving article body before stream reset",
				"trace_id", execCtx.TraceID,
				"article_chars", len([]rune(savedArticle)),
			)
		}
		bodyBuf.Reset()
		// StreamReset 清空前端所有 streaming text parts。
		// 不发 StreamDone，避免中间版本的正文被标记为 streaming:false 留在消息中。
		// 最终的 StreamDone 在 Run 收尾时发送一次，确保前端只有一篇最终文章。
		if h.emitter != nil {
			h.emitter.StreamReset()
		}
	}

	// 8. 启动 LLM 持续会话
	var fullText string
	var tokens int
	var err error

	if len(toolDefs) > 0 {
		fullText, tokens, err = h.llm.ChatWithTools(
			streamCtx, messages,
			onDelta, onReasoning, onReset,
			toolDefs, executor,
			opts...,
		)
	} else {
		// 纯流式对话
		fullText, tokens, err = h.llm.ChatStreamWithReasoning(
			streamCtx, messages,
			onDelta, onReasoning,
			opts...,
		)
	}

	// 断线处理：LLM 调用因断线取消，标记为 Paused 而非 Failed
	if disconnected {
		slog.Info("harness: stream cancelled due to client disconnect",
			"trace_id", execCtx.TraceID,
			"intent", intent,
			"buffered_chars", bodyBuf.Len(),
		)
		execCtx.Status = engine.StatusPaused
		if h.emitter != nil {
			h.emitter.PausedWithReason(engine.StepName("streaming"), nil, "disconnect")
		}
		return nil
	}

	if err != nil {
		// 配额/断路器检查
		errMsg := strings.ToLower(err.Error())
		if strings.Contains(errMsg, "quota") || strings.Contains(errMsg, "402") {
			h.emitter.Error("quota_exceeded", "AI 模型服务额度不足", execCtx.CurrentStep)
			execCtx.Status = engine.StatusFailed
			return engine.ErrQuotaExceeded
		}
		return fmt.Errorf("harness LLM call failed: %w", err)
	}

	// 9. 收尾
	execCtx.TotalTokens = tokens
	articleBody := bodyBuf.String()
	// 如果 session.Reviewed 为 true 且 savedArticle 有值，
	// 说明 LLM 在输出正文后调用了 review_article，
	// onReset 保存了正文到 savedArticle，
	// 而 bodyBuf 中的内容是评审说明/写作分析，不是正文。
	// 此时优先使用 savedArticle 作为正文内容。
	// 但如果 bodyBuf 的内容比 savedArticle 长很多（如 revise_section 后的新文章），
	// 说明 bodyBuf 是新正文，应该用 bodyBuf。
	if session.Reviewed && savedArticle != "" {
		// 简单启发式：如果 bodyBuf 以 ## 开头（Markdown 标题），可能是新文章
		if strings.HasPrefix(strings.TrimSpace(articleBody), "##") && len([]rune(articleBody)) > 200 {
			// bodyBuf 看起来是新文章，使用它
		} else {
			// bodyBuf 不是文章格式，使用 savedArticle
			articleBody = savedArticle
		}
	}
	if articleBody == "" && savedArticle != "" {
		articleBody = savedArticle
	}
	if articleBody == "" {
		articleBody = fullText
	}

	// 写作/修改意图：更新文章
	if intent == IntentWriting || intent == IntentPolish || intent == IntentShorten || intent == IntentExpand {
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

	// 流式完成
	if h.emitter != nil {
		h.emitter.StreamDone(articleBody)
	}

	// 存储 user 消息
	session.StoreMessage(ctx, h.sessionStore, "user", execCtx.UserInput, "text")

	// 存储 assistant 消息
	contentType := "text"
	if intent == IntentWriting || intent == IntentPolish || intent == IntentShorten || intent == IntentExpand {
		contentType = "article"
	}
	session.StoreMessage(ctx, h.sessionStore, "assistant", articleBody, contentType)

	// 发送 completed 事件
	if h.emitter != nil {
		var review interface{}
		if session.ReviewResult != nil {
			review = session.ReviewResult
		}
		h.emitter.Completed(
			articleBody,
			execCtx.ArticleTitle,
			review,
			map[string]interface{}{
				"total_tokens": execCtx.TotalTokens,
				"intent":       string(intent),
			},
		)
	}

	execCtx.Status = engine.StatusCompleted
	slog.Info("harness completed",
		"trace_id", execCtx.TraceID,
		"intent", intent,
		"article_length", len([]rune(articleBody)),
		"total_tokens", execCtx.TotalTokens,
	)

	return nil
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

	// 当前用户输入
	messages = append(messages, tools.LLMMessage{
		Role:    "user",
		Content: execCtx.UserInput,
	})

	return messages
}

// retrieveMemory 主动检索用户写作偏好和反馈记忆。
// 如果 SessionStore 实现了 MemoryRetriever 接口，则调用 Retrieve；
// 否则静默跳过。检索结果存入 session.MemoryContext。
func (h *Harness) retrieveMemory(ctx context.Context, execCtx *engine.ExecutionContext, session *WritingSession) {
	if h.sessionStore == nil {
		return
	}
	retriever, ok := h.sessionStore.(MemoryRetriever)
	if !ok {
		return
	}
	if !h.sessionStore.IsEnabledForUser(session.UserID) {
		return
	}
	if session.UserID == "" || session.UserID == "anonymous" {
		return
	}

	explicit := map[string]any{}
	if execCtx.StyleSlug != "" {
		explicit["style"] = execCtx.StyleSlug
	}
	if execCtx.Mode != "" {
		explicit["mode"] = execCtx.Mode
	}
	if execCtx.UserInput != "" {
		explicit["message"] = execCtx.UserInput
	}

	intent := "writing"
	if execCtx.TaskIntent != nil {
		intent = execCtx.TaskIntent.TaskMode
	}

	req := memory.RetrieveRequest{
		UserID:    session.UserID,
		UserInput: execCtx.UserInput,
		Intent:    intent,
		Explicit:  explicit,
		SessionID: execCtx.SessionID,
	}

	memCtx, err := retriever.Retrieve(ctx, req)
	if err != nil {
		slog.Warn("harness: memory retrieve failed", "error", err, "trace_id", execCtx.TraceID)
		return
	}
	if memCtx != nil {
		session.MemoryContext = memCtx
		execCtx.MemoryContext = memCtx
		slog.Info("harness: memory retrieved",
			"trace_id", execCtx.TraceID,
			"injected", len(memCtx.Injected),
			"review_guard", len(memCtx.ReviewGuard),
		)
	}
}

// buildSystemPrompt 构建 system prompt。
//
// 设计演进（P2 按需上下文）：
//   旧模式：全量注入（Profile + 文章全文 + 记忆 + 素材全部塞进 system prompt）
//   新模式：精简常驻 + 按需查询（只放基础身份和任务指令，LLM 通过 retrieve_context 工具
//          主动获取所需信息：文章段落、风格配置、用户记忆、搜索素材等）
//
// 好处：
//   1. Token 大幅减少（system prompt 从 3000+ tokens 降到 500-800 tokens）
//   2. 信息更精准（LLM 只获取它需要的信息，不被无关内容干扰）
//   3. 上下文窗口更充裕（留出空间给对话历史和思考过程）
//
// 保留在 system prompt 中的内容（常驻层）：
//   - 基础身份（你是谁）
//   - 当前日期
//   - 用户素材（用户主动上传的，优先级高）
//   - 已确认提纲（Guided 模式，用户已确认的最终版本）
//   - 任务指令（当前要做什么）
//   - 注入防御
//
// 移至按需查询的内容（动态层）：
//   - 风格配置详情（结构/修辞/标题指南/事实红线/价值导向）
//   - 用户记忆偏好
//   - 当前文章全文（改为通过 retrieve_context(source="article") 获取段落）
//   - 搜索素材详情（改为通过 retrieve_context(source="search") 获取）
func (h *Harness) buildSystemPrompt(session *WritingSession, intent Intent, isGuided bool) string {
	var sb strings.Builder

	// ── 基础身份 ──
	if h.profile != nil && h.profile.SystemPrompt != "" {
		sb.WriteString(h.profile.SystemPrompt)
		sb.WriteString("\n")
	} else {
		sb.WriteString("你是笔润智谈，一个专业的中文写作助手。\n")
	}

	// ── 当前日期 ──
	sb.WriteString(fmt.Sprintf("\n当前日期：%s。", time.Now().Format("2006年1月2日")))

	// ── 用户素材（优先级高，常驻）──
	if len(session.UserMaterials) > 0 {
		sb.WriteString("\n用户上传素材：\n")
		for i, mat := range session.UserMaterials {
			sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, mat))
		}
		sb.WriteString("（用户素材优先级高于 AI 检索结果）\n")
	}

	// ── 已确认提纲（Guided 模式，常驻）──
	if session.Outline != nil && session.Outline.Title != "" {
		sb.WriteString(fmt.Sprintf("\n【标题（必须原样使用，不得修改）】：%s\n", session.Outline.Title))
		sb.WriteString("【写作提纲（必须严格按照以下提纲展开，每个要点对应一个段落，不得增删或更改要点顺序）】：\n")
		typeLabels := map[string]string{
			"opening":    "开头",
			"argument":   "分论点",
			"conclusion": "结尾",
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

	// ── 上下文查询指引 ──
	// 告知 LLM 可以通过 retrieve_context 工具按需获取信息
	sb.WriteString("\n--- 上下文查询指引 ---\n")
	sb.WriteString("当你需要以下信息时，请调用 retrieve_context 工具按需获取，而非猜测：\n")
	sb.WriteString("- 当前文章的特定段落 → retrieve_context(source=\"article\", query=\"段落描述\")\n")
	sb.WriteString("- 用户的写作偏好/历史记忆 → retrieve_context(source=\"memory\", query=\"偏好描述\")\n")
	sb.WriteString("- 已收集的搜索素材 → retrieve_context(source=\"search\", query=\"素材关键词\")\n")
	sb.WriteString("- 当前风格配置详情 → retrieve_context(source=\"profile\", query=\"配置项\")\n")
	sb.WriteString("- 对话历史中的关键信息 → retrieve_context(source=\"history\", query=\"信息描述\")\n")

	if session.HasArticle() {
		sb.WriteString(fmt.Sprintf("\n当前已有文章（%d 字）。如需查看内容，请调用 retrieve_context(source=\"article\", query=\"文章相关描述\")。\n",
			len([]rune(session.CurrentArticle))))
	}
	if len(session.SearchResults) > 0 {
		sb.WriteString(fmt.Sprintf("\n已有 %d 条搜索素材。如需查看，请调用 retrieve_context(source=\"search\", query=\"素材关键词\")。\n",
			len(session.SearchResults)))
	}

	// ── 意图相关指令 ──
	switch intent {
	case IntentWriting:
		if isGuided && session.Outline == nil {
			sb.WriteString("\n\n你现在要帮用户写一篇新文章（引导模式）。请按以下步骤操作：")
			sb.WriteString("\n1. 如果已有素材不足，用 search_web 搜索网络信息，用 search_knowledge 检索知识库范文")
			sb.WriteString("\n   - search_web：时事热点、公开数据、新闻资讯、网络观点")
			sb.WriteString("\n   - search_knowledge：写作风格规范、历史范文、栏目调性、内部观点")
			sb.WriteString("\n   - 两者可同时调用（并行），结果统一通过 read_source 读取详情")
			sb.WriteString("\n2. 调用 generate_outline 生成提纲，等待用户确认")
			sb.WriteString("\n3. 用户确认提纲后，调用 write_article 开始写作，然后在下一轮回复中直接输出文章（Markdown格式，##开头作为标题）")
			sb.WriteString("\n4. 文章写完后调用 review_article 评审质量")
			sb.WriteString("\n5. 如果评审发现问题，调用 revise_section 修正")
		} else {
			sb.WriteString("\n\n你现在要帮用户写一篇新文章。请按以下步骤操作：")
			sb.WriteString("\n1. 如果已有素材不足，用 search_web 搜索网络信息，用 search_knowledge 检索知识库范文")
			sb.WriteString("\n   - search_web：时事热点、公开数据、新闻资讯、网络观点")
			sb.WriteString("\n   - search_knowledge：写作风格规范、历史范文、栏目调性、内部观点")
			sb.WriteString("\n   - 两者可同时调用（并行），结果统一通过 read_source 读取详情")
			sb.WriteString("\n2. 用 read_source 读取重要素材的详细内容")
			sb.WriteString("\n3. 调用 write_article 开始写作，然后在下一轮回复中直接输出文章（Markdown格式，##开头作为标题）")
			sb.WriteString("\n4. 文章写完后调用 review_article 评审质量")
			sb.WriteString("\n5. 如果评审发现问题，调用 revise_section 修正")
		}
	case IntentPolish, IntentShorten, IntentExpand, IntentExtract:
		sb.WriteString("\n\n用户要修改已有文章。请调用 revise_section 定向修改，不要重写全文。")
		sb.WriteString("\n调用后直接输出修改后的完整文章。")
		sb.WriteString("\n如需查看文章特定段落，调用 retrieve_context(source=\"article\", query=\"段落描述\")。")
	case IntentChat:
		sb.WriteString("\n\n用户在和你对话。直接回复即可。")
		sb.WriteString("\n如果需要查资料可以调用 search_web（网络信息）或 search_knowledge（内部知识库），但简单问题直接回答。")
	}

	sb.WriteString(engine.PromptInjectionDefenseDirective)
	promptStr := sb.String()
	slog.Debug("harness: system prompt built",
		"intent", intent,
		"prompt_chars", len([]rune(promptStr)),
	)
	return promptStr
}

// buildLLMOptions 构建 LLM 调用选项。
func (h *Harness) buildLLMOptions(intent Intent, session *WritingSession) []tools.ChatOption {
	opts := []tools.ChatOption{}

	switch intent {
	case IntentWriting:
		opts = append(opts,
			tools.WithThinking(true),
			tools.WithReasoningEffort("high"),
		)
	case IntentPolish, IntentShorten, IntentExpand:
		opts = append(opts,
			tools.WithThinking(true),
			tools.WithReasoningEffort("high"),
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

// extractTitleFromMarkdown 从 Markdown 文本中提取标题。
func extractTitleFromMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
	}
	return ""
}
