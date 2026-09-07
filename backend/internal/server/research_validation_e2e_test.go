package server

// T07 E2E (docs/plans/2026-09-07-research-review-integration.md §T07): the
// research_review template drives the full chain over the real HTTP surface
// with the T06 fake worker, the REAL T07 citation validator, and the REAL
// mechanical quality gate (ResearchQualityGateRunner wrapping the scripted
// quality runner — the production wiring shape). Four delivery-gate scenarios:
//  1. draft-marker tamper → invalid_citation blocker → quality gate rejects →
//     no revision_set, run paused, error visible on node.failed;
//  2. pack block-hash tamper → hash-tamper blocker → EVIDENCE_INVALID;
//  3. abstract-scope evidence under full_text_required → review finding,
//     NOT blocking, quality report shows it, finalize succeeds;
//  4. clean path → finalize succeeds, bibliography numbering matches the
//     citation bindings.
//
// Tamper injection: the persisted artifact rows and the content store are
// immutable by design (migrations 090/104 triggers), so the only mutable seam
// is an executor's returned artifacts before the orchestrator commits them.
// The wrappers tamper exactly there and delegate to the REAL executors — the
// tampered state is indistinguishable from a buggy/malicious upstream commit
// and flows through the unmodified validator → gate → finalize chain.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// ── Harness ─────────────────────────────────────────────────────────────────

type t07Harness struct {
	*t06Harness
}

// newT07E2EHarness is the T06 harness with the real mechanical quality gate
// wrapped around the scripted quality runner. The full real research executor
// set is mounted in one remount; wrapRead/wrapDraft inject tampering at the
// read/draft executor seams (nil keeps them untouched).
func newT07E2EHarness(t *testing.T, papers int, wrapRead, wrapDraft func(inner writingruntime.Executor, content writingruntime.ContentGateway) writingruntime.Executor) *t07Harness {
	t.Helper()
	h := &t07Harness{t06Harness: newT06E2EHarness(t, papers, 0)}
	h.qualityWrap = func(inner writingruntime.LegacyNodeRunner) writingruntime.LegacyNodeRunner {
		return writingruntime.ResearchQualityGateRunner{Inner: inner}
	}
	store := h.store
	canonical := writingruntime.WritingStoreContentGateway{Store: store}
	discover, err := writingruntime.NewResearchDiscoverExecutor(h.worker, canonical)
	if err != nil {
		t.Fatal(err)
	}
	read, err := writingruntime.NewResearchReadExecutor(h.worker, canonical, store, h.budget)
	if err != nil {
		t.Fatal(err)
	}
	var readExecutor writingruntime.Executor = read
	if wrapRead != nil {
		readExecutor = wrapRead(read, canonical)
	}
	outline, err := writingruntime.NewResearchOutlineExecutor(canonical, nil)
	if err != nil {
		t.Fatal(err)
	}
	draft, err := writingruntime.NewResearchDraftExecutor(canonical, h.generator)
	if err != nil {
		t.Fatal(err)
	}
	var draftExecutor writingruntime.Executor = draft
	if wrapDraft != nil {
		draftExecutor = wrapDraft(draft, canonical)
	}
	citations, err := writingruntime.NewResearchCitationValidator(canonical, store)
	if err != nil {
		t.Fatal(err)
	}
	fact, err := writingruntime.NewResearchFactValidator(canonical, nil)
	if err != nil {
		t.Fatal(err)
	}
	h.remount(t, map[string]writingruntime.Executor{
		"engine.step.research_discover":  discover,
		"engine.step.research_read":      readExecutor,
		"engine.step.research_outline":   outline,
		"engine.step.research_draft":     draftExecutor,
		"engine.step.research_citations": citations,
		"engine.step.research_fact":      fact,
	})
	return h
}

// ── Tamper helpers (executor-output seam) ───────────────────────────────────

