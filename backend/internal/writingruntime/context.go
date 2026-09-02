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

// M4a context shadow wiring (docs/18 §18.12): the orchestrator compiles one
// envelope per node attempt from the capability's manifest contract, persists
// it, and hands it to the executor. Required blocks that go unsupplied are
// recorded in the envelope and in telemetry — they never fail the node in
// M4a. Activating required-context fail-closed is a separate, explicitly
// reviewed policy change (M4b).

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
// tables, scoped through the run's document project. Blocks without a data
// source in this milestone (document state, source evidence, style
// directives) stay empty — the manifest contract decides whether their
// absence is flagged.
type StoreContextSource struct {
	Store *writingstore.Store
}

// CompileInputs assembles the compiler input for one node attempt.
func (source StoreContextSource) CompileInputs(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode) (contextcompiler.Input, error) {
	input := contextcompiler.Input{
		ContractDigest: fmt.Sprintf("contract %s v%d | node %s | capability %s@%s",
			run.ContractID, run.ContractVersion, node.NodeID, node.Capability, node.CapabilityVersion),
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

// compileNodeContext is the shadow hook: compile, persist, and return the
// envelope for injection. Every failure degrades to nil with a telemetry
// record — shadow mode must not change execution outcomes.
func (orchestrator *Orchestrator) compileNodeContext(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, attempt int, manifest writingplan.CapabilityManifest) *contextcompiler.Envelope {
	if orchestrator.Context == nil {
		return nil
	}
	observe := func(status string) {
		observeRuntime(ctx, orchestrator.Telemetry, RuntimeMetric{Kind: "runtime.context_envelope",
			ExecutorID: manifest.Executor, Capability: node.Capability, Mode: "shadow",
			Lane: LaneBaseline, Status: status})
	}
	input, err := orchestrator.Context.CompileInputs(ctx, run, node)
	if err != nil {
		observe("source_failed")
		return nil
	}
	input.Wanted = manifest.Context.ContextWanted()
	if manifest.Context.ContextTokenBudget > 0 {
		input.TotalBudget = manifest.Context.ContextTokenBudget
	}
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		observe("compile_failed")
		return nil
	}
	if orchestrator.Envelopes != nil {
		payload, err := json.Marshal(envelope)
		if err != nil {
			observe("persist_failed")
			return &envelope
		}
		record := writingstore.ContextEnvelopeRecord{EnvelopeID: writingstore.StableID("env_", run.RunID, node.NodeID, fmt.Sprint(attempt), envelope.Hash),
			RunID: run.RunID, NodeID: node.NodeID, Attempt: attempt, CompilerVersion: envelope.CompilerVersion,
			EnvelopeHash: envelope.Hash, Payload: payload, Missing: envelope.Missing,
			Trimmed: envelope.Trimmed, Diagnostics: envelope.Diagnostics()}
		if err := orchestrator.Envelopes.SaveContextEnvelope(ctx, record); err != nil {
			observe("persist_failed")
			return &envelope
		}
	}
	observe("succeeded")
	return &envelope
}
