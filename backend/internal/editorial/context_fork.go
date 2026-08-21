package editorial

import (
	"fmt"
	"log/slog"
)

// ─── DAG 上下文传递（Beta: 编辑部模式 Phase 2.2）────────────
//
// 借鉴 Codex 的 SpawnAgentForkMode，控制 DAG 节点间的上下文继承范围。
// 三种模式覆盖不同场景：
// - FullHistory: 完整对话历史传递（研究→写作）
// - LastNTurns: 最近 N 轮对话（并行节点间的交叉引用）
// - SummaryOnly: 只传 Artifact + 摘要（审校节点）

// ForkContext 根据上下文传递模式，为子节点构建输入上下文。
//
// 参数：
//   - mode: 上下文传递模式
//   - nTurns: LastNTurns 模式下要保留的轮数
//   - upstreamArtifacts: 上游节点产出的 Artifact 列表
//   - parentHistory: 父节点的对话历史（FullHistory/LastNTurns 模式使用）
//
// 返回：构建好的 AgentContext，已注入输入 Artifact
func ForkContext(
	mode ContextForkMode,
	nTurns int,
	upstreamArtifacts []Artifact,
	parentHistory []map[string]string,
	role AgentRole,
	taskID, userID string,
) *AgentContext {
	ac := NewAgentContext(role, taskID, userID)

	// 始终注入上游 Artifact（无论哪种模式）
	for _, art := range upstreamArtifacts {
		ac.AddInputArtifact(art)
	}

	switch mode {
	case ContextForkFull:
		// 完整历史传递 — 将父节点历史存入 LocalMemory
		if len(parentHistory) > 0 {
			ac.LocalMemory = parentHistory
		}
		slog.Debug("context fork: full history",
			"task_id", taskID, "history_len", len(parentHistory),
			"artifacts", len(upstreamArtifacts))

	case ContextForkLastN:
		// 最近 N 轮 — 截取 parentHistory 的尾部
		if len(parentHistory) > 0 {
			start := len(parentHistory) - nTurns
			if start < 0 {
				start = 0
			}
			ac.LocalMemory = parentHistory[start:]
		}
		slog.Debug("context fork: last N turns",
			"task_id", taskID, "n", nTurns,
			"history_len", len(parentHistory), "kept", min(nTurns, len(parentHistory)))

	case ContextForkSummary:
		// 只传 Artifact + 摘要 — 不传历史
		slog.Debug("context fork: summary only",
			"task_id", taskID, "artifacts", len(upstreamArtifacts))
	}

	return ac
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// GetForkedHistory 从 AgentContext 的 LocalMemory 中提取历史。
// 仅在 ContextForkFull 或 ContextForkLastN 模式下有值。
func GetForkedHistory(ac *AgentContext) []map[string]string {
	if ac.LocalMemory == nil {
		return nil
	}
	if history, ok := ac.LocalMemory.([]map[string]string); ok {
		return history
	}
	return nil
}

// FormatArtifactSummary 格式化 Artifact 摘要，用于 SummaryOnly 模式。
func FormatArtifactSummary(artifacts []Artifact) string {
	if len(artifacts) == 0 {
		return ""
	}
	summary := fmt.Sprintf("上游交付物摘要（共 %d 个）:\n", len(artifacts))
	for i, art := range artifacts {
		// 截取内容前 200 字符作为摘要
		content := art.Content
		if len(content) > 200 {
			content = content[:200] + "..."
		}
		summary += fmt.Sprintf("  %d. [%s v%d] %s\n", i+1, art.Type, art.Version, content)
	}
	return summary
}
