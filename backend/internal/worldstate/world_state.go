package worldstate

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// ─── WorldStateSection 接口 ────────────────────────────────
//
// 借鉴 OpenAI Codex 的 WorldState 设计：
//   - 每个上下文片段知道如何做 diff（Snapshot + RenderDiff）
//   - 只有变化的 section 才需要推送给 LLM
//   - 支持 Known（有前序状态，做 diff）vs Unknown（首次推送，全量）
//
// 这解决了全量拼装 system prompt 导致 Token 暴涨的问题。
// 适用于 LuminBuddy V2 全平台所有 LLM 调用路径：
//   - Harness 模式（chat/writing/polish）
//   - Pipeline 模式（WriteStep/ChatStep/CompressStep）
//   - 评测模式（EvaluationService）
//   - Admin LLM 调用（StyleBuilder/MCP llm_complete）

// WorldStateSection 是一个上下文片段的抽象接口。
// 每个 section 负责生成自己的快照和增量 diff。
type WorldStateSection interface {
	// ID 返回 section 的唯一标识符。
	ID() string

	// Snapshot 返回当前状态的快照（用于后续 diff 比较的基线）。
	Snapshot() interface{}

	// RenderDiff 渲染与 previous 之间的差异。
	// 如果 previous 为 nil，则全量渲染。
	// 如果无变化，返回 nil。
	RenderDiff(previous interface{}) *ContextFragment
}

// ContextFragment 是增量推送给模型的上下文片段。
type ContextFragment struct {
	Role    string          `json:"role"`     // "system" / "developer" / "user"
	Body    string          `json:"body"`     // 推送给模型的内容
	Markers *ContextMarker `json:"markers"`  // 可选的上下文标记
}

// ContextMarker 用于在模型上下文中标记边界（借鉴 Codex 的 context_window 标记）。
type ContextMarker struct {
	Open  string `json:"open"`  // 如 "<context_section>"
	Close string `json:"close"` // 如 "</context_section>"
}

// PreviousSectionState 描述前一次推送时该 section 的状态。
type PreviousSectionState int

const (
	// SectionAbsent: section 不存在于上一轮（首次推送，全量渲染）。
	SectionAbsent PreviousSectionState = iota
	// SectionKnown: section 存在于上一轮（可以做 diff）。
	SectionKnown
)

// ─── WorldState 管理器 ────────────────────────────────────

// WorldState 管理所有 section 的基线快照和增量推送。
// 线程安全，可被多 goroutine 并发读取。
type WorldState struct {
	mu        sync.RWMutex
	sections  map[string]WorldStateSection
	baselines map[string]interface{} // 上一轮的快照
	version   uint64                 // 历史版本号，每次更新递增
}

// NewWorldState 创建一个新的 WorldState 管理器。
func NewWorldState() *WorldState {
	return &WorldState{
		sections:  make(map[string]WorldStateSection),
		baselines: make(map[string]interface{}),
	}
}

// Register 注册一个 section。
// 如果 section ID 已存在，会覆盖旧的。
func (w *WorldState) Register(section WorldStateSection) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.sections[section.ID()] = section
}

// Unregister 移除一个 section。
func (w *WorldState) Unregister(id string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.sections, id)
	delete(w.baselines, id)
}

// UpdateWorldState 遍历所有 section，只推送变化部分。
// 返回增量 ContextFragment 列表。
// 调用后，所有 section 的基线会更新为当前快照。
func (w *WorldState) UpdateWorldState() []ContextFragment {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 按 ID 排序确保输出稳定
	ids := make([]string, 0, len(w.sections))
	for id := range w.sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var fragments []ContextFragment
	for _, id := range ids {
		section := w.sections[id]
		snapshot := section.Snapshot()

		var previous interface{}
		hasPrev := false
		if prev, ok := w.baselines[id]; ok {
			previous = prev
			hasPrev = true
		}

		fragment := section.RenderDiff(previous)
		if fragment != nil {
			fragments = append(fragments, *fragment)
		}

		// 更新基线
		w.baselines[id] = snapshot

		if !hasPrev && fragment != nil {
			slog.Debug("world_state: section first push",
				"section", id,
				"body_len", len(fragment.Body),
			)
		} else if fragment != nil {
			slog.Debug("world_state: section diff pushed",
				"section", id,
				"body_len", len(fragment.Body),
			)
		}
	}

	w.version++
	return fragments
}

