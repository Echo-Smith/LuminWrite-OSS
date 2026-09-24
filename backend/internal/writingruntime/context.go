package writingruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/memoryport"
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
	Store      *writingstore.Store
	MemoryPort memoryport.Port // nil disables user-memory injection
	// Content is the content-addressed artifact gateway. When non-nil,
	// CompileInputs loads node artifacts and projects them into the
	// evidence view for the context envelope's source_evidence block.
	Content ContentGateway
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

	// Load user memory (WP1.2): project WriteDirectives into StyleDirectives,
	// ReviewGuard into ReviewGuard. The memoryport.Port is the anti-corruption
	// layer — its output is already rendered-ready DTOs.
	if source.MemoryPort != nil && source.MemoryPort.EnabledForUser(run.OwnerUserID) {
		bundle, err := source.MemoryPort.PrepareInjection(ctx, memoryport.Request{
			UserID:    run.OwnerUserID,
			Query:     node.Capability, // capability as query context
			Intent:    "writing",
			SessionID: run.RunID,
		})
		if err == nil && bundle != nil {
			// WriteDirectives → StyleDirectives
			for _, d := range bundle.WriteDirectives {
				input.StyleDirectives = append(input.StyleDirectives, d.Value)
			}
			// ReviewGuard → ReviewGuard
			for _, d := range bundle.ReviewGuard {
				input.ReviewGuard = append(input.ReviewGuard, d.Value)
			}
			// EntityProfile → EntityCards (only if capability declares it)
			if bundle.EntityProfile != "" {
				input.EntityCards = append(input.EntityCards, bundle.EntityProfile)
			}
		}
	}

	// WP2.1 Evidence View: project existing artifacts into the
	// source_evidence block. This is a deterministic projection — the same
	// artifacts always produce the same evidence lines.
	if evidenceView, err := source.renderEvidenceView(ctx, run, node); err == nil {
		for _, item := range evidenceView.Items {
			line := item.ClaimOrTopic
			if item.SourceRef != "" {
				line += " [" + item.SourceRef + "]"
			}
			if item.ContentHash != "" {
				short := item.ContentHash
				// Strip the "sha256:" prefix for the short display
				if len(short) > 12 {
					short = short[:12]
				}
				line += " {" + short + "}"
			}
			input.EvidenceLines = append(input.EvidenceLines, line)
		}
	}

	return input, nil
}

// renderEvidenceView loads the node's artifacts and runtime evidence, then
// projects them into a deterministic EvidenceView. Failure is non-fatal:
// the caller degrades gracefully (empty evidence lines).
func (source StoreContextSource) renderEvidenceView(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode) (EvidenceView, error) {
	if source.Store == nil {
		return EvidenceView{}, fmt.Errorf("store is required for evidence view")
	}
	// Load all artifacts for the run, then select the ones this node's view
	// projects: its own outputs, its resolved input references, and the
	// products of its transitive plan dependencies (WP1/WP2 closure).
	allArtifacts, err := source.Store.ListRunArtifacts(ctx, run.RunID)
	if err != nil {
		return EvidenceView{}, fmt.Errorf("list run artifacts: %w", err)
	}
	nodeArtifacts := selectEvidenceArtifacts(allArtifacts, node, source.loadEvidencePlan(ctx, run))

	// Build evidence sources from node artifacts.
	var evidenceSources EvidenceSources
	for _, art := range nodeArtifacts {
		observedAt := art.CreatedAt
		if observedAt.IsZero() {
			observedAt = time.Now().UTC()
		}
		switch art.ArtifactType {
		case "source_pack":
			if source.Content != nil {
				if sp, err := source.loadSourcePackEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.SourcePacks = append(evidenceSources.SourcePacks, sp)
				}
			}
		case "claim_map":
			if source.Content != nil {
				if cm, err := source.loadClaimMapEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.ClaimMaps = append(evidenceSources.ClaimMaps, cm)
				}
			}
		case "materials":
			if source.Content != nil {
				if mats, err := source.loadMaterialSnapshotEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.MaterialSnapshots = append(evidenceSources.MaterialSnapshots, mats...)
				}
			}
		case "research_evidence_pack":
			if source.Content != nil {
				if rps, err := source.loadResearchPackEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.ResearchPacks = append(evidenceSources.ResearchPacks, rps...)
				}
			}
		}
	}

	// Load runtime evidence events for this node.
	if events, err := source.Store.ListRunEvents(ctx, run.RunID, 0, 500); err == nil {
		for _, event := range events {
			if event.NodeID != node.NodeID {
				continue
			}
			if event.EntityKind != "rollout_evidence" {
				continue
			}
			evItem := RuntimeEvidenceItem{
				EvidenceID: event.EventID,
				Kind:       event.Payload["evidence_kind"].(string),
				Status:     event.EventType,
				RecordedAt: event.OccurredAt,
			}
			if lane, ok := event.Payload["lane"].(string); ok {
				evItem.Lane = lane
			}
			if policyHash, ok := event.Payload["policy_hash"].(string); ok {
				evItem.PolicyHash = policyHash
			}
			if policyVersion, ok := event.Payload["policy_version"].(float64); ok {
				evItem.PolicyVersion = int(policyVersion)
			}
			evidenceSources.RuntimeEvidence = append(evidenceSources.RuntimeEvidence, evItem)
		}
	}

	renderer := EvidenceViewRenderer{}
	return renderer.Render(node.NodeID, 0, evidenceSources), nil
}