// t07MutateArtifact loads a returned artifact's staged body, applies mutate,
// re-stages the mutated bytes, and re-points the draft's content identity.
func t07MutateArtifact(content writingruntime.ContentGateway, draft *writingruntime.OutputArtifactDraft,
	mutate func([]byte) []byte) error {
	body, err := content.Load(context.Background(), writingruntime.InputArtifact{
		ArtifactID: "art_t07_tmp", Version: 1, ContentHash: draft.ContentHash,
		MediaType: draft.MediaType, ContentRef: draft.ContentRef})
	if err != nil {
		return err
	}
	mutated := mutate(body)
	ref, hash, err := content.Stage(context.Background(), "t07-tamper:"+draft.OutputKey, draft.MediaType, mutated)
	if err != nil {
		return err
	}
	draft.ContentHash = hash
	draft.ContentRef = ref
	return nil
}

// t07DropFirstMarker removes the first citation marker (with its leading
// space) from the draft text.
func t07DropFirstMarker(body []byte) []byte {
	text := string(body)
	start := strings.Index(text, " [@ev_")
	if start < 0 {
		start = strings.Index(text, "[@ev_")
	}
	if start < 0 {
		return body
	}
	end := start + strings.IndexByte(text[start:], ']')
	return []byte(text[:start] + text[end+1:])
}

// t07ReadTamperer wraps the read executor: the returned evidence pack is
// mutated before the orchestrator commits it, so every downstream artifact
// (approval, outline, draft, citation index) binds the tampered pack — the
// state a corrupted read commit would produce.
func t07ReadTamperer(mode string) func(inner writingruntime.Executor, content writingruntime.ContentGateway) writingruntime.Executor {
	return func(inner writingruntime.Executor, content writingruntime.ContentGateway) writingruntime.Executor {
		return &t07ReadTamperExecutor{inner: inner, content: content, mode: mode}
	}
}

type t07ReadTamperExecutor struct {
	inner   writingruntime.Executor
	content writingruntime.ContentGateway
	mode    string // "block_hash" | "scope_flip"
}

func (executor *t07ReadTamperExecutor) Descriptor() writingruntime.ExecutorDescriptor {
	return executor.inner.Descriptor()
}

func (executor *t07ReadTamperExecutor) Execute(ctx context.Context, request writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	result, err := executor.inner.Execute(ctx, request)
	if err != nil {
		return result, err
	}
	for i := range result.Artifacts {
		if result.Artifacts[i].ArtifactType != "research_evidence_pack" {
			continue
		}
		pack := result.Artifacts[i]
		if err := t07MutateArtifact(executor.content, &pack, func(body []byte) []byte {
			var decoded writingkernel.ResearchEvidencePack
			if err := json.Unmarshal(body, &decoded); err != nil {
				return body
			}
			if len(decoded.Evidence) == 0 {
				return body
			}
			switch executor.mode {
			case "block_hash":
				// Flip the recorded block hash to another valid sha256: the
				// pack still validates structurally, but no longer matches
				// the parsed blocks frozen at read time (class c).
				flipped := []byte(decoded.Evidence[0].BlockHash)
				if len(flipped) > 9 {
					flipped[7], flipped[8], flipped[9] = 'f', 'f', 'e'
				}
				decoded.Evidence[0].BlockHash = string(flipped)
			case "scope_flip":
				decoded.Evidence[0].EvidenceScope = writingkernel.EvidenceScopeAbstract
			}
			encoded, err := json.Marshal(decoded)
			if err != nil {
				return body
			}
			return encoded
		}); err != nil {
			return writingruntime.ExecutionResult{}, err
		}
		result.Artifacts[i] = pack
	}
	return result, nil
}

// t07DraftTamperExecutor wraps the draft executor: the committed draft loses
// one marker while the citation index keeps its entries but re-binds to the
// tampered draft hash — the orphaned citation the validator must catch.
type t07DraftTamperExecutor struct {
	inner   writingruntime.Executor
	content writingruntime.ContentGateway
}

func (executor *t07DraftTamperExecutor) Descriptor() writingruntime.ExecutorDescriptor {
	return executor.inner.Descriptor()
}

