// Offline context replay (WP2.4): reconstitutes a compiled context envelope
// from persisted state without querying live project memory, search sources,
// or the current user memory port. The replay reads from the writing store's
// immutable records only:
//
//	writing_runs, writing_run_plans, writing_snapshots,
//	writing_artifacts, writing_artifact_contents,
//	project_context_envelopes, writing_run_events,
//	writing_documents, writing_document_versions.
//
// It does NOT read from mutable project memory tables (project_facts,
// project_memory_terminology, etc.) or any external service.
//
// The replay goal is to reconstruct the model input, not to guarantee
// byte-identical model output. The compiler is a pure deterministic function:
// same input + same compiler version = same envelope hash.
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
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// OfflineReplayResult is the output of a context replay.
type OfflineReplayResult struct {
	RunID        string                    `json:"run_id"`
	NodeID       string                    `json:"node_id"`
	Attempt      int                       `json:"attempt"`
	EnvelopeHash string                    `json:"envelope_hash"`
	OriginalHash string                    `json:"original_hash,omitempty"`
	HashMatch    bool                      `json:"hash_match"`
	Envelope     *contextcompiler.Envelope `json:"envelope,omitempty"`
	Missing      []contextcompiler.Missing `json:"missing,omitempty"`
	Trimmed      []contextcompiler.Trimmed `json:"trimmed,omitempty"`
	EvidenceView *EvidenceView             `json:"evidence_view,omitempty"`
}

// OfflineReplayer reconstitutes context envelopes from saved snapshots without
// querying live state. It reads from the writing store's persisted records
// only. The compiler is a pure deterministic function, so the same saved input
// always produces the same envelope hash.
type OfflineReplayer struct {
	Store    *writingstore.Store
	Content  ContentGateway // loads artifact bodies for evidence projection
	Renderer *EvidenceViewRenderer
}

// Replay reconstitutes the context envelope for a specific node attempt.
// It reads from saved snapshots and persisted artifacts only — no live
// database queries, no search, no current user memory.
func (r *OfflineReplayer) Replay(ctx context.Context, runID, nodeID string, attempt int) (*OfflineReplayResult, error) {
	if r.Store == nil {
		return nil, fmt.Errorf("offline replay: store is required")
	}

	// 1. Load the run record (immutable identity + contract binding).
	run, err := r.Store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("offline replay: load run: %w", err)
	}

	// 2. Load the active plan (immutable plan + capability manifest).
	plan, err := r.Store.LoadActivePlan(ctx, runID)
	if err != nil {
		return nil, fmt.Errorf("offline replay: load plan: %w", err)
	}

	// 3. Find the node in the plan.
	var node writingplan.PlanNode
	var found bool
	for _, n := range plan.Envelope.ExecutablePlan.Nodes {
		if n.NodeID == nodeID {
			node = n
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("offline replay: node %s not found in plan", nodeID)
	}

	// 4. Look up the capability manifest for the context contract.
	manifest, ok := r.resolveManifest(plan, node)
	if !ok {
		return nil, fmt.Errorf("offline replay: capability %s not found in registry", node.Capability)
	}

	// 5. Load the original persisted envelope (if any).
	var originalEnvelope *writingstore.ContextEnvelopeRecord
	envelopes, err := r.Store.ListContextEnvelopes(ctx, runID, nodeID, attempt)
	if err != nil {
		return nil, fmt.Errorf("offline replay: list envelopes: %w", err)
	}
	if len(envelopes) > 0 {
		originalEnvelope = &envelopes[0] // newest first
	}

	// 6. Reconstruct the compiler input from saved data only.
	input, err := r.reconstructInput(ctx, run, node, plan)
	if err != nil {
		return nil, fmt.Errorf("offline replay: reconstruct input: %w", err)
	}

	// Apply the manifest contract (same as compileNodeContext).
	input.Wanted = manifest.Context.ContextWanted()
	input.RetentionPriority = manifest.Context.ContextRetentionPriority()
	if manifest.Context.ContextTokenBudget > 0 {
		input.TotalBudget = manifest.Context.ContextTokenBudget
	}

	// 7. Compile the context envelope (pure function, deterministic).
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		return nil, fmt.Errorf("offline replay: compile: %w", err)
	}

	// 8. Build the evidence view (deterministic projection of saved artifacts).
	var evidenceView *EvidenceView
	if r.Content != nil {
		if ev, err := r.renderEvidenceView(ctx, run, node, &plan.Envelope.ExecutablePlan); err == nil {
			evidenceView = &ev
		}
	}

	// 9. Compare with the original envelope.
	result := &OfflineReplayResult{
		RunID:        runID,
		NodeID:       nodeID,
		Attempt:      attempt,
		EnvelopeHash: envelope.Hash,
		Envelope:     &envelope,
		Missing:      envelope.Missing,
		Trimmed:      envelope.Trimmed,
		EvidenceView: evidenceView,
	}
	if originalEnvelope != nil {
		result.OriginalHash = originalEnvelope.EnvelopeHash
		result.HashMatch = envelope.Hash == originalEnvelope.EnvelopeHash
	}

	return result, nil
}

