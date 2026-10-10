package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"log/slog"
	"sync"
	"time"
)

// servicePolicyExecutor reloads operator intent per node. It keeps only the
// current revision's executors, bounding memory and retaining its shadow circuit.
type servicePolicyExecutor struct {
	baseline writingruntime.Executor
	spec     governedCapabilitySpec
	deps     governedRuntimeDependencies
	store    *writingstore.Store
	mode     writingruntime.RuntimeMode
	mu       sync.Mutex
	revision int64
	cached   writingruntime.Executor
}

func (e *servicePolicyExecutor) Descriptor() writingruntime.ExecutorDescriptor {
	return e.baseline.Descriptor()
}
func (e *servicePolicyExecutor) Execute(ctx context.Context, req writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	record, err := e.store.LatestRuntimePolicy(ctx, e.spec.CapabilityID)
	if errors.Is(err, writingstore.ErrNotFound) {
		// 空策略表 = 默认 shadow（task12 语义）。必须清 err：否则缓存命中
		// 的 dispatch（revision 未变、跳过 build）会带着这个陈旧的
		// ErrNotFound 走进 fallback —— 每个非首次 dispatch 都被强制成
		// mode=off 离线 baseline，真实 LLM 节点必败，运行暂停。
		policy := writingruntime.DefaultShadowPolicy(e.spec.CandidateID, writingruntime.AdapterFamilyEngine, e.spec.CapabilityID, e.spec.CapabilityVersion)
		record.Policy, _ = json.Marshal(policy)
		err = nil
	} else if err != nil {
		// runtime-agility 观测修复：fallback 强制 mode=off（离线 baseline，
		// 真实 LLM 节点必败 → 运行暂停），此前底层错误被静默吞掉，根因
		// 不可见。现在必须留痕。
		slog.Error("runtime policy load failed — forcing mode=off baseline (run will pause)",
			"capability", e.spec.CapabilityID, "error", err)
		return e.fallback(ctx, req, "policy_unavailable")
	}
	e.mu.Lock()
	buildRan := e.cached == nil || e.revision != record.Revision
	if buildRan {
		e.cached, err = e.build(record)
		e.revision = record.Revision
	}
	executor := e.cached
	e.mu.Unlock()
	if err != nil || executor == nil {
		// runtime-agility 观测：三分歧因——load 失败 / build 失败 / 缓存
		// 命中时的陈旧 err（ErrNotFound 分支未清 err，缓存命中跳过 build
		// 时 err 仍是上层的 not found——这是要定位的真 bug）。
		slog.Error("runtime policy fallback triggered (forcing mode=off baseline, run will pause)",
			"capability", e.spec.CapabilityID, "buildRan", buildRan,
			"cachedNil", executor == nil, "error", err)
		return e.fallback(ctx, req, "policy_invalid")
	}
	return executor.Execute(ctx, req)
}
func (e *servicePolicyExecutor) fallback(ctx context.Context, req writingruntime.ExecutionRequest, reason string) (writingruntime.ExecutionResult, error) {
	if e.deps.telemetry != nil {
		e.deps.telemetry.Observe(ctx, writingruntime.RuntimeMetric{Kind: writingruntime.MetricRouteDecision, Capability: req.Node.Capability, Mode: writingruntime.RolloutOff, Lane: writingruntime.LaneBaseline, Status: "policy_failed", Reason: reason})
	}
	// Reuse the lane accounting path even on a missing/unreadable policy.
	p := writingruntime.DefaultShadowPolicy(e.spec.CandidateID, writingruntime.AdapterFamilyEngine, e.spec.CapabilityID, e.spec.CapabilityVersion)
	p.Mode = writingruntime.RolloutOff
	p, _ = p.WithComputedHash()
	b, _ := json.Marshal(p)
	x, err := e.build(writingstore.RuntimePolicyRevision{Policy: b})
	if err != nil {
		return writingruntime.ExecutionResult{}, err
	}
	return x.Execute(ctx, req)
}
func (e *servicePolicyExecutor) build(record writingstore.RuntimePolicyRevision) (writingruntime.Executor, error) {
	var p writingruntime.AdapterRolloutPolicy
	if err := json.Unmarshal(record.Policy, &p); err != nil {
		return nil, err
	}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	if p.CapabilityID != e.spec.CapabilityID || p.CapabilityVersion != e.spec.CapabilityVersion || p.ExecutorID != e.spec.CandidateID || p.Family != writingruntime.AdapterFamilyEngine || (record.PolicyHash != "" && record.PolicyHash != p.PolicyHash) {
		return nil, fmt.Errorf("runtime policy binding mismatch")
	}
	// P0 explicitly serves allowlist only; later rungs remain fail-closed.
	if p.Mode == writingruntime.RolloutPercentage || p.Mode == writingruntime.RolloutEnabled {
		return nil, fmt.Errorf("runtime stage unavailable")
	}
	provider, err := writingruntime.NewMutableRolloutPolicyProvider(p)
	if err != nil {
		return nil, err
	}
	gateway, err := writingruntime.NewShadowContentGateway(e.deps.canonical, e.deps.sink, p)
	if err != nil {
		return nil, err
	}
	desc := writingruntime.ExecutorDescriptor{ExecutorID: e.spec.CandidateID, Version: "1", SupportedNodeKinds: e.baseline.Descriptor().SupportedNodeKinds}
	isolated, err := writingruntime.NewShadowIsolatedExecutorAdapter(writingruntime.AdapterFamilyEngine, desc, e.spec.CapabilityID, e.spec.CapabilityVersion, e.spec.Permissions, gateway, e.spec.Runner)
	if err != nil {
		return nil, err
	}
	shadow, err := writingruntime.NewObservationRolloutExecutor(e.baseline, isolated, provider, e.deps.evidence, e.deps.telemetry)
	if err != nil {
		return nil, err
	}
	if e.mode != writingruntime.RuntimeModeAllowlist || !record.Active || p.Mode != writingruntime.RolloutAllowlist {
		return shadow, nil
	}
	candidate, err := writingruntime.NewLegacyExecutorAdapter(writingruntime.AdapterFamilyEngine, desc, e.spec.CapabilityID, e.spec.CapabilityVersion, e.spec.Permissions, e.deps.canonical, e.spec.Runner)
	if err != nil {
		return nil, err
	}
	gated := writingruntime.GatedRolloutPolicyProvider{Base: provider, Gate: writingruntime.AllowlistPromotionGate{Store: e.store}, Evidence: e.deps.evidence}
	authoritative, err := writingruntime.NewRolloutExecutor(e.baseline, candidate, gated, e.deps.evidence, e.deps.telemetry)
	if err != nil {
		return nil, err
	}
	return policyLanes{policy: p, shadow: shadow, authoritative: authoritative, descriptor: e.Descriptor()}, nil
}

type policyLanes struct {
	policy                writingruntime.AdapterRolloutPolicy
	shadow, authoritative writingruntime.Executor
	descriptor            writingruntime.ExecutorDescriptor
}

func (e policyLanes) Descriptor() writingruntime.ExecutorDescriptor { return e.descriptor }
func (e policyLanes) Execute(ctx context.Context, req writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	decision, err := writingruntime.DecideRoute(e.policy, req, time.Now().UTC())
	if err != nil {
		return writingruntime.ExecutionResult{}, err
	}
	if decision.RunShadow {
		return e.shadow.Execute(ctx, req)
	}
	return e.authoritative.Execute(ctx, req)
}