func (executor *t07DraftTamperExecutor) Execute(ctx context.Context, request writingruntime.ExecutionRequest) (writingruntime.ExecutionResult, error) {
	result, err := executor.inner.Execute(ctx, request)
	if err != nil {
		return result, err
	}
	tamperedDraftHash := ""
	for i := range result.Artifacts {
		switch result.Artifacts[i].ArtifactType {
		case "full_draft":
			draft := result.Artifacts[i]
			if err := t07MutateArtifact(executor.content, &draft, t07DropFirstMarker); err != nil {
				return writingruntime.ExecutionResult{}, err
			}
			tamperedDraftHash = draft.ContentHash
			result.Artifacts[i] = draft
		case "research_citation_index":
			index := result.Artifacts[i]
			if err := t07MutateArtifact(executor.content, &index, func(body []byte) []byte {
				var decoded writingkernel.ResearchCitationIndex
				if err := json.Unmarshal(body, &decoded); err != nil {
					return body
				}
				// Re-bind the index to the tampered draft so the draft-hash
				// binding holds and the orphaned-entry finding is the precise
				// result (not a generic binding failure).
				if tamperedDraftHash != "" {
					decoded.DraftHash = tamperedDraftHash
				}
				encoded, err := json.Marshal(decoded)
				if err != nil {
					return body
				}
				return encoded
			}); err != nil {
				return writingruntime.ExecutionResult{}, err
			}
			result.Artifacts[i] = index
		}
	}
	return result, nil
}

// ── Shared chain driver ─────────────────────────────────────────────────────

// t07DriveToGates decides both research gates; afterwards the run proceeds
// into draft → citations → quality → finalize under the test's control.
func (h *t07Harness) t07DriveToGates(t *testing.T, runID string, envelope writingplan.WritingPlanEnvelope) {
	t.Helper()
	evidenceGate := h.advanceToGate(t, runID, "evidence")
	h.decideGate(t, runID, evidenceGate, envelope)
	outlineGate := h.advanceToGate(t, runID, "outline")
	h.decideGate(t, runID, outlineGate, envelope)
}

// ── Scenario 1: draft-marker tamper blocks formal delivery ─────────────────

func TestT07InvalidCitationBlocksFinalize(t *testing.T) {
	h := newT07E2EHarness(t, 1, nil,
		func(inner writingruntime.Executor, content writingruntime.ContentGateway) writingruntime.Executor {
			return &t07DraftTamperExecutor{inner: inner, content: content}
		})
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	h.t07DriveToGates(t, runID, envelope)

	// The quality node fails closed; the run pauses cleanly with the error on
	// the attempt ledger.
	h.waitForNodeAttempt(t, runID, "node_quality", "failed", 5)
	if status := h.httpStatus(t, runID); status != "paused" {
		t.Fatalf("run ended as %q, want paused after the quality gate rejection", status)
	}
	t07AssertNoRevisionSet(t, h.store, runID)
	t07AssertGateErrorCode(t, h.store, runID, "EVIDENCE_INVALID")
	details := t07LoadDetails(t, h.store, runID)
	if len(details.InvalidCitations) == 0 {
		t.Fatal("research_validation_details lacks invalid_citations")
	}
	t07AssertQualityReportVisible(t, h.store, runID)
}

// ── Scenario 2: pack hash tamper → EVIDENCE_INVALID ─────────────────────────

func TestT07PackHashTamperBlocksDelivery(t *testing.T) {
	h := newT07E2EHarness(t, 1, t07ReadTamperer("block_hash"), nil)
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	h.t07DriveToGates(t, runID, envelope)

	h.waitForNodeAttempt(t, runID, "node_quality", "failed", 5)
	if status := h.httpStatus(t, runID); status != "paused" {
		t.Fatalf("run ended as %q, want paused", status)
	}
	t07AssertNoRevisionSet(t, h.store, runID)
	t07AssertGateErrorCode(t, h.store, runID, "EVIDENCE_INVALID")
	details := t07LoadDetails(t, h.store, runID)
	found := false
	for _, finding := range details.Findings {
		if finding.Type == writingruntime.FindingHashTamper {
			found = true
		}
	}
	if !found {
		t.Fatalf("hash-tamper finding missing from details: %+v", details.Findings)
	}
}

