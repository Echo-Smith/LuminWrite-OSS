package agent

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── Compaction: 对话历史压缩 ──────────────────────────────
//
// 借鉴 dsh 的 compaction 模式：当对话历史消息过多时，用 LLM 将
// 旧消息压缩为一条摘要消息，替换原始消息列表。
//
// 触发条件：
//   - 对话历史超过 compactionThreshold 条消息
//   - 或估算的 token 总量超过 compactionTokenThreshold
//
// 压缩策略：
//   - 保留最近 N 条消息不动（keepRecent）
//   - 将更早的消息用 LLM 压缩为一条 system 摘要
//   - 向前端推送 agent.compaction 事件，显示压缩状态
//
// 设计原则（继承 dsh）：
//   - 不可逆：压缩后的摘要替代原始消息，不保留原始内容
//   - 透明：前端可见压缩发生了，显示节省的 token 数
//   - 安全：LLM 压缩失败时回退到简单截断

const (
	// compactionThreshold 触发压缩的最小历史消息数
	compactionThreshold = 10

	// compactionKeepRecent 压缩时保留最近的 N 条消息不动
	compactionKeepRecent = 4

	// compactionTokenThreshold 触发压缩的 token 估算阈值
	// 中文约 1.5 字/token，6000 token ≈ 9000 字
	compactionTokenThreshold = 6000

	// compactionMaxSummaryLen 压缩摘要的最大字符数
	compactionMaxSummaryLen = 800
)

// estimateTokens 粗略估算消息列表的 token 数。
// 中文约 1.5 字/token，英文约 4 字符/token。
// 这里用简单的字符数 / 2 估算（偏保守）。
func estimateTokens(messages []tools.LLMMessage) int {
	totalChars := 0
	for _, msg := range messages {
		totalChars += len(msg.Content)
		if msg.ReasoningContent != "" {
			totalChars += len(msg.ReasoningContent)
		}
	}
	return totalChars / 2
}

// estimateMemoryTokens 估算 ConversationMessage 列表的 token 数
func estimateMemoryTokens(msgs []memory.ConversationMessage) int {
	totalChars := 0
	for _, msg := range msgs {
		totalChars += len(msg.Content)
	}
	return totalChars / 2
}

// maybeCompact 检查是否需要压缩对话历史，如果需要则执行压缩。
// 返回压缩后的消息列表和压缩信息（如果发生了压缩）。
//
// v3.0 增强（借鉴 Codex AutoCompactFallback）：
//  1. 检查 session.Messages 长度是否超过阈值（原有逻辑）
//  2. 检查 Token 预算是否不足（新增：AutoCompactFallback）
//  3. 将旧消息（保留最近 N 条）发送给 LLM 生成摘要
//  4. 用摘要消息替换旧消息
//  5. 重置 WorldState 基线（新增：压缩后全量推送下一轮）
//  6. 通过 emitter 推送 compaction 事件到前端
func (h *Harness) maybeCompact(
	ctx context.Context,
	execCtx *engine.ExecutionContext,
	session *WritingSession,
	intent Intent,
) (compacted bool, savedTokens int) {
	// ── 检查 1：消息数阈值（原有逻辑）──
	needCompact := len(session.Messages) >= compactionThreshold
	triggerReason := "threshold"

	// ── 检查 2：Token 预算不足（新增 AutoCompactFallback）──
	if !needCompact && h.autoCompact != nil && h.tokenBudget != nil {
		if h.autoCompact.ShouldCompact(h.tokenBudget) {
			needCompact = len(session.Messages) > compactionKeepRecent // 至少有可压缩的消息
			triggerReason = "token_budget"
			slog.Info("harness: auto-compact triggered by token budget",
				"trace_id", execCtx.TraceID,
				"remaining_tokens", h.tokenBudget.Remaining(),
				"messages", len(session.Messages),
			)
		}
	}

	if !needCompact {
		return false, 0
	}

	// 估算旧消息的 token 数
	oldMsgs := session.Messages[:len(session.Messages)-compactionKeepRecent]
	oldTokens := estimateMemoryTokens(oldMsgs)
	if oldTokens < 500 {
		// 太少不值得压缩
		return false, 0
	}

	// 构建压缩请求
	summary, err := h.compactHistory(ctx, oldMsgs, intent)
	if err != nil {
		slog.Warn("harness: compaction failed, falling back to truncation",
			"trace_id", execCtx.TraceID,
			"error", err,
		)
		// 回退策略：用简单截断替代 LLM 压缩
		summary = simpleTruncate(oldMsgs)
	}

	// 估算节省的 token
	savedTokens = oldTokens - len(summary)/2
	if savedTokens < 0 {
		savedTokens = 0
	}

	// 更新 session.Messages：用摘要替换旧消息
	compactedMsgs := make([]memory.ConversationMessage, 0, compactionKeepRecent+1)
	compactedMsgs = append(compactedMsgs, memory.ConversationMessage{
		Role:        memory.RoleSystem,
		Content:     summary,
		ContentType: memory.ContentText,
	})
	compactedMsgs = append(compactedMsgs, session.Messages[len(session.Messages)-compactionKeepRecent:]...)
	session.Messages = compactedMsgs

	// ── 重置 WorldState 基线（v3.0 新增）──
	// 压缩后，下一轮 system prompt 需要全量推送（因为上下文已变化）
	if h.worldState != nil {
		h.worldState.ResetBaselines()
		h.historyVersion++
		slog.Debug("harness: world state baselines reset after compaction",
			"trace_id", execCtx.TraceID,
			"history_version", h.historyVersion,
		)
	}

	// 推送 compaction 事件到前端
	if h.emitter != nil {
		preview := summary
		if len([]rune(preview)) > 100 {
			preview = string([]rune(preview)[:100]) + "…"
		}
		h.emitter.Compaction(len(oldMsgs), savedTokens, preview, h.historyVersion, triggerReason)
	}

	slog.Info("harness: conversation history compacted",
		"trace_id", execCtx.TraceID,
		"original_messages", len(oldMsgs),
		"saved_tokens", savedTokens,
		"summary_chars", len([]rune(summary)),
		"history_version", h.historyVersion,
	)

	return true, savedTokens
}

