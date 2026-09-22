// Package adapter 是 memoryport 契约与 pkg/memory SDK 之间的跨界适配器。
// 本包是全仓唯一允许同时 import internal/memory 与 pkg/memory 并把
// SDK 类型翻译为契约 DTO 的地方（计划 §4.2 纪律 1）。
package adapter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memory"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
	pkgmem "github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/memory"
)

// Embedder 实体画像检索所需的向量化接口。
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// ServiceAdapter 把 internal/memory.Service 适配为 memoryport.Port。
type ServiceAdapter struct {
	svc      *memory.Service
	embedder Embedder
}

// 编译期确认 Port 契约。
var _ memoryport.Port = (*ServiceAdapter)(nil)

// NewServiceAdapter 创建适配器。embedder 为 nil 时 Capabilities.EntityGraph=false，
// 对应旧 NewMemoryGateStep（无实体图）路径。
func NewServiceAdapter(svc *memory.Service, embedder Embedder) *ServiceAdapter {
	return &ServiceAdapter{svc: svc, embedder: embedder}
}

// EnabledForUser 灰度门控 + 服务可用性。
func (a *ServiceAdapter) EnabledForUser(userID string) bool {
	return a.svc != nil && a.svc.IsAvailable() && a.svc.IsEnabledForUser(userID)
}

// Capabilities 能力协商。
func (a *ServiceAdapter) Capabilities() memoryport.Capabilities {
	return memoryport.Capabilities{
		EntityGraph:     a.embedder != nil,
		ExplicitCapture: true, // P1-3：SDK.Create 已补 PII 检查后启用
	}
}

// PrepareInjection 批量预取 + 门控 + 可选实体画像。
func (a *ServiceAdapter) PrepareInjection(ctx context.Context, req memoryport.Request) (*memoryport.Bundle, error) {
	if a.svc == nil || !a.svc.IsAvailable() {
		return nil, nil
	}

	memCtx, err := a.svc.Retrieve(ctx, pkgmem.RetrieveRequest{
		UserID:    req.UserID,
		UserInput: req.Query,
		Intent:    req.Intent,
		Explicit:  req.Explicit,
		SessionID: req.SessionID,
		TraceID:   req.TraceID,
		Source:    req.Source,
	})
	if err != nil {
		return nil, err
	}
	if memCtx == nil {
		return nil, nil
	}

	bundle := bundleFromMemoryContext(memCtx)

	// ── 实体画像网络（行为与旧 MemoryGateStep 一致）──
	if req.WithEntityGraph && a.embedder != nil {
		queryVector, embErr := a.embedder.Embed(ctx, req.Query)
		if embErr != nil || len(queryVector) == 0 {
			if embErr != nil {
				slog.Warn("memoryport: entity embed failed", "error", embErr)
			}
		} else {
			entityResult, entErr := a.svc.RetrieveEntityGraph(ctx, pkgmem.EntityGraphQuery{
				UserID:       req.UserID,
				QueryText:    req.Query,
				QueryVector:  queryVector,
				MaxHops:      2,
				MaxEntities:  10,
				MinRelevance: 0.3,
			})
			if entErr != nil {
				slog.Warn("memoryport: entity graph retrieval failed", "error", entErr)
			} else if entityResult != nil && len(entityResult.Entities) > 0 {
				bundle.EntityProfile = entityResult.FormattedContext
			}
		}
	}

	return bundle, nil
}

// QueryOnDemand 真检索 + 真门控，按 ExcludeIDs 去重后返回增量。
func (a *ServiceAdapter) QueryOnDemand(ctx context.Context, req memoryport.QueryRequest) ([]memoryport.Directive, error) {
	if a.svc == nil || !a.svc.IsAvailable() {
		return nil, nil
	}

	memCtx, err := a.svc.Retrieve(ctx, pkgmem.RetrieveRequest{
		UserID:    req.UserID,
		UserInput: req.Query,
		Intent:    req.Intent,
		SessionID: req.SessionID,
		TraceID:   req.TraceID,
		Source:    req.Source,
	})
	if err != nil || memCtx == nil {
		return nil, err
	}

	excluded := make(map[string]bool, len(req.ExcludeIDs))
	for _, id := range req.ExcludeIDs {
		excluded[id] = true
	}

	var out []memoryport.Directive
	for _, pool := range [][]pkgmem.MemoryEntry{memCtx.Injected, memCtx.ReviewGuard} {
		for _, e := range pool {
			if excluded[e.ID] {
				continue
			}
			out = append(out, directiveFromEntry(e, kindForTier(e.Tier)))
		}
	}
	return out, nil
}