// RenderFullPrompt 渲染所有 section 的全量内容（用于首次调用或无法做 diff 的场景）。
// 返回拼接后的完整 system prompt。
func (w *WorldState) RenderFullPrompt() string {
	w.mu.RLock()
	defer w.mu.RUnlock()

	// 按 ID 排序
	ids := make([]string, 0, len(w.sections))
	for id := range w.sections {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	var sb strings.Builder
	for _, id := range ids {
		section := w.sections[id]
		fragment := section.RenderDiff(nil) // nil = 全量渲染
		if fragment != nil && fragment.Body != "" {
			sb.WriteString(fragment.Body)
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// Version 返回当前历史版本号。
func (w *WorldState) Version() uint64 {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.version
}

// ResetBaselines 清除所有基线，强制下次全量推送。
// 用于 compaction/rollback 等需要重置上下文的场景。
func (w *WorldState) ResetBaselines() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.baselines = make(map[string]interface{})
	w.version++
}

// ─── Token 预算追踪 ───────────────────────────────────────

// TokenBudget 追踪单个 Agent / 会话的 Token 使用量。
// 借鉴 Codex 的 TokenBudgetContext + TokenBudgetRemainingContext。
type TokenBudget struct {
	ContextWindowID string // UUID，标识当前上下文窗口
	TotalBudget     int64  // 总 Token 预算（0 = 无限）
	Used            int64  // 已使用 Token 数
}

// Remaining 返回剩余 Token 数。
func (b *TokenBudget) Remaining() int64 {
	if b.TotalBudget == 0 {
		return -1 // 无限
	}
	r := b.TotalBudget - b.Used
	if r < 0 {
		return 0
	}
	return r
}

// Consume 消耗指定数量的 Token。返回是否仍在预算内。
func (b *TokenBudget) Consume(tokens int64) bool {
	b.Used += tokens
	if b.TotalBudget == 0 {
		return true
	}
	return b.Used < b.TotalBudget
}

// IsLow 返回剩余 Token 是否低于阈值。
func (b *TokenBudget) IsLow(threshold int64) bool {
	if b.TotalBudget == 0 {
		return false
	}
	return b.Remaining() < threshold
}

// ─── AutoCompactFallback ──────────────────────────────────

// AutoCompactFallback 是 Codex 的自动压缩降级机制。
// 当 Token 预算不足时，自动触发对话历史压缩。
type AutoCompactFallback struct {
	Threshold int64 // 触发阈值（剩余 Token 低于此值时触发）
}

// DefaultAutoCompactThreshold 默认触发阈值。
const DefaultAutoCompactThreshold = 2000

// NewAutoCompactFallback 创建默认配置的自动压缩降级。
func NewAutoCompactFallback() *AutoCompactFallback {
	return &AutoCompactFallback{
		Threshold: DefaultAutoCompactThreshold,
	}
}

// ShouldCompact 检查是否需要触发压缩。
func (a *AutoCompactFallback) ShouldCompact(budget *TokenBudget) bool {
	if budget == nil || budget.TotalBudget == 0 {
		return false
	}
	return budget.IsLow(a.Threshold)
}

// CompactPrompt 返回压缩降级提示语（注入给 LLM，告知其历史已被压缩）。
func (a *AutoCompactFallback) CompactPrompt(savedTokens int64) string {
	return fmt.Sprintf("[系统提示] 对话历史已自动压缩，节省了约 %d tokens。后续回复请基于压缩后的摘要继续。", savedTokens)
}
