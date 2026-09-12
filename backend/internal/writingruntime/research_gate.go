package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Human-gate orchestration (design.md §5). Reaching a gate node commits the
// pending gate, the waiting-gate checkpoint (a NEW snapshot version, never an
// in-place manifest edit — snapshots are immutable), the paused transitions,
// and the gate.pending event in ONE store transaction. The decision
// transaction commits the decision, the gate node's succeeded attempt, and
// the cleared checkpoint together; recovery consumes the persisted decision
// instead of re-blocking.

var (
	// ErrGateApprovalRequired maps to 409 GATE_APPROVAL_REQUIRED: a plain
	// resume cannot advance past an unconfirmed gate.
	ErrGateApprovalRequired = errors.New("writingruntime: gate approval required")
)

// GatePauseStore is the store surface the orchestrator needs for the atomic
// gate arrival. *writingstore.Store implements it.
type GatePauseStore interface {
	PauseAtHumanGate(ctx context.Context, pause writingstore.HumanGatePause) (writingstore.GateRecord, bool, error)
}

// GateDecisionStore is the surface the gate-decision flow (API layer) needs:
// the decision transaction, gate lookups, and content staging for the
// server-created approval artifact.
type GateDecisionStore interface {
	DecideGate(ctx context.Context, command writingstore.GateDecisionCommand) (writingstore.GateDecisionResult, error)
	GetRunGate(ctx context.Context, runID, nodeID string, planVersion int) (writingstore.GateRecord, error)
	GetGate(ctx context.Context, runID, gateID string) (writingstore.GateRecord, error)
	GetArtifactContent(ctx context.Context, contentHash string) (string, []byte, error)
	PutArtifactContent(ctx context.Context, contentHash, mediaType string, body []byte) error
	LoadActivePlan(ctx context.Context, runID string) (writingstore.PlanRecord, error)
	LoadLatestSnapshot(ctx context.Context, runID string) (writingstore.SnapshotRecord, error)
	LoadRuntimeRun(ctx context.Context, runID string) (writingstore.RuntimeRun, error)
	ListRunAttempts(ctx context.Context, runID string) ([]writingstore.NodeAttempt, error)
}

// gateTrace is the system trace for gate arrival and decision events.
func gateTrace() writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "flow": "research_review"},
		SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorSystem, ID: "writingruntime.gate"}}
}

// DecisionTrace builds the user-actor trace for a gate decision.
func DecisionTrace(actorID string) writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"runtime": "governed", "flow": "research_review"},
		SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: actorID}}
}

// GateArrival carries everything the orchestrator knows at the gate node.
type GateArrival struct {
	Run             writingstore.RuntimeRun
	Plan            writingplan.ExecutablePlan
	Node            writingplan.PlanNode
	PlanVersion     int
	GateKind        string
	Input           writingstore.ArtifactContentRef
	Completed       map[string]int
	Artifacts       []InputArtifact
	SpentCostUSD    float64
	SpentDurationMS int64
}