// SubmitOutcome 写回入口。SDK.Extract 内部异步，此处不额外起 goroutine，
// 保持与旧 MemoryExtractStep 相同的非阻塞语义。
func (a *ServiceAdapter) SubmitOutcome(ctx context.Context, out memoryport.Outcome) error {
	if a.svc == nil || !a.svc.IsAvailable() {
		return nil
	}

	switch out.Kind {
	case memoryport.OutcomeExplicit:
		return a.createExplicit(ctx, out)
	default: // memoryport.OutcomeWrite
		session := pkgmem.ExtractSession{
			UserID:         out.UserID,
			TraceID:        out.TraceID,
			Article:        out.Article,
			StyleSlug:      out.StyleSlug,
			Mode:           out.Mode,
			WordLimit:      out.WordLimit,
			BeforeRevision: out.BeforeRevision, // P2-1 浓信号：diff 对比提取
		}
		for _, f := range out.Feedbacks {
			session.Feedback = append(session.Feedback, pkgmem.FeedbackInfo{
				SegmentType: f.SegmentType,
				Rating:      f.Rating,
				Comment:     f.Comment,
			})
		}
		// 浓信号字段（BeforeRevision/Transcript）契约 v1 预留，此处暂忽略。
		a.svc.Extract(ctx, session)
		return nil
	}
}

// ─── SDK → 契约翻译 ─────────────────────────────────────────

// createExplicit 显式记忆（remember 工具）落 Tier1 硬偏好。
// key 用内容指纹：精确重复会走 Service.Create 的 supersede 去重，
// 换述内容各自成行（读端去重由 P2-2 统一处理）。
func (a *ServiceAdapter) createExplicit(ctx context.Context, out memoryport.Outcome) error {
	value := strings.TrimSpace(out.ExplicitValue)
	if value == "" {
		return fmt.Errorf("empty explicit value")
	}
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(value)))[:12]
	_, err := a.svc.Create(ctx, out.UserID, "explicit", key, value)
	if err != nil {
		slog.Warn("memoryport: explicit capture failed", "error", err, "trace_id", out.TraceID)
		return err
	}
	slog.Info("memoryport: explicit memory captured", "trace_id", out.TraceID)
	return nil
}

func bundleFromMemoryContext(m *pkgmem.MemoryContext) *memoryport.Bundle {
	b := &memoryport.Bundle{
		Version:       memoryport.ContractVersion,
		WriteDirectives: make([]memoryport.Directive, 0, len(m.Injected)),
		ReviewGuard:     make([]memoryport.Directive, 0, len(m.ReviewGuard)),
		RefusalReason:   m.RefusalReason,
		Dismissed:       m.Dismissed,
	}
	for _, e := range m.Injected {
		b.WriteDirectives = append(b.WriteDirectives, directiveFromEntry(e, memoryport.KindPreference))
	}
	for _, e := range m.ReviewGuard {
		b.ReviewGuard = append(b.ReviewGuard, directiveFromEntry(e, memoryport.KindFeedback))
	}
	return b
}

func directiveFromEntry(e pkgmem.MemoryEntry, kind memoryport.DirectiveKind) memoryport.Directive {
	return memoryport.Directive{
		ID:             e.ID,
		Tier:           string(e.Tier),
		Category:       e.Category,
		Value:          e.Value,
		Confidence:     e.Confidence,
		Dismissible:    e.Dismissible,
		EvidenceStatus: string(e.EvidenceStatus),
		Kind:           kind,
		// Strength = 衰减后有效置信度（时序信号），供消费端预算截断排序
		Strength: e.Confidence,
	}
}

func kindForTier(tier pkgmem.Tier) memoryport.DirectiveKind {
	if tier == pkgmem.TierFeedback {
		return memoryport.KindFeedback
	}
	return memoryport.KindPreference
}
