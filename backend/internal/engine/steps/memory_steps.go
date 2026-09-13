package steps

import (
	"context"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
)

// ─── MemoryGateStep ──────────────────────────────────────

// MemoryGateStep 在 IntentStep 之后执行：
//  - 经 memoryport.Port 预取并门控用户记忆
//  - 将 Bundle 注入 ExecutionContext（长期记忆注入的唯一入口）
//  - 通过 emitter 推送 memory.used 事件
//
// 记忆检索/门控/实体画像的实现细节全部在 Port 适配器后面（anti-corruption
// layer），本步骤只消费契约 DTO。
type MemoryGateStep struct {
	port            memoryport.Port
	withEntityGraph bool
}

// NewMemoryGateStep 创建不带实体画像检索的门控步骤。
func NewMemoryGateStep(port memoryport.Port) *MemoryGateStep {
	return &MemoryGateStep{port: port}
}

// NewMemoryGateStepWithEntityGraph 创建带实体画像检索的门控步骤
// （要求 Port 的 Capabilities.EntityGraph 可用）。
func NewMemoryGateStepWithEntityGraph(port memoryport.Port) *MemoryGateStep {
	return &MemoryGateStep{port: port, withEntityGraph: true}
}

func (s *MemoryGateStep) Name() engine.StepName { return engine.StepMemoryGate }
func (s *MemoryGateStep) CanPause() bool         { return false }
func (s *MemoryGateStep) Timeout() time.Duration { return 30 * time.Second }
func (s *MemoryGateStep) Critical() bool         { return false }

// ShouldSkip returns true for anonymous/guest users or when the port is nil.
// Memory retrieval runs for ALL intents (including chat) so that
// ChatStep, WriteStep, and PostReviewStep can all consume the Bundle.
// Guest users ("anonymous") don't have valid UUIDs, so skip to avoid DB errors.
func (s *MemoryGateStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.UserID == "" || execCtx.UserID == "anonymous" {
		return true
	}
	return false
}

func (s *MemoryGateStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.port == nil {
		return nil // Memory port not available, skip
	}

	// 灰度门控 + 服务可用性
	if !s.port.EnabledForUser(execCtx.UserID) {
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

	bundle, err := s.port.PrepareInjection(ctx, memoryport.Request{
		UserID:          execCtx.UserID,
		Query:           execCtx.UserInput,
		Intent:          intent,
		Explicit:        explicit,
		SessionID:       execCtx.SessionID,
		WithEntityGraph: s.withEntityGraph,
	})
	if err != nil {
		slog.Warn("memory gate: retrieve failed", "error", err)
		return nil // Non-fatal
	}

	if bundle == nil {
		return nil
	}

	// Store in execution context
	execCtx.MemoryContext = bundle

	// Emit memory.used event via emitter
	if len(bundle.WriteDirectives) > 0 || len(bundle.ReviewGuard) > 0 {
		if wsEmitter, ok := emitter.(interface {
			EmitMemoryUsed(traceID string, bundle *memoryport.Bundle)
		}); ok {
			wsEmitter.EmitMemoryUsed(execCtx.TraceID, bundle)
		}
	}

	slog.Info("memory gate: completed",
		"trace_id", execCtx.TraceID,
		"injected", len(bundle.WriteDirectives),
		"review_guard", len(bundle.ReviewGuard),
		"entity_profile", bundle.EntityProfile != "",
	)

	return nil
}

// ─── MemoryExtractStep ───────────────────────────────────

// MemoryExtractStep 在 AutoFixStep 之后执行（SDK 内部异步，不阻塞写作流程）：
// 经 memoryport.Port.SubmitOutcome 提交提取输入（终稿 + 确定性字段）。
// 浓信号（改前稿/对话）由 P2 在 Outcome 上加性启用。
type MemoryExtractStep struct {
	port memoryport.Port
}

// NewMemoryExtractStep 创建提取步骤。
func NewMemoryExtractStep(port memoryport.Port) *MemoryExtractStep {
	return &MemoryExtractStep{port: port}
}

func (s *MemoryExtractStep) Name() engine.StepName { return engine.StepMemoryExtract }
func (s *MemoryExtractStep) CanPause() bool         { return false }
func (s *MemoryExtractStep) Timeout() time.Duration { return 60 * time.Second }
func (s *MemoryExtractStep) Critical() bool         { return false }

// ShouldSkip returns true for chat intent or anonymous/guest users.
// Guest users ("anonymous") don't have valid UUIDs, so skip to avoid DB errors.
func (s *MemoryExtractStep) ShouldSkip(execCtx *engine.ExecutionContext) bool {
	if execCtx.UserID == "" || execCtx.UserID == "anonymous" {
		return true
	}
	return execCtx.TaskIntent != nil && execCtx.TaskIntent.TaskMode == "chat"
}

func (s *MemoryExtractStep) Execute(ctx context.Context, execCtx *engine.ExecutionContext, emitter engine.EventEmitter) error {
	if s.port == nil {
		return nil
	}

	// 灰度门控
	if !s.port.EnabledForUser(execCtx.UserID) {
		return nil
	}

	// Submit asynchronously (non-blocking: adapter → SDK handles async)
	return s.port.SubmitOutcome(ctx, memoryport.Outcome{
		Kind:      memoryport.OutcomeWrite,
		UserID:    execCtx.UserID,
		TraceID:   execCtx.TraceID,
		Article:   execCtx.Article,
		StyleSlug: execCtx.StyleSlug,
		Mode:      execCtx.Mode,
		WordLimit: execCtx.WordLimit,
	})
}