// PauseAtGate is the orchestrator's atomic gate arrival: one store
// transaction commits the pending gate row, the running→pausing→paused
// transitions, the waiting-gate checkpoint, and the gate.pending event.
// Re-arrival (a replayed dispatch) is a no-op returning the existing row.
func (orchestrator *Orchestrator) PauseAtGate(ctx context.Context, store GatePauseStore, checkpoints CheckpointTxCommitter, arrival GateArrival) (writingstore.GateRecord, error) {
	if store == nil || checkpoints == nil {
		return writingstore.GateRecord{}, ErrRuntimeNotReady
	}
	gateID := writingstore.StableID("gate_", arrival.Run.RunID, arrival.Plan.PlanID,
		fmt.Sprint(arrival.PlanVersion), arrival.Node.NodeID)
	gate := writingstore.GateRecord{GateID: gateID, RunID: arrival.Run.RunID, NodeID: arrival.Node.NodeID,
		PlanID: arrival.Plan.PlanID, PlanVersion: arrival.PlanVersion, PlanHash: arrival.Plan.PlanHash,
		GateKind: arrival.GateKind, InputArtifactID: arrival.Input.ArtifactID,
		InputArtifactVersion: arrival.Input.Version, InputHash: arrival.Input.ContentHash,
		Revision: 1, OwnerUserID: arrival.Run.OwnerUserID}
	now := orchestrator.Now()
	transitions := []writingstore.RunTransitionCommand{
		orchestrator.gateTransition(arrival.Run.RunID, string(StateRunning), string(StatePausing), now),
		orchestrator.gateTransition(arrival.Run.RunID, string(StatePausing), string(StatePaused), now),
	}
	pause := writingstore.HumanGatePause{Gate: gate, Transitions: transitions, Trace: gateTrace()}
	pause.Checkpoint = func(tx *writingstore.Tx) error {
		// The checkpoint writer runs inside the store transaction: the
		// waiting-gate marker commits atomically with the gate row and the
		// paused projection. UnsafeInFlight stays empty — a gate wait is not
		// in-flight execution.
		checkpoint := orchestrator.gateCheckpoint(arrival, now, arrival.Node.NodeID)
		return checkpoints.CommitWithin(ctx, tx, checkpoint)
	}
	record, _, err := store.PauseAtHumanGate(ctx, pause)
	return record, err
}

func (orchestrator *Orchestrator) gateTransition(runID, from, to string, now time.Time) writingstore.RunTransitionCommand {
	id := orchestrator.commands.Add(1)
	return writingstore.RunTransitionCommand{RunID: runID,
		IdempotencyKey: runID + ":transition:" + writingstore.StableID("command_", runID, "gate", from, to, fmt.Sprint(now.UnixNano()), fmt.Sprint(id)),
		ExpectedFrom:   from, RequestedTo: to, RuleAccepted: true,
		Cause: "human_gate", ReasonCode: "human_gate",
		Summary: "waiting for human gate confirmation", OccurredAt: now, Trace: gateTrace()}
}

// gateCheckpoint builds the waiting-gate checkpoint. checkpointID includes
// the waiting marker, so a gate pause always persists as its own snapshot
// even when the completed set is unchanged.
func (orchestrator *Orchestrator) gateCheckpoint(arrival GateArrival, now time.Time, waitingNodeID string) Checkpoint {
	copyCompleted := make(map[string]int, len(arrival.Completed))
	for key, value := range arrival.Completed {
		copyCompleted[key] = value
	}
	refs := make([]string, 0, len(arrival.Artifacts))
	for _, artifact := range arrival.Artifacts {
		refs = append(refs, artifactIdentity(artifact.ArtifactID, artifact.Version))
	}
	return Checkpoint{CheckpointID: checkpointID(arrival.Run.RunID, arrival.Plan.PlanHash, copyCompleted, arrival.Artifacts, waitingNodeID),
		RunID: arrival.Run.RunID, PlanID: arrival.Plan.PlanID, PlanVersion: arrival.PlanVersion,
		PlanHash: arrival.Plan.PlanHash, CompletedNodes: copyCompleted, ArtifactRefs: refs,
		SpentCostUSD: arrival.SpentCostUSD, SpentDurationMS: arrival.SpentDurationMS,
		UnsafeInFlight: []string{}, WaitingGateID: waitingNodeID, CreatedAt: now}
}

// DecidedGateCheckpoint clears the waiting marker for the resume path: the
// decision transaction commits this successor snapshot right after the
// decision row, so a crash between the two leaves no ambiguous state —
// either both are visible or neither is.
func (orchestrator *Orchestrator) DecidedGateCheckpoint(arrival GateArrival, now time.Time) Checkpoint {
	checkpoint := orchestrator.gateCheckpoint(arrival, now, "")
	return checkpoint
}