// ── Scenario 3: scope overclaim → review, not blocking ──────────────────────

func TestT07ScopeOverclaimNeedsReviewButFinalizes(t *testing.T) {
	h := newT07E2EHarness(t, 1, t07ReadTamperer("scope_flip"), nil)
	// The scope-overclaim scenario needs a paper that was genuinely read as
	// full text (otherwise full_text_required would reject the workset at
	// the read node before any citation exists).
	h.worker.mu.Lock()
	h.worker.withFullText = true
	h.worker.mu.Unlock()
	// full_text_required is what makes an abstract-scope citation an
	// overclaim (contracts.md §1).
	fixture := h.fixtureMutate(t, func(contract *writingkernel.WritingContract) {
		contract.Research.EvidenceRequirement = writingkernel.EvidenceRequirementFullTextRequired
	})
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	h.t07DriveToGates(t, runID, envelope)

	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed (scope findings must not block)", status)
	}
	details := t07LoadDetails(t, h.store, runID)
	if len(details.ScopeOverclaims) == 0 {
		t.Fatalf("scope overclaims empty: %+v", details.Findings)
	}
	t07AssertRevisionSet(t, h.store, runID)
	t07AssertQualityWarning(t, h.store, runID, writingruntime.FindingScopeOverclaim)
}

// ── Scenario 4: clean path ──────────────────────────────────────────────────

func TestT07CleanPathFinalizesWithBibliography(t *testing.T) {
	h := newT07E2EHarness(t, 2, nil, nil)
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	h.t07DriveToGates(t, runID, envelope)

	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("run ended as %q, want completed", status)
	}
	t07AssertRevisionSet(t, h.store, runID)
	details := t07LoadDetails(t, h.store, runID)
	if len(details.InvalidCitations) != 0 {
		t.Fatalf("clean path reported invalid citations: %v", details.InvalidCitations)
	}
	if len(details.Bibliography) == 0 {
		t.Fatal("bibliography projection missing on the clean path")
	}
	// Numbering follows first-citation order; the evidence bindings stay the
	// authority (rendering may renumber freely).
	for i, entry := range details.Bibliography {
		if entry.Number != i+1 {
			t.Fatalf("bibliography number %d at index %d", entry.Number, i)
		}
		if len(entry.CitationIDs) == 0 || entry.Title == "" {
			t.Fatalf("bibliography entry %d lost its data: %+v", i, entry)
		}
	}
	// Every draft marker's paper is covered by the projection.
	draftBody, ok := t07RunArtifact(t, h.store, runID, "full_draft")
	if !ok {
		t.Fatal("full_draft artifact missing")
	}
	for _, marker := range strings.Split(string(draftBody), "[@")[1:] {
		id := marker[:strings.IndexByte(marker, ']')]
		if !strings.HasPrefix(id, "ev_") {
			continue
		}
		if !t07MarkerProjected(t, h.store, runID, id) {
			t.Fatalf("draft marker %s missing from the bibliography projection", id)
		}
	}
}

// t07MarkerProjected resolves one draft marker through the run's pack → paper
// and checks the bibliography covers that paper.
func t07MarkerProjected(t *testing.T, store *writingstore.Store, runID, evidenceID string) bool {
	t.Helper()
	packBody, ok := t07RunArtifact(t, store, runID, "research_evidence_pack")
	if !ok {
		t.Fatal("evidence pack missing")
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(packBody, &pack); err != nil {
		t.Fatal(err)
	}
	paperID := ""
	for _, evidence := range pack.Evidence {
		if evidence.EvidenceID == evidenceID {
			paperID = evidence.PaperID
		}
	}
	if paperID == "" {
		return false
	}
	for _, entry := range t07LoadDetails(t, store, runID).Bibliography {
		if entry.PaperID == paperID {
			return true
		}
	}
	return false
}

// ── Assertions ──────────────────────────────────────────────────────────────

func t07RunArtifact(t *testing.T, store *writingstore.Store, runID, artifactType string) ([]byte, bool) {
	t.Helper()
	artifacts, err := store.ListRunArtifacts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactType != artifactType {
			continue
		}
		_, body, err := store.GetArtifactContent(context.Background(), artifact.ContentHash)
		if err != nil {
			t.Fatal(err)
		}
		return body, true
	}
	return nil, false
}

