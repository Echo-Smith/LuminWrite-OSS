package server

import (
	"context"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/agent"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
	memportadapter "github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport/adapter"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
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
	svc  *memsvc.Service
	port memoryport.Port
}

// newHarnessSessionStore 装配 harness 会话存储与其记忆检索 Port。
// harness 检索不含实体画像（与旧行为一致），embedder 传 nil。
func newHarnessSessionStore(svc *memsvc.Service) *harnessSessionStore {
	return &harnessSessionStore{
		svc:  svc,
		port: memportadapter.NewServiceAdapter(svc, nil),
	}
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
	if err := s.svc.StoreMessage(ctx, msg); err != nil {
		return err
	}
	// P1-5: 异步补 embedding，与 Pipeline 写历史的质量对齐。
	// 成本控制：只 embed 非 article、≤2000 字的消息（计划 P1-5）。
	if msg.Embedding == nil && msg.ContentType != "article" &&
		msg.ID != "" && len([]rune(msg.Content)) <= 2000 {
		go func(id, content string) {
			embCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			vec, err := s.svc.Embed(embCtx, content)
			if err != nil || len(vec) == 0 {
				return
			}
			_ = s.svc.UpdateMessageEmbedding(embCtx, id, vec)
		}(msg.ID, msg.Content)
	}
	return nil
}

func (s *harnessSessionStore) IsEnabledForUser(userID string) bool {
	if s.svc == nil || !s.svc.IsAvailable() {
		return false
	}
	return s.svc.IsEnabledForUser(userID)
}

// Retrieve 实现 agent.MemoryRetriever 接口，用于 Harness 的主动记忆检索。
func (s *harnessSessionStore) Retrieve(ctx context.Context, req memoryport.Request) (*memoryport.Bundle, error) {
	if s.port == nil {
		return nil, nil
	}
	return s.port.PrepareInjection(ctx, req)
}