// decodeCheckpointManifest is the shared manifest decoder (identical to the
// repository's LoadLatest decoding).
func decodeCheckpointManifest(manifest map[string]any) (Checkpoint, error) {
	payload, err := json.Marshal(manifest)
	if err != nil {
		return Checkpoint{}, fmt.Errorf("marshal checkpoint manifest: %w", err)
	}
	var checkpoint Checkpoint
	if err := json.Unmarshal(payload, &checkpoint); err != nil {
		return Checkpoint{}, fmt.Errorf("decode runtime checkpoint: %w", err)
	}
	if checkpoint.ArtifactRefs == nil {
		checkpoint.ArtifactRefs = []string{}
	}
	if checkpoint.UnsafeInFlight == nil {
		checkpoint.UnsafeInFlight = []string{}
	}
	return checkpoint, checkpoint.Validate()
}

// LoadWaitingGate resolves the gate a paused run is parked on: the checkpoint
// carries the waiting node id and the gate row carries the binding. A nil
// record (nil error) means the run is not gate-paused.
func LoadWaitingGate(ctx context.Context, store GateDecisionStore, runID string) (*writingstore.GateRecord, error) {
	if store == nil {
		return nil, ErrRuntimeNotReady
	}
	run, err := store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if run.Status != string(StatePaused) {
		return nil, nil
	}
	snapshot, err := store.LoadLatestSnapshot(ctx, runID)
	if errors.Is(err, writingstore.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	checkpoint, err := decodeCheckpointManifest(snapshot.Manifest)
	if err != nil {
		return nil, err
	}
	if checkpoint.WaitingGateID == "" {
		return nil, nil
	}
	gate, err := store.GetRunGate(ctx, runID, checkpoint.WaitingGateID, run.ActivePlanVersion)
	if errors.Is(err, writingstore.ErrNotFound) {
		return nil, fmt.Errorf("writingruntime: checkpoint waits on unknown gate node %s", checkpoint.WaitingGateID)
	}
	if err != nil {
		return nil, err
	}
	return &gate, nil
}

// CheckRunGateForResume gates a plain resume on the run's gate state:
//   - no waiting gate: normal resume (nil gate, nil error)
//   - pending gate: blocked — ErrGateApprovalRequired
//   - approved gate: the persisted decision is consumed transparently (the
//     gate node's succeeded attempt from the decision transaction completes
//     it in recovery) and resume proceeds.
func CheckRunGateForResume(ctx context.Context, store GateDecisionStore, runID string) (*writingstore.GateRecord, error) {
	gate, err := LoadWaitingGate(ctx, store, runID)
	if err != nil || gate == nil {
		return gate, err
	}
	if gate.Status == writingstore.GateStatusPending {
		return gate, fmt.Errorf("%w: %s", ErrGateApprovalRequired, gate.GateID)
	}
	return gate, nil
}

// gateAttemptFor builds the synthetic succeeded attempt the decision
// transaction records for the gate node: the kernel owns the node (no
// executor dispatch ever ran), attempt 1, the node's pause failure path.
func gateAttemptFor(run writingstore.RuntimeRun, planVersion int, node writingplan.PlanNode) writingstore.NodeAttempt {
	return writingstore.NodeAttempt{RunID: run.RunID, PlanID: run.ActivePlanID,
		PlanVersion: planVersion, NodeID: node.NodeID, Attempt: 1,
		NodeKind: writingplan.NodeHumanGate, CapabilityID: node.Capability,
		CapabilityVersion: node.CapabilityVersion, ExecutorID: "kernel.human_gate",
		FailurePath: node.FailurePath,
		Bounds:      writingplan.Bounds{MaxAttempts: 1, MaxConcurrency: 1, MaxItems: 1, MaxCostUSD: 0, TimeoutMS: 1},
		InputHash:   "sha256:" + strings.Repeat("0", 64), InputArtifactIDs: []string{}}
}

// GateDecisionRequest is the verified API payload for one gate confirmation.
type GateDecisionRequest struct {
	RunID            string
	GateID           string
	ActorID          string
	PlanID           string
	PlanVersion      int
	PlanHash         string
	GateRevision     int
	InputArtifactID  string
	InputVersion     int
	InputContentHash string
	Decision         string
	IdempotencyKey   string
	RequestHash      string
}

// EvidenceApprovalBody builds the server-created evidence_approval artifact
// content for an approved evidence gate (contracts.md §2: clients never write
// actor_id/decided_at — the server does, from the verified decision).
func EvidenceApprovalBody(gateID, actorID string, decidedAt time.Time, planHash string, input writingstore.ArtifactContentRef) writingkernel.EvidenceApproval {
	return writingkernel.EvidenceApproval{SchemaVersion: writingkernel.EvidenceApprovalSchemaVersion,
		GateID: gateID, ActorID: actorID, DecidedAt: decidedAt.UTC().Format(time.RFC3339),
		PlanHash:        planHash,
		EvidencePackRef: writingkernel.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version, ContentHash: input.ContentHash},
		Decision:        writingstore.GateDecisionApprove}
}