// loadEvidencePlan resolves the run's active plan for the evidence view's
// dependency walk. It is best-effort by design: a missing or unloadable plan
// degrades the view to the node's own artifacts plus its input references —
// infrastructure gaps never fail context compilation.
func (source StoreContextSource) loadEvidencePlan(ctx context.Context, run writingstore.RuntimeRun) *writingplan.ExecutablePlan {
	record, err := source.Store.LoadActivePlan(ctx, run.RunID)
	if err != nil {
		return nil
	}
	return &record.Envelope.ExecutablePlan
}

// selectEvidenceArtifacts chooses the artifacts one node's evidence view
// projects, deterministically (input order preserved, identity deduped):
//
//  1. the node's own artifacts — a retry attempt re-projects its earlier
//     outputs (the pre-closure behavior, kept as a subset);
//  2. the node's resolved input references — the same
//     latest-version-per-type rule selectInputs applies at dispatch, so the
//     view covers exactly what the node will read;
//  3. the products of the node's TRANSITIVE plan dependencies — the plan
//     graph is the provenance contract, so a downstream node (research
//     draft, quality) sees the upstream research_evidence_pack through its
//     dependency chain even when the pack is not one of its declared input
//     types.
//
// plan may be nil (plan record unavailable): the dependency products then
// contribute nothing and the view degrades to (1)+(2). The offline replayer
// calls this with the persisted plan so replay stays byte-consistent with
// what compileNodeContext assembled.
func selectEvidenceArtifacts(allArtifacts []writingstore.ArtifactRecord, node writingplan.PlanNode, plan *writingplan.ExecutablePlan) []writingstore.ArtifactRecord {
	selected := map[string]bool{}
	key := func(art writingstore.ArtifactRecord) string {
		return art.ArtifactID + "\x00" + fmt.Sprint(art.Version)
	}
	include := func(art writingstore.ArtifactRecord) { selected[key(art)] = true }

	// (1) Own artifacts.
	for _, art := range allArtifacts {
		if art.NodeID == node.NodeID {
			include(art)
		}
	}
	// (2) Resolved input references: latest version per declared input type
	// (mirrors orchestrator selectInputs; ListRunArtifacts order is stable).
	latest := map[writingplan.ArtifactType]writingstore.ArtifactRecord{}
	for _, art := range allArtifacts {
		artifactType := writingplan.ArtifactType(art.ArtifactType)
		if current, ok := latest[artifactType]; !ok || art.Version > current.Version {
			latest[artifactType] = art
		}
	}
	for _, artifactType := range node.InputArtifactTypes {
		if art, ok := latest[artifactType]; ok {
			include(art)
		}
	}
	// (3) Transitive plan-dependency products. Only nodes that exist in the
	// plan graph count as dependency producers; within those products the
	// same latest-version-per-type freshness rule as the input resolution
	// applies, so a re-read's stale pack revision never shadows the current
	// one.
	if plan != nil {
		planNodes := map[string]bool{}
		for _, planNode := range plan.Nodes {
			planNodes[planNode.NodeID] = true
		}
		dependencies := map[string]bool{}
		pending := append([]string(nil), node.DependsOn...)
		for len(pending) > 0 {
			dependency := pending[len(pending)-1]
			pending = pending[:len(pending)-1]
			if dependencies[dependency] {
				continue
			}
			dependencies[dependency] = true
			for _, planNode := range plan.Nodes {
				if planNode.NodeID == dependency {
					pending = append(pending, planNode.DependsOn...)
				}
			}
		}
		latestDependency := map[writingplan.ArtifactType]writingstore.ArtifactRecord{}
		for _, art := range allArtifacts {
			if !dependencies[art.NodeID] || !planNodes[art.NodeID] {
				continue
			}
			artifactType := writingplan.ArtifactType(art.ArtifactType)
			if current, ok := latestDependency[artifactType]; !ok || art.Version > current.Version {
				latestDependency[artifactType] = art
			}
		}
		for _, art := range latestDependency {
			include(art)
		}
	}

	result := make([]writingstore.ArtifactRecord, 0, len(selected))
	for _, art := range allArtifacts {
		if selected[key(art)] {
			result = append(result, art)
		}
	}
	return result
}