// compactHistory 用 LLM 将对话历史压缩为摘要。
func (h *Harness) compactHistory(ctx context.Context, msgs []memory.ConversationMessage, intent Intent) (string, error) {
	if h.llm == nil || len(msgs) == 0 {
		return "", fmt.Errorf("LLM client not available or no messages to compact")
	}

	// 构建压缩请求
	var historyBuf strings.Builder
	for i, msg := range msgs {
		role := string(msg.Role)
		if msg.Role == memory.RoleSystem {
			role = "system"
		}

		content := msg.Content
		// 文章类型用摘要替代
		if msg.Role == memory.RoleAssistant && msg.ContentType == memory.ContentArticle {
			articleLen := len([]rune(content))
			content = fmt.Sprintf("[文章 %d 字]", articleLen)
		} else if len(content) > 500 {
			content = string([]rune(content)[:500]) + "…"
		}

		historyBuf.WriteString(fmt.Sprintf("[%d] %s: %s\n", i+1, role, content))
	}

	systemMsg := "你是对话压缩器。将以下对话历史压缩为一份简洁的摘要，保留关键信息（用户意图、已做的工作、重要结论、文章状态）。只输出摘要内容，不要寒暄。摘要控制在 500 字以内。"
	userMsg := fmt.Sprintf(`以下是对话历史，请压缩为摘要：

%s

要求：
1. 保留用户的核心需求和意图
2. 保留已完成的工作（搜索了什么、写了什么、评审结果）
3. 保留关键决策和用户偏好
4. 省略冗余的寒暄和过渡
5. 用简洁的叙述风格，不要用列表格式`, historyBuf.String())

	resp, _, err := h.llm.Chat(ctx, []tools.LLMMessage{
		{Role: "system", Content: systemMsg},
		{Role: "user", Content: userMsg},
	}, tools.WithTemperature(0.1), tools.WithThinking(false))
	if err != nil {
		return "", fmt.Errorf("compaction LLM call failed: %w", err)
	}

	if resp == "" {
		return "", fmt.Errorf("compaction LLM returned empty response")
	}

	// 前缀标记，让 buildMessages 知道这是压缩摘要
	return "[对话历史摘要] " + resp, nil
}

// simpleTruncate 是 compaction 失败时的回退策略：
// 简单截取每条消息的前 100 字拼接为摘要。
func simpleTruncate(msgs []memory.ConversationMessage) string {
	var sb strings.Builder
	sb.WriteString("[对话历史摘要（截断模式）]\n")
	for i, msg := range msgs {
		content := msg.Content
		if len([]rune(content)) > 100 {
			content = string([]rune(content)[:100]) + "…"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", string(msg.Role), content))
		if i >= 5 {
			sb.WriteString("…（更早的消息已省略）\n")
			break
		}
	}
	return sb.String()
}