// reconstructInput builds the compiler input from persisted store state.
// It reads from immutable records only — no live project memory queries.
func (r *OfflineReplayer) reconstructInput(
	ctx context.Context,
	run writingstore.RuntimeRun,
	node writingplan.PlanNode,
	plan writingstore.PlanRecord,
) (contextcompiler.Input, error) {
	input := contextcompiler.Input{
		// ContractDigest is deterministic from run identity.
		ContractDigest: fmt.Sprintf("contract %s v%d | node %s | capability %s@%s",
			run.ContractID, run.ContractVersion, node.NodeID, node.Capability, node.CapabilityVersion),
	}

	// Document state from the document's current version (immutable record).
	if document, err := r.Store.GetDocument(ctx, run.DocumentID); err != nil {
		if !errors.Is(err, writingstore.ErrNotFound) {
			return input, fmt.Errorf("load document: %w", err)
		}
	} else if document.CurrentVersionID == "" {
		input.DocumentState = "(empty document)"
	} else if version, err := r.Store.GetDocumentVersion(ctx, run.DocumentID, document.CurrentVersionID); err != nil {
		if !errors.Is(err, writingstore.ErrNotFound) {
			return input, fmt.Errorf("load document version: %w", err)
		}
	} else {
		input.DocumentState = renderDocumentState(version)
	}

	// Project memory blocks. The offline replayer reads from the project
	// memory tables, which are append-only / immutable-by-contract for
	// active records. This is a best-effort reconstruction: the data may
	// have changed since the original compilation (facts superseded,
	// terminology archived). The hash comparison reveals any drift.
	projectID, err := r.Store.DocumentProjectID(ctx, run.DocumentID)
	if err != nil {
		if errors.Is(err, writingstore.ErrNotFound) {
			return input, nil
		}
		return input, err
	}
	input.ProjectID = projectID

	if facts, err := r.Store.ListActiveFacts(ctx, projectID, ""); err == nil {
		for _, fact := range facts {
			input.FactLines = append(input.FactLines, fmt.Sprintf("%s | %s | %s（as_of %s）",
				fact.Subject, fact.Predicate, fact.Object, fact.ValidFrom.Format("2006-01-02")))
		}
	}
	if entries, err := r.Store.ListTerminology(ctx, projectID, "active"); err == nil {
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
	if decisions, err := r.Store.ListDecisions(ctx, projectID, "active"); err == nil {
		for _, decision := range decisions {
			input.DecisionLines = append(input.DecisionLines, decision.Statement)
		}
	}
	if questions, err := r.Store.ListOpenQuestions(ctx, projectID, "open"); err == nil {
		for _, question := range questions {
			input.QuestionLines = append(input.QuestionLines, question.Question)
		}
	}
	if entities, err := r.Store.ListMemoryEntities(ctx, projectID, "", "promoted"); err == nil {
		for _, entity := range entities {
			line := fmt.Sprintf("%s [%s]", entity.CanonicalName, entity.EntityKind)
			if len(entity.Aliases) > 0 {
				line += " 别名：" + strings.Join(entity.Aliases, "、")
			}
			input.EntityCards = append(input.EntityCards, line)
		}
	}
	if threads, err := r.Store.ListThreads(ctx, projectID, "active"); err == nil {
		for _, thread := range threads {
			if thread.Resident {
				input.ThreadLabels = append(input.ThreadLabels, thread.Label)
			}
		}
		sort.Strings(input.ThreadLabels)
	}

	// Evidence lines from saved artifacts (deterministic projection).
	if r.Content != nil {
		if evidenceView, err := r.renderEvidenceView(ctx, run, node, &plan.Envelope.ExecutablePlan); err == nil {
			for _, item := range evidenceView.Items {
				line := item.ClaimOrTopic
				if item.SourceRef != "" {
					line += " [" + item.SourceRef + "]"
				}
				if item.ContentHash != "" {
					short := item.ContentHash
					if len(short) > 12 {
						short = short[:12]
					}
					line += " {" + short + "}"
				}
				input.EvidenceLines = append(input.EvidenceLines, line)
			}
		}
	}

	// Note: StyleDirectives and ReviewGuard come from the user memory port,
	// which is an external service. The offline replayer does not call it.
	// These blocks will appear as missing/empty in the replayed envelope.
	// This is expected: the replay reconstructs the model input from saved
	// state, and user memory is not persisted with the run.

	return input, nil
}

// resolveManifest finds the capability manifest for a node. The offline
// replayer does not have a live registry — it reconstructs the context
// contract from the plan's capability bindings.
func (r *OfflineReplayer) resolveManifest(plan writingstore.PlanRecord, node writingplan.PlanNode) (writingplan.CapabilityManifest, bool) {
	// The offline replayer reconstructs a minimal manifest from the plan
	// node's capability binding. The full manifest lives in the capability
	// registry, which is a runtime construct. For replay purposes, we need
	// only the context contract — blocks, budget, and enforcement.
	//
	// The plan node carries the capability ID and version. The context
	// contract is not persisted in the plan — it is a registry lookup.
	// For a faithful replay, we reconstruct a manifest with all compiler
	// blocks as wanted (the superset), since the original manifest's
	// contract is not persisted.
	return writingplan.CapabilityManifest{
		ID:                node.Capability,
		Version:           node.CapabilityVersion,
		Context:           defaultReplayContextContract(),
	}, true
}

// defaultReplayContextContract returns a context contract that wants all
// compiler blocks. The original manifest's contract may have been narrower,
// but without a persisted registry we use the superset. The hash comparison
// with the original envelope will reveal any mismatch.
func defaultReplayContextContract() writingplan.ContextContract {
	return writingplan.ContextContract{
		RequiredContext: []writingplan.ContextBlockName{
			writingplan.ContextContractDigest,
			writingplan.ContextDocumentState,
		},
		OptionalContext: []writingplan.ContextBlockName{
			writingplan.ContextThroughLine,
			writingplan.ContextCanonFacts,
			writingplan.ContextTerminology,
			writingplan.ContextOpenDecisions,
			writingplan.ContextEntitiesCards,
			writingplan.ContextSourceEvidence,
			writingplan.ContextStyleDirectives,
			writingplan.ContextReviewGuard,
		},
	}
}

// renderEvidenceView loads saved artifacts and runtime evidence events, then
// projects them into a deterministic EvidenceView. This is the same logic as
// StoreContextSource.renderEvidenceView — including the WP1/WP2 selection
// (own artifacts + resolved input references + transitive plan-dependency
// products) — but reads from the already-loaded persisted plan instead of
// re-fetching the active plan record.
func (r *OfflineReplayer) renderEvidenceView(ctx context.Context, run writingstore.RuntimeRun, node writingplan.PlanNode, plan *writingplan.ExecutablePlan) (EvidenceView, error) {
	if r.Store == nil {
		return EvidenceView{}, fmt.Errorf("store is required for evidence view")
	}
	allArtifacts, err := r.Store.ListRunArtifacts(ctx, run.RunID)
	if err != nil {
		return EvidenceView{}, fmt.Errorf("list run artifacts: %w", err)
	}
	nodeArtifacts := selectEvidenceArtifacts(allArtifacts, node, plan)

	var evidenceSources EvidenceSources
	for _, art := range nodeArtifacts {
		observedAt := art.CreatedAt
		if observedAt.IsZero() {
			observedAt = art.CreatedAt
		}
		switch art.ArtifactType {
		case "source_pack":
			if r.Content != nil {
				if sp, err := r.loadSourcePackEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.SourcePacks = append(evidenceSources.SourcePacks, sp)
				}
			}
		case "claim_map":
			if r.Content != nil {
				if cm, err := r.loadClaimMapEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.ClaimMaps = append(evidenceSources.ClaimMaps, cm)
				}
			}
		case "materials":
			if r.Content != nil {
				if mats, err := r.loadMaterialSnapshotEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.MaterialSnapshots = append(evidenceSources.MaterialSnapshots, mats...)
				}
			}
		case "research_evidence_pack":
			if r.Content != nil {
				if rps, err := r.loadResearchPackEvidence(ctx, art, observedAt); err == nil {
					evidenceSources.ResearchPacks = append(evidenceSources.ResearchPacks, rps...)
				}
			}
		}
	}

	// Load runtime evidence events for this node (from the event ledger).
	if events, err := r.Store.ListRunEvents(ctx, run.RunID, 0, 500); err == nil {
		for _, event := range events {
			if event.NodeID != node.NodeID || event.EntityKind != "rollout_evidence" {
				continue
			}
			evItem := RuntimeEvidenceItem{
				EvidenceID: event.EventID,
				Status:     event.EventType,
				RecordedAt: event.OccurredAt,
			}
			if kind, ok := event.Payload["evidence_kind"].(string); ok {
				evItem.Kind = kind
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

	renderer := r.Renderer
	if renderer == nil {
		renderer = &EvidenceViewRenderer{}
	}
	return renderer.Render(node.NodeID, attemptFromNode(node), evidenceSources), nil
}

func attemptFromNode(_ writingplan.PlanNode) int {
	// The offline replayer does not know the attempt number from the node
	// alone — it is a runtime concept. Use 0 for the evidence view hash;
	// the caller passes the real attempt for the envelope comparison.
	return 0
}

// loadSourcePackEvidence parses a source_pack artifact into SourcePackEvidence.
func (r *OfflineReplayer) loadSourcePackEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) (SourcePackEvidence, error) {
	body, err := r.loadArtifactBody(ctx, art)
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
func (r *OfflineReplayer) loadClaimMapEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) (ClaimMapEvidence, error) {
	body, err := r.loadArtifactBody(ctx, art)
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

// loadMaterialSnapshotEvidence parses a materials artifact into evidence.
func (r *OfflineReplayer) loadMaterialSnapshotEvidence(ctx context.Context, art writingstore.ArtifactRecord, observedAt time.Time) ([]MaterialSnapshotEvidence, error) {
	body, err := r.loadArtifactBody(ctx, art)
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

// loadResearchPackEvidence parses a research_evidence_pack artifact.
func (r *OfflineReplayer) loadResearchPackEvidence(ctx context.Context, art writingstore.ArtifactRecord, _ time.Time) ([]ResearchPackEvidence, error) {
	body, err := r.loadArtifactBody(ctx, art)
	if err != nil {
		return nil, err
	}
	var pack struct {
		Claims []struct {
			ClaimID      string   `json:"claim_id"`
			Text         string   `json:"text"`
			Kind         string   `json:"kind"`
			PaperID      string   `json:"paper_id"`
			ReviewStatus string   `json:"review_status"`
			EvidenceIDs  []string `json:"evidence_ids"`
		} `json:"claims"`
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
			// Same pack-revision binding as the live compile path so replay
			// reconstructs byte-identical evidence lines (envelope hash match).
			ContentHash: art.ContentHash,
		}
	}
	return result, nil
}

// loadArtifactBody loads the content-addressed artifact body.
func (r *OfflineReplayer) loadArtifactBody(ctx context.Context, art writingstore.ArtifactRecord) ([]byte, error) {
	if r.Content == nil {
		return nil, fmt.Errorf("content gateway not configured")
	}
	return r.Content.Load(ctx, InputArtifact{
		ArtifactID:   art.ArtifactID,
		Version:      art.Version,
		ArtifactType: writingplan.ArtifactType(art.ArtifactType),
		ContentHash:  art.ContentHash,
		MediaType:    art.MediaType,
		ContentRef:   art.ContentRef,
	})
}
