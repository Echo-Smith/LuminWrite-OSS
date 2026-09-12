package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// M4b context activation (docs/18 §18.12): the orchestrator compiles one
// envelope per node attempt from the capability's manifest contract, persists
// it, and hands it to the executor. Required blocks that go unsupplied are
// recorded in the envelope and in telemetry; a capability that opted into
// EnforceRequiredContext additionally fails the node with
// CONTEXT_REQUIRED_MISSING. Infrastructure degradation never fails the node.

// ContextSource preloads the compiler inputs for one node attempt. The
// orchestrator never queries project memory directly: implementors decide how
// run/project scoping works. A source that cannot reach project data returns
// a bare input — every contract block then lands in the envelope's missing
// channel, which keeps runs without a project scope fully auditable.
type ContextSource interface {
	CompileInputs(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode) (contextcompiler.Input, error)
}

// ContextEnvelopeSink persists compiled envelopes. Nil disables persistence
// while still injecting the envelope into the request.
type ContextEnvelopeSink interface {
	SaveContextEnvelope(ctx context.Context, record writingstore.ContextEnvelopeRecord) error
}

// StoreContextSource compiles inputs from the writingstore project-memory
// tables, scoped through the run's document project. Blocks whose backing
// data does not exist yet (source evidence, style directives) stay empty —
// the manifest contract decides whether their absence is flagged.
type StoreContextSource struct {
	Store *writingstore.Store
}

// CompileInputs assembles the compiler input for one node attempt.
func (source StoreContextSource) CompileInputs(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode) (contextcompiler.Input, error) {
	input := contextcompiler.Input{
		ContractDigest: fmt.Sprintf("contract %s v%d | node %s | capability %s@%s",
			run.ContractID, run.ContractVersion, node.NodeID, node.Capability, node.CapabilityVersion),
	}
	// M5 document_state (docs/18 §18.5.2): the run document's committed
	// current version, rendered as the subtree summary. It belongs to the
	// run's document, not to project memory, so it is resolved before the
	// project branch — a document without a project still satisfies required
	// document_state (M1.4 e2e finding). A missing document record (or a
	// dangling current-version pointer) stays empty: that is a real data
	// gap, and fail-closed records it explicitly.
	if document, err := source.Store.GetDocument(ctx, run.DocumentID); err != nil {
		if !errors.Is(err, writingstore.ErrNotFound) {
			return input, err
		}
	} else if document.CurrentVersionID == "" {
		input.DocumentState = documentStateEmpty
	} else if version, err := source.Store.GetDocumentVersion(ctx, run.DocumentID, document.CurrentVersionID); err != nil {
		if !errors.Is(err, writingstore.ErrNotFound) {
			return input, err
		}
	} else {
		input.DocumentState = renderDocumentState(version)
	}
	projectID, err := source.Store.DocumentProjectID(ctx, run.DocumentID)
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			return input, nil
		}
		return input, err
	}
	input.ProjectID = projectID

	if facts, err := source.Store.ListActiveFacts(ctx, projectID, ""); err != nil {
		return input, err
	} else {
		for _, fact := range facts {
			input.FactLines = append(input.FactLines, fmt.Sprintf("%s | %s | %s（as_of %s）", fact.Subject, fact.Predicate, fact.Object, fact.ValidFrom.Format("2006-01-02")))
		}
	}
	if entries, err := source.Store.ListTerminology(ctx, projectID, "active"); err != nil {
		return input, err
	} else {
		for _, entry := range entries {
			line := entry.Term
			if entry.Definition != "" {
				line += "：" + entry.Definition
			}
			if len(entry.Forbidden) > 0 {
				line += "；禁止：" + strings.Join(entry.Forbidden, "、")
			}
			input.TerminologyLines = append(input.TerminologyLines, line)
		}
	}
	if decisions, err := source.Store.ListDecisions(ctx, projectID, "active"); err != nil {
		return input, err
	} else {
		for _, decision := range decisions {
			input.DecisionLines = append(input.DecisionLines, decision.Statement)
		}
	}
	if questions, err := source.Store.ListOpenQuestions(ctx, projectID, "open"); err != nil {
		return input, err
	} else {
		for _, question := range questions {
			input.QuestionLines = append(input.QuestionLines, question.Question)
		}
	}
	if entities, err := source.Store.ListMemoryEntities(ctx, projectID, "", "promoted"); err != nil {
		return input, err
	} else {
		for _, entity := range entities {
			line := fmt.Sprintf("%s [%s]", entity.CanonicalName, entity.EntityKind)
			if len(entity.Aliases) > 0 {
				line += " 别名：" + strings.Join(entity.Aliases, "、")
			}
			input.EntityCards = append(input.EntityCards, line)
		}
	}
	if threads, err := source.Store.ListThreads(ctx, projectID, "active"); err != nil {
		return input, err
	} else {
		for _, thread := range threads {
			if thread.Resident {
				input.ThreadLabels = append(input.ThreadLabels, thread.Label)
			}
		}
	}
	sort.Strings(input.ThreadLabels)
	return input, nil
}

