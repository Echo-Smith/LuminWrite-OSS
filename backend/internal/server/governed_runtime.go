// V3.0 M0b-1 (docs/20 §20.2): the composition spine that mounts the governed
// writing runtime (Orchestrator + shadow RolloutExecutor + StoreContextSource
// + ContextRuntime) behind an explicit runtime mode. This file builds the
// runtime from injected pieces; it does NOT yet mount it on the live route or
// supply the real per-capability engine-step runners — those are M0b-2. Until
// then the default mode is off and nothing here runs in the serving path, so
// behavior is byte-identical to pre-V3.0.
package server

import (
	"context"
	"fmt"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// governedCapabilitySpec is one capability's wiring into the governed runtime:
// the executor binding ids, the capability identity, and the engine-step
// runner that executes its nodes. M0b-2 supplies these for the five built-in
// writing capabilities; the factory itself is capability-agnostic.
type governedCapabilitySpec struct {
	BindingID         string // baseline executor id (matches the manifest's Executor)
	CandidateID       string // shadow candidate executor id
	CapabilityID      string
	CapabilityVersion string
	Inputs            []writingplan.ArtifactType
	Outputs           []writingplan.ArtifactType
	Permissions       []writingplan.Permission
	Runner            writingruntime.LegacyNodeRunner
	// Direct, when set, replaces the LegacyExecutorAdapter baseline with a
	// typed runtime executor (the research path's design.md §3 wiring: these
	// executors consume typed artifacts and stage through the ContentGateway
	// themselves — no legacy payload collection). The candidate stays the
	// same DirectExecutor in the servicePolicyExecutor's shadow assembly.
	Direct writingruntime.Executor
}

// governedWritingRuntime is the assembled governed runtime plus the controller
// the writing API mounts. Nil means the runtime is off (mode=off).
type governedWritingRuntime struct {
	orchestrator *writingruntime.Orchestrator
	controller   writingRunController
	capabilities *writingplan.CapabilityRegistry
	mode         writingruntime.RuntimeMode
}

// governedRunController adapts the Orchestrator to the writing API's
// writingRunController: Orchestrator.Resume returns a RunOutcome, the
// controller contract returns only an error.
type governedRunController struct {
	orchestrator *writingruntime.Orchestrator
	store        *writingstore.Store
}

// Compile-time proof the adapter satisfies the writing API's controller
// contract (Orchestrator.Resume returns a RunOutcome; the contract wants error).
var _ writingRunController = governedRunController{}

func (controller governedRunController) Pause(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	return controller.orchestrator.Pause(ctx, runID, commandID, actor)
}

func (controller governedRunController) Resume(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	if controller.store == nil {
		return writingruntime.ErrRuntimeNotReady
	}
	acquired, err := controller.store.WithRunExecutionLock(ctx, runID, func(owned context.Context) error {
		stop := watchGovernedControl(owned, controller.store, controller.orchestrator, runID)
		defer stop()
		_, err := controller.orchestrator.Resume(owned, runID, commandID, actor)
		return err
	})
	if !acquired && err == nil {
		return writingstore.ErrConflict
	}
	return err
}

func (controller governedRunController) Cancel(ctx context.Context, runID, commandID string, actor writingstore.Actor) error {
	return controller.orchestrator.Cancel(ctx, runID, commandID, actor)
}

// governedRuntimeDependencies are the store-backed pieces the composition
// needs; the caller supplies them so this file stays free of storage wiring.
type governedRuntimeDependencies struct {
	canonical       writingruntime.ContentGateway
	sink            writingruntime.ShadowContentSink
	evidence        writingruntime.RolloutEvidenceStore
	transitionStore writingruntime.TransitionStore
	checkpoints     writingruntime.CheckpointRepository
	initial         writingruntime.InitialArtifactProvider
	materials       writingruntime.MaterialSnapshotRepository
	context         writingruntime.ContextSource
	telemetry       writingruntime.RuntimeTelemetry
	now             func() time.Time
}

// newGovernedWritingRuntime assembles the governed runtime for the given mode.
// mode=off returns (nil, nil): the caller leaves the writing API exactly as
// before V3.0. mode=shadow builds a shadow RolloutExecutor per capability
// (baseline authoritative, candidate isolated) and an Orchestrator wired with
// the V2.9 context source/sink/runtime so context compilation and forgetting
// actually run in the request path. mode=allowlist additionally lets the
// servicePolicyExecutor build authoritative candidate lanes for subjects on a
// persisted, approved allowlist policy (AllowlistPromotionGate); misses keep
// running shadow. Activation itself remains a separate authorized change —
// this mode only provides the mechanism, never the authorization.
func newGovernedWritingRuntime(store *writingstore.Store, mode writingruntime.RuntimeMode, deps governedRuntimeDependencies, specs []governedCapabilitySpec) (*governedWritingRuntime, error) {
	if mode == writingruntime.RuntimeModeOff {
		return nil, nil
	}
	if mode != writingruntime.RuntimeModeShadow && mode != writingruntime.RuntimeModeAllowlist {
		return nil, fmt.Errorf("governed runtime mode %q is not supported (valid modes: off, shadow, allowlist)", mode)
	}
	if store == nil || deps.canonical == nil || deps.sink == nil || deps.evidence == nil ||
		deps.transitionStore == nil || deps.checkpoints == nil || deps.initial == nil || deps.materials == nil ||
		deps.context == nil {
		return nil, fmt.Errorf("governed runtime: store and all dependencies are required")
	}
	now := deps.now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	capabilities := writingplan.DefaultCapabilityRegistry()
	executors := writingruntime.NewExecutorRegistry()
	for _, spec := range specs {
		if _, ok := capabilities.Get(spec.CapabilityID); !ok {
			return nil, fmt.Errorf("governed runtime: capability %s is not in the default catalog", spec.CapabilityID)
		}
		if err := capabilities.RegisterExecutor(writingplan.ExecutorBinding{ID: spec.BindingID,
			AcceptedInputTypes:  append([]writingplan.ArtifactType(nil), spec.Inputs...),
			ProducedOutputTypes: append([]writingplan.ArtifactType(nil), spec.Outputs...),
			// The governed runtime dispatches through the rollout executor, not
			// this binding's closure; the binding exists to satisfy the plan
			// compiler's executor-existence check.
			Dispatch: func(context.Context, writingplan.ExecutionRequest) (writingplan.ExecutionResult, error) {
				return writingplan.ExecutionResult{}, nil
			}}); err != nil {
			return nil, fmt.Errorf("register executor binding %s: %w", spec.BindingID, err)
		}
		// Bind the manifest to this spec's executor and mark it available:
		// without this the plan compiler keeps seeing the declared-only
		// catalog and every compile fails closed as T4 (M1.4 seam fix).
		if err := capabilities.Activate(spec.CapabilityID, spec.BindingID); err != nil {
			return nil, fmt.Errorf("activate capability %s: %w", spec.CapabilityID, err)
		}
		if spec.Direct != nil {
			// Research executors (T06, design.md §3) are typed runtime
			// executors: they register directly — no LegacyExecutorAdapter and
			// no shadow-lane wrapping (there is no legacy baseline to compare
			// against). The binding above satisfies the compile-time existence
			// check; the direct executor's descriptor must carry the same id.
			if spec.Direct.Descriptor().ExecutorID != spec.BindingID {
				return nil, fmt.Errorf("governed runtime: direct executor %s does not match binding %s",
					spec.Direct.Descriptor().ExecutorID, spec.BindingID)
			}
			if err := executors.Register(spec.Direct); err != nil {
				return nil, fmt.Errorf("register research executor %s: %w", spec.CapabilityID, err)
			}
			continue
		}
		descriptor := writingruntime.ExecutorDescriptor{ExecutorID: spec.BindingID, Version: "1", SupportedNodeKinds: []writingplan.NodeKind{writingplan.NodeAction, writingplan.NodeValidate}}
		baseline, err := writingruntime.NewLegacyExecutorAdapter(writingruntime.AdapterFamilyEngine, descriptor, spec.CapabilityID, spec.CapabilityVersion, spec.Permissions, deps.canonical, spec.Runner)
		if err != nil {
			return nil, err
		}
		rollout := &servicePolicyExecutor{baseline: baseline, spec: spec, deps: deps, store: store, mode: mode}

		if err := executors.Register(rollout); err != nil {
			return nil, fmt.Errorf("register rollout executor %s: %w", spec.CapabilityID, err)
		}
	}
	orchestrator := &writingruntime.Orchestrator{Store: store, Capabilities: capabilities, Executors: executors,
		State: writingruntime.NewStateMachine(deps.transitionStore), Checkpoints: deps.checkpoints, Initial: deps.initial,
		Materials: deps.materials, Telemetry: deps.telemetry, Now: now,
		Subject: func(run writingstore.RuntimeRun) string { return run.OwnerUserID },
		Context: deps.context, Envelopes: store,
		ContextRuntime: &writingruntime.ContextRuntime{},
		// M1.0 delivery protocol: draft commits its candidate version, quality
		// commits the delivery bundle (docs/21 §21.8). Kernel delivery is a
		// system behavior.
		Delivery: &writingruntime.DeliveryProtocol{Store: store,
			Content: writingruntime.WritingStoreContentGateway{Store: store},
			Actor:   writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.delivery"}, Now: now}}
	return &governedWritingRuntime{orchestrator: orchestrator, controller: governedRunController{orchestrator: orchestrator}, capabilities: capabilities, mode: mode}, nil
}