// loadSourcePackEvidence parses a source_pack artifact into SourcePackEvidence.
func (source StoreContextSource) loadSourcePackEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) (SourcePackEvidence, error) {
	body, err := source.loadArtifactContent(ctx, art)
	if err != nil {
		return SourcePackEvidence{}, err
	}
	var pack SourcePack
	if err := json.Unmarshal(body, &pack); err != nil {
		return SourcePackEvidence{}, err
	}
	sources := make([]SourceRecordEvidence, len(pack.Sources))
	for i, s := range pack.Sources {
		sources[i] = SourceRecordEvidence{
			SourceID: s.SourceID, Title: s.Title,
			URL: s.URL, Excerpt: s.Excerpt, Score: s.Score,
		}
	}
	return SourcePackEvidence{
		Query: pack.Query, Sources: sources,
		ContentHash: art.ContentHash, ObservedAt: observedAt,
	}, nil
}

// loadClaimMapEvidence parses a claim_map artifact into ClaimMapEvidence.
func (source StoreContextSource) loadClaimMapEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) (ClaimMapEvidence, error) {
	body, err := source.loadArtifactContent(ctx, art)
	if err != nil {
		return ClaimMapEvidence{}, err
	}
	var cm ClaimMap
	if err := json.Unmarshal(body, &cm); err != nil {
		return ClaimMapEvidence{}, err
	}
	claims := make([]ClaimEvidence, len(cm.Claims))
	for i, c := range cm.Claims {
		claims[i] = ClaimEvidence{
			ClaimID: c.ClaimID, Subject: c.Subject,
			Predicate: c.Predicate, Value: c.Value, SourceRefs: c.SourceRefs,
		}
	}
	findings := make([]FindingEvidence, len(cm.Findings))
	for i, f := range cm.Findings {
		findings[i] = FindingEvidence{
			FindingID: f.FindingID, Code: string(f.Code),
			Severity: f.Severity, Subject: f.Subject,
			Predicate: f.Predicate, ClaimIDs: f.ClaimIDs, SourceRefs: f.SourceRefs,
		}
	}
	return ClaimMapEvidence{
		Claims: claims, Findings: findings,
		ContentHash: art.ContentHash, ObservedAt: observedAt,
	}, nil
}

// loadMaterialSnapshotEvidence parses a materials artifact manifest into
// MaterialSnapshotEvidence entries.
func (source StoreContextSource) loadMaterialSnapshotEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) ([]MaterialSnapshotEvidence, error) {
	body, err := source.loadArtifactContent(ctx, art)
	if err != nil {
		return nil, err
	}
	var manifest MaterialManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, err
	}
	result := make([]MaterialSnapshotEvidence, len(manifest.Materials))
	for i, m := range manifest.Materials {
		result[i] = MaterialSnapshotEvidence{
			MaterialID: m.MaterialID, Title: m.Title,
			SourceKind: string(m.SourceKind), SourceRef: m.SourceRef,
			ContentHash: m.ContentHash, UpdatedAt: m.UpdatedAt,
		}
	}
	return result, nil
}

// loadResearchPackEvidence parses a research_evidence_pack artifact into
// ResearchPackEvidence entries (one per claim in the pack).
func (source StoreContextSource) loadResearchPackEvidence(ctx context.Context, art writingstore.ArtifactRecord, _ time.Time) ([]ResearchPackEvidence, error) {
	body, err := source.loadArtifactContent(ctx, art)
	if err != nil {
		return nil, err
	}
	// The research evidence pack is a JSON object with "claims" and "evidence" arrays.
	var pack struct {
		Claims []struct {
			ClaimID      string   `json:"claim_id"`
			Text         string   `json:"text"`
			Kind         string   `json:"kind"`
			PaperID      string   `json:"paper_id"`
			ReviewStatus string   `json:"review_status"`
			EvidenceIDs  []string `json:"evidence_ids"`
		} `json:"claims"`
		Evidence []struct {
			EvidenceID string `json:"evidence_id"`
			Quote      string `json:"quote"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(body, &pack); err != nil {
		return nil, err
	}
	result := make([]ResearchPackEvidence, len(pack.Claims))
	for i, c := range pack.Claims {
		result[i] = ResearchPackEvidence{
			ClaimID: c.ClaimID, ClaimText: c.Text,
			Kind: c.Kind, PaperID: c.PaperID,
			ReviewStatus: c.ReviewStatus, EvidenceIDs: c.EvidenceIDs,
			// The pack revision this claim was read from: it must survive
			// into the envelope's source_evidence block (envelope hash
			// coverage, WP1/WP2 closure).
			ContentHash: art.ContentHash,
		}
	}
	return result, nil
}

// loadArtifactContent is a thin helper that loads artifact body bytes.
func (source StoreContextSource) loadArtifactContent(ctx context.Context, art writingstore.ArtifactRecord) ([]byte, error) {
	if source.Content == nil {
		return nil, fmt.Errorf("content gateway not configured")
	}
	return source.Content.Load(ctx, InputArtifact{
		ArtifactID:   art.ArtifactID,
		Version:      art.Version,
		ArtifactType: writingplan.ArtifactType(art.ArtifactType),
		ContentHash:  art.ContentHash,
		MediaType:    art.MediaType,
		ContentRef:   art.ContentRef,
	})
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