// DecideGate executes the API-facing confirmation: verify the run/gate
// binding, run the store's atomic decision transaction (decision row + gate
// node succeeded attempt + approval artifact + cleared waiting-gate
// checkpoint + gate.decided event), and return the persisted gate. The run
// stays paused; the caller schedules the resume trigger separately. The
// returned bool marks an idempotent replay of an already-approved decision.
func DecideGate(ctx context.Context, store GateDecisionStore, checkpoints CheckpointTxCommitter, orchestrator *Orchestrator, request GateDecisionRequest) (writingstore.GateDecisionResult, error) {
	if store == nil || checkpoints == nil || orchestrator == nil {
		return writingstore.GateDecisionResult{}, ErrRuntimeNotReady
	}
	run, err := store.LoadRuntimeRun(ctx, request.RunID)
	if err != nil {
		return writingstore.GateDecisionResult{}, err
	}
	if run.OwnerUserID != request.ActorID {
		// Handlers translate this to 404: gate existence is not leaked.
		return writingstore.GateDecisionResult{}, writingstore.ErrGateForbidden
	}
	gate, err := store.GetGate(ctx, request.RunID, request.GateID)
	if err != nil {
		return writingstore.GateDecisionResult{}, err
	}
	planRecord, err := store.LoadActivePlan(ctx, request.RunID)
	if err != nil {
		return writingstore.GateDecisionResult{}, err
	}
	plan := planRecord.Envelope.ExecutablePlan
	var gateNode *writingplan.PlanNode
	for index := range plan.Nodes {
		if plan.Nodes[index].NodeID == gate.NodeID {
			gateNode = &plan.Nodes[index]
			break
		}
	}
	if gateNode == nil || gateNode.Kind != writingplan.NodeHumanGate {
		return writingstore.GateDecisionResult{}, fmt.Errorf("writingruntime: gate %s is not a plan gate node", gate.GateID)
	}
	input := writingstore.ArtifactContentRef{ArtifactID: request.InputArtifactID,
		Version: request.InputVersion, ContentHash: request.InputContentHash}
	decidedAt := orchestrator.Now()
	var decisionArtifact writingstore.ArtifactRecord
	switch gate.GateKind {
	case writingstore.GateKindOutline:
		// The outline gate's decision artifact is the approved_research_outline
		// (contracts.md §2): the server loads the gate's input outline (the
		// revision the decision binds), verifies its hash, and seals it with
		// the source outline ref and the gate decision ref. Clients can never
		// write actor/decision content — this is server-created in the same
		// transaction as the decision row.
		artifact, buildErr := approvedOutlineArtifact(run, gate, gateNode, input, request.ActorID, decidedAt, store)
		if buildErr != nil {
			return writingstore.GateDecisionResult{}, buildErr
		}
		decisionArtifact = artifact
	default:
		approval := EvidenceApprovalBody(gate.GateID, request.ActorID, decidedAt, gate.PlanHash, input)
		if err := approval.Validate(); err != nil {
			return writingstore.GateDecisionResult{}, fmt.Errorf("writingruntime: server-created approval artifact rejected: %w", err)
		}
		body, err := json.Marshal(approval)
		if err != nil {
			return writingstore.GateDecisionResult{}, err
		}
		approvalHash := contentHash(body)
		if err := store.PutArtifactContent(ctx, approvalHash, "application/json", body); err != nil {
			return writingstore.GateDecisionResult{}, err
		}
		decisionArtifact = writingstore.ArtifactRecord{ArtifactID: writingstore.StableID("art_", request.RunID, gate.NodeID, "approval"),
			Version: 1, RunID: run.RunID, PlanID: plan.PlanID, PlanVersion: planRecord.PlanVersion,
			NodeID: gate.NodeID, Attempt: 1, OutputKey: "evidence_approval",
			ArtifactType: "evidence_approval", Status: "validated", ContentHash: approvalHash,
			MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(approvalHash, "sha256:"),
			Parents: []writingstore.ArtifactRef{}, Producer: "kernel.human_gate",
			CapabilityVersion: gateNode.CapabilityVersion, InputHashes: []string{request.InputContentHash},
			Trace: DecisionTrace(request.ActorID)}
	}
	command := writingstore.GateDecisionCommand{RunID: request.RunID, GateID: request.GateID,
		PlanID: request.PlanID, PlanVersion: request.PlanVersion, PlanHash: request.PlanHash,
		GateRevision: request.GateRevision, Input: input, Decision: request.Decision,
		ActorID: request.ActorID, IdempotencyKey: request.IdempotencyKey, RequestHash: request.RequestHash,
		Attempt: gateAttemptFor(run, planRecord.PlanVersion, *gateNode),
		AttemptCompletion: writingstore.AttemptCompletion{Artifacts: []writingstore.ArtifactRecord{decisionArtifact},
			CompletedAt: decidedAt, Trace: DecisionTrace(request.ActorID)},
		Trace: DecisionTrace(request.ActorID)}
	command.AfterDecision = func(tx *writingstore.Tx) error {
		// The cleared waiting-gate checkpoint commits inside the decision
		// transaction: after any crash the run is either waiting-and-undecided
		// or decided-and-clear — never both markers.
		snapshot, err := tx.LoadLatestSnapshot(ctx, request.RunID)
		if err != nil {
			return err
		}
		previous, err := decodeCheckpointManifest(snapshot.Manifest)
		if err != nil {
			return err
		}
		if previous.WaitingGateID != "" && previous.WaitingGateID != gate.NodeID {
			return fmt.Errorf("writingruntime: checkpoint waits on %s, decision targeted %s", previous.WaitingGateID, gate.NodeID)
		}
		arrival := GateArrival{Run: run, Plan: plan, Node: *gateNode, PlanVersion: planRecord.PlanVersion,
			GateKind: gate.GateKind, Input: input,
			Completed: previous.CompletedNodes, Artifacts: artifactsFromRefs(previous.ArtifactRefs),
			SpentCostUSD: previous.SpentCostUSD, SpentDurationMS: previous.SpentDurationMS}
		return checkpoints.CommitWithin(ctx, tx, orchestrator.DecidedGateCheckpoint(arrival, decidedAt))
	}
	result, err := store.DecideGate(ctx, command)
	if err != nil {
		return writingstore.GateDecisionResult{}, err
	}
	return result, nil
}

