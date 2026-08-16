package server

import (
	"context"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	memsvc "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	pkgmem "github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// ─── Harness 适配器 ──────────────────────────────────────────
//
// harnessRunner 适配 agent.Runner 接口，将 Harness.Run 包装为
// Run(ctx, execCtx) error 签名。
// WritingSession 通过闭包传递，在 Run 时使用。

type harnessRunner struct {
	harness *agent.Harness
	session *agent.WritingSession
}

func (r *harnessRunner) Run(ctx context.Context, execCtx *engine.ExecutionContext) error {
	return r.harness.Run(ctx, execCtx, r.session)
}

// ─── harnessSessionStore 适配 SessionStore 接口 ────────────
//
// 将 internal/memory.Service 适配为 agent.SessionStore。
// SessionStore 接口使用 pkg/memory.ConversationMessage，
// 而 internal/memory.Service 也使用同一类型，所以直接转发。
// 如果 memorySvc 为 nil 或不可用，所有方法都是 no-op。

type harnessSessionStore struct {
	svc *memsvc.Service
}

func (s *harnessSessionStore) LoadHistory(ctx context.Context, conversationID string, limit int) ([]pkgmem.ConversationMessage, error) {
	if s.svc == nil || !s.svc.IsAvailable() {
		return nil, nil
	}
	return s.svc.LoadHistory(ctx, conversationID, limit)
}

func (s *harnessSessionStore) StoreMessage(ctx context.Context, msg *pkgmem.ConversationMessage) error {
	if s.svc == nil || !s.svc.IsAvailable() {
		return nil
	}
	return s.svc.StoreMessage(ctx, msg)
}

func (s *harnessSessionStore) IsEnabledForUser(userID string) bool {
	if s.svc == nil || !s.svc.IsAvailable() {
		return false
	}
	return s.svc.IsEnabledForUser(userID)
}

// Retrieve 实现 agent.MemoryRetriever 接口，用于 Harness 的主动记忆检索。
func (s *harnessSessionStore) Retrieve(ctx context.Context, req pkgmem.RetrieveRequest) (*pkgmem.MemoryContext, error) {
	if s.svc == nil || !s.svc.IsAvailable() {
		return nil, nil
	}
	return s.svc.Retrieve(ctx, req)
}