func t07AssertNoRevisionSet(t *testing.T, store *writingstore.Store, runID string) {
	t.Helper()
	if _, ok := t07RunArtifact(t, store, runID, "revision_set"); ok {
		t.Fatal("finalize produced a revision_set behind a blocked citation report")
	}
}

func t07AssertRevisionSet(t *testing.T, store *writingstore.Store, runID string) {
	t.Helper()
	if _, ok := t07RunArtifact(t, store, runID, "revision_set"); !ok {
		t.Fatal("finalize produced no revision_set on the allowed path")
	}
}

// t07AssertGateErrorCode checks the failed quality attempt recorded the typed
// gate rejection on node.failed.
func t07AssertGateErrorCode(t *testing.T, store *writingstore.Store, runID, code string) {
	t.Helper()
	events, err := store.ListRunEvents(context.Background(), runID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.EventType != "node.failed" {
			continue
		}
		payload, _ := json.Marshal(event.Payload)
		var decoded struct {
			ErrorCode string `json:"error_code"`
		}
		_ = json.Unmarshal(payload, &decoded)
		if decoded.ErrorCode == code {
			return
		}
	}
	t.Fatalf("no node.failed event with error_code %s", code)
}

func t07LoadDetails(t *testing.T, store *writingstore.Store, runID string) writingruntime.ResearchValidationDetails {
	t.Helper()
	body, ok := t07RunArtifact(t, store, runID, "research_validation_details")
	if !ok {
		t.Fatal("research_validation_details artifact missing")
	}
	var details writingruntime.ResearchValidationDetails
	if err := json.Unmarshal(body, &details); err != nil {
		t.Fatal(err)
	}
	return details
}

// t07AssertQualityReportVisible checks the user-facing finding surfaces exist
// behind the blocked run: the evidence report (with the blocker mapping) and
// the details artifact. The quality_report itself is NOT produced when the
// gate rejects the node before any report is committed — the rejection rides
// node.failed plus the validator artifacts (T07 acceptance: the user sees the
// specific findings, not just a score).
func t07AssertQualityReportVisible(t *testing.T, store *writingstore.Store, runID string) {
	t.Helper()
	for _, artifactType := range []string{"evidence_report", "research_validation_details"} {
		if _, ok := t07RunArtifact(t, store, runID, artifactType); !ok {
			t.Fatalf("%s artifact missing behind the blocked run", artifactType)
		}
	}
	reportBody, _ := t07RunArtifact(t, store, runID, "evidence_report")
	var report struct {
		Validator string `json:"validator"`
		Passed    bool   `json:"passed"`
		Issues    []struct {
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(reportBody, &report); err != nil {
		t.Fatalf("evidence report undecodable: %v", err)
	}
	if report.Validator != "core.research.validate.citations" || report.Passed {
		t.Fatalf("evidence report not a blocked citations report: %+v", report)
	}
	blocker := false
	for _, issue := range report.Issues {
		if issue.Severity == "blocker" {
			blocker = true
		}
	}
	if !blocker {
		t.Fatal("evidence report carries no blocker issue")
	}
}

func t07AssertQualityWarning(t *testing.T, store *writingstore.Store, runID, wantType string) {
	t.Helper()
	body, ok := t07RunArtifact(t, store, runID, "quality_report")
	if !ok {
		t.Fatal("quality_report artifact missing")
	}
	var report struct {
		Issues []struct {
			Type string `json:"type"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatal(err)
	}
	for _, issue := range report.Issues {
		if issue.Type == wantType {
			return
		}
	}
	t.Fatalf("quality report lacks a %q issue: %s", wantType, string(body))
}