// compileNodeContext compiles, persists, and returns the envelope for
// injection. Failure semantics are deliberately split:
//   - infrastructure degradation (source error, compile error, persistence
//     error) degrades to a nil envelope and never fails the node — shadow
//     mode must not change execution outcomes, and an infrastructure gap is
//     not evidence of a context gap;
//   - a capability that opted into EnforceRequiredContext and compiled an
//     envelope missing a required block fails the node with
//     CONTEXT_REQUIRED_MISSING (M4b activation, per-manifest).
//
// M5 runtime (docs/18 §18.5.6): a compiled envelope is observed for context
// pressure — 0.70 warns, 0.85 schedules one guarded pre-compression
// (cooldown + in-flight) whose smaller envelope replaces the injected one
// and is persisted with a "-p" envelope id suffix so evidence keeps both.
func (orchestrator *Orchestrator) compileNodeContext(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, attempt int, manifest writingplan.CapabilityManifest) (*contextcompiler.Envelope, error) {
	if orchestrator.Context == nil {
		return nil, nil
	}
	observe := func(status string) {
		observeRuntime(ctx, orchestrator.Telemetry, RuntimeMetric{Kind: MetricContextEnvelope,
			ExecutorID: manifest.Executor, Capability: node.Capability, Mode: "shadow",
			Lane: LaneBaseline, Status: status})
	}
	observeRecovery := func(status string, path RecoveryPath) {
		observeRuntime(ctx, orchestrator.Telemetry, RuntimeMetric{Kind: MetricContextEnvelope,
			ExecutorID: manifest.Executor, Capability: node.Capability, Mode: "shadow",
			Lane: LaneBaseline, Status: status, Reason: string(path)})
	}
	input, err := orchestrator.Context.CompileInputs(ctx, run, node)
	if err != nil {
		// M5d: the failure category names its recovery path in telemetry.
		observeRecovery("source_failed", RecoveryPathFor(ContextFailureSource, 0))
		return nil, nil
	}
	input.Wanted = manifest.Context.ContextWanted()
	input.RetentionPriority = manifest.Context.ContextRetentionPriority()
	if manifest.Context.ContextTokenBudget > 0 {
		input.TotalBudget = manifest.Context.ContextTokenBudget
	}
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		if errors.Is(err, contextcompiler.ErrResidentOverflow) {
			observeRecovery("compile_failed", RecoveryPathFor(ContextFailureResidentOverflow, 0))
		} else {
			observeRecovery("compile_failed", RecoveryPathFor(ContextFailureCompile, 0))
		}
		return nil, nil
	}

	// M5 pre-compression (docs/18 §18.5.6): at >= 0.85 pressure recompile
	// once against a reduced budget under cooldown and in-flight guards.
	// A recompression failure degrades to the original envelope — the guard
	// component refines pressure, it never creates a context gap.
	if orchestrator.ContextRuntime != nil {
		observePressure := func(status string) {
			observeRuntime(ctx, orchestrator.Telemetry, RuntimeMetric{Kind: MetricContextPressure,
				ExecutorID: manifest.Executor, Capability: node.Capability, Mode: "shadow",
				Lane: LaneBaseline, Status: status})
		}
		switch orchestrator.ContextRuntime.Observe(node.Capability, &envelope) {
		case PrecompressionWarn:
			observe("pressure_warn")
			observePressure("warn")
		case PrecompressionRecompile:
			defer orchestrator.ContextRuntime.ReleaseRecompression(node.Capability)
			observePressure("recompile")
			if budget, budgetErr := orchestrator.ContextRuntime.CompressedBudget(&envelope); budgetErr == nil {
				recompressed := input
				recompressed.TotalBudget = budget
				if smaller, compileErr := contextcompiler.Compile(recompressed); compileErr == nil {
					observe("precompressed")
					observePressure("recompiled")
					envelope = smaller
				} else {
					// One self-compression already failed: canon is beyond
					// what the runtime may fix alone.
					observeRecovery("precompress_failed", RecoveryPathFor(ContextFailureCompile, 1))
					observePressure("recompile_failed")
				}
			} else {
				observeRecovery("precompress_refused", RecoveryPathFor(ContextFailureBudget, 0))
				observePressure("recompile_refused")
			}
		case PrecompressionCooling:
			observe("pressure_cooling")
			observePressure("cooling")
		case PrecompressionInFlight:
			observe("pressure_in_flight")
			observePressure("in_flight")
		}
	}

	if manifest.Context.EnforceRequiredContext {
		missing := map[string]bool{}
		for _, entry := range envelope.Missing {
			missing[entry.Block] = true
		}
		absent := []string{}
		for _, block := range manifest.Context.RequiredContext {
			if missing[string(block)] {
				absent = append(absent, string(block))
			}
		}
		if len(absent) > 0 {
			sort.Strings(absent)
			observe("required_missing")
			// The attempt is already started: record its completion so the
			// attempt ledger never leaves a running row behind.
			_ = orchestrator.completeAttempt(ctx, manifest.Executor, node.Capability, writingstore.AttemptCompletion{RunID: run.RunID,
				NodeID: node.NodeID, Attempt: attempt, Status: "failed", ErrorCode: string(CodeContextRequiredMissing),
				ErrorMessage: strings.Join(absent, ","), Trace: runtimeTrace(node.Capability), CompletedAt: orchestrator.Now()})
			return nil, runtimeError(CodeContextRequiredMissing, RetryNever,
				strings.Join(absent, ","), ErrContextRequiredMissing)
		}
	}
	if orchestrator.Envelopes != nil {
		payload, err := json.Marshal(envelope)
		if err != nil {
			observe("persist_failed")
			return &envelope, nil
		}
		record := writingstore.ContextEnvelopeRecord{EnvelopeID: writingstore.StableID("env_", run.RunID, node.NodeID, fmt.Sprint(attempt), envelope.Hash),
			RunID: run.RunID, NodeID: node.NodeID, Attempt: attempt, CompilerVersion: envelope.CompilerVersion,
			EnvelopeHash: envelope.Hash, Payload: payload, Missing: envelope.Missing,
			Trimmed: envelope.Trimmed, Diagnostics: envelope.Diagnostics()}
		if err := orchestrator.Envelopes.SaveContextEnvelope(ctx, record); err != nil {
			observe("persist_failed")
			return &envelope, nil
		}
	}
	observe("succeeded")
	return &envelope, nil
}
