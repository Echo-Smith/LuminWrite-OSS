package steps

import (
	"context"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── MemoryGateStep ──────────────────────────────────────

// MemoryGateStep 在 IntentStep 之后执行：
//  - 检索用户记忆
//  - 场景门控（过滤掉用户已显式指定的维度）
//  - 将 MemoryContext 注入 ExecutionContext
//  - 通过 emitter 推送 memory.used 事件
type MemoryGateStep struct {
	svc memoryServiceAdapter
}

// memoryServiceAdapter 是对 internal/memory.Service 的接口抽象
// 避免循环依赖：steps 不直接依赖 internal/memory
type memoryServiceAdapter interface {
	Retrieve(ctx context.Context, req memory.RetrieveRequest) (*memory.MemoryContext, error)
	IsEnabledForUser(userID string) bool
}

func NewMemoryGateStep(svc memoryServiceAdapter) *MemoryGateStep {
	return &MemoryGateStep{svc: svc}
}

func (s *MemoryGateStep) Name() engine.StepName { return engine.StepMemoryGate }
func (s *MemoryGateStep) CanPause() bool         { return false }

// ShouldSkip returns true only when the memory service is nil.
// Memory retrieval runs for ALL intents (including chat) so that
// ChatStep, WriteStep, and PostReviewStep can all consume MemoryContext.
func (s *MemoryGateStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return false
}

func (s *MemoryGateStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.svc == nil {
		return nil // Memory service not available, skip
	}

	// 灰度门控：检查该用户是否启用记忆
	if !s.svc.IsEnabledForUser(execCtx.UserID) {
		return nil
	}

	// Build explicit dimensions from execution context
	explicit := map[string]any{}
	if execCtx.WordLimit > 0 {
		explicit["word_limit"] = execCtx.WordLimit
	}
	if execCtx.StyleSlug != "" {
		explicit["style"] = execCtx.StyleSlug
	}
	if execCtx.Mode != "" {
		explicit["mode"] = execCtx.Mode
	}
	if execCtx.UserInput != "" {
		explicit["message"] = execCtx.UserInput
	}

	// Determine intent
	intent := "writing"
	if execCtx.TaskIntent != nil {
		intent = execCtx.TaskIntent.TaskMode
	}

	req := memory.RetrieveRequest{
		UserID:    execCtx.UserID,
		UserInput: execCtx.UserInput,
		Intent:    intent,
		Explicit:  explicit,
		SessionID: execCtx.SessionID,
	}

	memCtx, err := s.svc.Retrieve(ctx, req)
	if err != nil {
		slog.Warn("memory gate: retrieve failed", "error", err)
		return nil // Non-fatal
	}

	if memCtx == nil {
		return nil
	}

	// Store in execution context
	execCtx.MemoryContext = memCtx

	// Emit memory.used event via emitter
	if len(memCtx.Injected) > 0 || len(memCtx.ReviewGuard) > 0 {
		if wsEmitter, ok := emitter.(interface {
			EmitMemoryUsed(traceID string, memCtx *memory.MemoryContext)
		}); ok {
			wsEmitter.EmitMemoryUsed(execCtx.TraceID, memCtx)
		}
	}

	slog.Info("memory gate: completed",
		"trace_id", execCtx.TraceID,
		"injected", len(memCtx.Injected),
		"review_guard", len(memCtx.ReviewGuard),
	)

	return nil
}

// ─── MemoryExtractStep ───────────────────────────────────

// MemoryExtractStep 在 AutoFixStep 之后异步执行：
//  - 从文章 + 反馈中提取记忆
//  - 确定性提取 + LLM 提取
//  - 不阻塞写作流程
type MemoryExtractStep struct {
	svc memoryExtractAdapter
}

// memoryExtractAdapter 提取接口
type memoryExtractAdapter interface {
	Extract(ctx context.Context, session memory.ExtractSession)
	IsEnabledForUser(userID string) bool
}

func NewMemoryExtractStep(svc memoryExtractAdapter) *MemoryExtractStep {
	return &MemoryExtractStep{svc: svc}
}

func (s *MemoryExtractStep) Name() engine.StepName { return engine.StepMemoryExtract }
func (s *MemoryExtractStep) CanPause() bool         { return false }

// ShouldSkip returns true for chat intent — no writing patterns to extract from chat.
func (s *MemoryExtractStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *MemoryExtractStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.svc == nil {
		return nil
	}

	// 灰度门控
	if !s.svc.IsEnabledForUser(execCtx.UserID) {
		return nil
	}

	// Build extract session
	session := memory.ExtractSession{
		UserID:    execCtx.UserID,
		TraceID:   execCtx.TraceID,
		Article:   execCtx.Article,
		StyleSlug: execCtx.StyleSlug,
		Mode:      execCtx.Mode,
		WordLimit: execCtx.WordLimit,
	}

	// Extract asynchronously (non-blocking)
	s.svc.Extract(ctx, session)

	return nil
}

// ─── Helpers for WriteStep to consume memory ─────────────

// FormatMemoryForPrompt 将注入记忆格式化为 prompt 文本
func FormatMemoryForPrompt(memCtx *memory.MemoryContext) string {
	if memCtx == nil || len(memCtx.Injected) == 0 {
		return ""
	}

	var sb []byte
	sb = append(sb, "\n\n--- 用户写作偏好（请参考但不强制）---\n"...)
	for _, entry := range memCtx.Injected {
		sb = append(sb, "- "...)
		sb = append(sb, entry.Value...)
		sb = append(sb, '\n')
	}
	return string(sb)
}

// FormatReviewGuardForPrompt 将反馈记忆格式化为审查标准
func FormatReviewGuardForPrompt(memCtx *memory.MemoryContext) string {
	if memCtx == nil || len(memCtx.ReviewGuard) == 0 {
		return ""
	}

	var sb []byte
	sb = append(sb, "\n\n--- 用户历史反馈（审查时请检查）---\n"...)
	for _, entry := range memCtx.ReviewGuard {
		sb = append(sb, "- "...)
		sb = append(sb, entry.Value...)
		sb = append(sb, '\n')
	}
	return string(sb)
}