// approvedOutlineArtifact builds the outline gate's decision artifact: the
// approved_research_outline content sealed over the gate's input outline
// (the exact revision the decision binds — an outline revision bumped the
// gate's input ref before the confirm). The GateDecisionRef points at the
// gate row itself (gate_id + revision + plan hash), the durable decision
// record this artifact exists to witness.
func approvedOutlineArtifact(run writingstore.RuntimeRun, gate writingstore.GateRecord, gateNode *writingplan.PlanNode,
	input writingstore.ArtifactContentRef, actorID string, decidedAt time.Time, store GateDecisionStore) (writingstore.ArtifactRecord, error) {
	_, outlineBody, err := store.GetArtifactContent(context.Background(), input.ContentHash)
	if err != nil {
		return writingstore.ArtifactRecord{}, fmt.Errorf("writingruntime: load outline gate input content: %w", err)
	}
	if contentHash(outlineBody) != input.ContentHash {
		return writingstore.ArtifactRecord{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever,
			"outline gate input content failed hash verification", nil)
	}
	var outline writingkernel.ResearchOutline
	if err := json.Unmarshal(outlineBody, &outline); err != nil {
		return writingstore.ArtifactRecord{}, runtimeError(CodeMaterialIntegrityFailed, RetryNever,
			"outline gate input is not a research outline", err)
	}
	if err := outline.Validate(); err != nil {
		return writingstore.ArtifactRecord{}, runtimeError(CodeOutlineEvidenceMismatch, RetryNever,
			"outline gate input failed kernel validation", err)
	}
	approved := writingkernel.ApprovedResearchOutline{ResearchOutline: outline,
		SourceOutlineRef: writingkernel.ArtifactRef{ArtifactID: input.ArtifactID, Version: input.Version, ContentHash: input.ContentHash},
		GateDecisionRef:  writingkernel.ArtifactRef{ArtifactID: gate.GateID, Version: gate.Revision, ContentHash: gate.PlanHash}}
	if err := approved.Validate(); err != nil {
		return writingstore.ArtifactRecord{}, runtimeError(CodeOutlineEvidenceMismatch, RetryNever,
			"approved outline failed kernel validation", err)
	}
	body, err := json.Marshal(approved)
	if err != nil {
		return writingstore.ArtifactRecord{}, err
	}
	hash := contentHash(body)
	// The decision transaction's artifact row references this content; stage
	// it before the transaction commits (same discipline as the evidence
	// approval artifact).
	if err := store.PutArtifactContent(context.Background(), hash, "application/json", body); err != nil {
		return writingstore.ArtifactRecord{}, fmt.Errorf("writingruntime: stage approved outline content: %w", err)
	}
	return writingstore.ArtifactRecord{ArtifactID: writingstore.StableID("art_", run.RunID, gate.NodeID, "approval"),
		Version: 1, RunID: run.RunID, PlanID: gate.PlanID, PlanVersion: gate.PlanVersion,
		NodeID: gate.NodeID, Attempt: 1, OutputKey: "approved_research_outline",
		ArtifactType: "approved_research_outline", Status: "validated", ContentHash: hash,
		MediaType: "application/json", ContentRef: "artifact://" + strings.TrimPrefix(hash, "sha256:"),
		Parents: []writingstore.ArtifactRef{}, Producer: "kernel.human_gate",
		CapabilityVersion: gateNode.CapabilityVersion, InputHashes: []string{input.ContentHash},
		Trace: DecisionTrace(actorID)}, nil
}

// artifactsFromRefs rebuilds input artifacts (identity only) from checkpoint
// artifact refs so the cleared successor checkpoint preserves the ref list.
func artifactsFromRefs(refs []string) []InputArtifact {
	artifacts := make([]InputArtifact, 0, len(refs))
	for _, ref := range refs {
		parts := strings.Split(ref, ":")
		if len(parts) != 2 {
			continue
		}
		version := 0
		fmt.Sscanf(parts[1], "%d", &version)
		artifacts = append(artifacts, InputArtifact{ArtifactID: parts[0], Version: version})
	}
	return artifacts
}
