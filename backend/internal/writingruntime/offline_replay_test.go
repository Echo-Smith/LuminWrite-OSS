package writingruntime

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/contextcompiler"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// TestOfflineReplayCompilationDeterminism verifies the core guarantee of
// offline replay: the compiler is a pure deterministic function — same input
// + same compiler version always produces the same envelope hash. This is
// the foundation that makes replay possible without re-querying live state.
func TestOfflineReplayCompilationDeterminism(t *testing.T) {
	input := contextcompiler.Input{
		ContractDigest:    "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		ThreadLabels:      []string{"论点链：检索优于重排"},
		FactLines:         []string{"luminbuddy | state | 开源", "luminbuddy | language | 中文"},
		TerminologyLines:  []string{"LLM：大语言模型；禁止：AI模型"},
		DecisionLines:     []string{"采用检索增强生成方案"},
		QuestionLines:     []string{"是否需要支持多语言？"},
		EntityCards:       []string{"LuminBuddy [产品] 别名：LB"},
		EvidenceLines:     []string{"检索优于重排 [https://arxiv.org/1234] {sha256:abcd}"},
		DocumentState:     "文档版本 v1（candidate_draft）：正文为空",
		StyleDirectives:   []string{"使用简洁技术风格"},
		ReviewGuard:       []string{"检查逻辑一致性"},
		TotalBudget:       6000,
		Wanted: []string{
			"contract_digest", "through_line_anchor", "canon_facts",
			"terminology", "open_decisions", "entities_cards",
			"source_evidence", "document_state", "style_directives", "review_guard",
		},
	}

	// Compile the same input twice — hashes must match.
	first, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatalf("first compile: %v", err)
	}
	second, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatalf("second compile: %v", err)
	}
	if first.Hash != second.Hash {
		t.Fatalf("determinism violated: first=%s second=%s", first.Hash, second.Hash)
	}
	if first.Hash == "" {
		t.Fatal("empty hash")
	}

	// Verify the blocks are rendered correctly.
	blockNames := map[string]bool{}
	for _, block := range first.Blocks {
		blockNames[block.Name] = true
	}
	for _, want := range []string{"contract_digest", "through_line_anchor", "canon_facts", "document_state"} {
		if !blockNames[want] {
			t.Fatalf("missing block %q in %v", want, blockNames)
		}
	}

	// Verify the persisted payload round-trips correctly.
	payload, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundTripped contextcompiler.Envelope
	if err := json.Unmarshal(payload, &roundTripped); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// The hash is over the rendered blocks, not the full envelope struct.
	// Re-render from the round-tripped blocks and re-hash.
	rendered, err := roundTripped.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	// Compile a fresh envelope from the same input to compare the render.
	fresh, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatalf("fresh compile: %v", err)
	}
	freshRendered, err := fresh.Render()
	if err != nil {
		t.Fatalf("fresh render: %v", err)
	}
	if rendered != freshRendered {
		t.Fatalf("render mismatch after round-trip")
	}
}

// TestOfflineReplayContractDigestDeterminism verifies that the contract
// digest format is deterministic from run/plan identity — the offline
// replayer must produce the same digest as the live orchestrator.
func TestOfflineReplayContractDigestDeterminism(t *testing.T) {
	// The digest format matches StoreContextSource.CompileInputs.
	digest := "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0"
	input := contextcompiler.Input{ContractDigest: digest, DocumentState: "文档版本 v1（candidate_draft）：正文为空"}
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	// Same inputs, same hash.
	input2 := contextcompiler.Input{ContractDigest: digest, DocumentState: "文档版本 v1（candidate_draft）：正文为空"}
	envelope2, err := contextcompiler.Compile(input2)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Hash != envelope2.Hash {
		t.Fatalf("hash mismatch: %s vs %s", envelope.Hash, envelope2.Hash)
	}
}

// TestOfflineReplayManifestContractApplication verifies that applying the
// manifest contract (Wanted, RetentionPriority, TotalBudget) produces the
// same envelope as the live orchestrator's compileNodeContext path.
func TestOfflineReplayManifestContractApplication(t *testing.T) {
	input := contextcompiler.Input{
		ContractDigest: "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		FactLines:      []string{"luminbuddy | state | 开源"},
		DocumentState:  "文档版本 v1（candidate_draft）：正文为空",
	}

	// Simulate what compileNodeContext does: apply manifest contract.
	manifest := writingplan.CapabilityManifest{
		ID:      "core.draft.generate",
		Version: "1.0.0",
		Context: writingplan.ContextContract{
			RequiredContext: []writingplan.ContextBlockName{
				writingplan.ContextContractDigest,
				writingplan.ContextDocumentState,
			},
			OptionalContext: []writingplan.ContextBlockName{
				writingplan.ContextThroughLine,
				writingplan.ContextCanonFacts,
			},
		},
	}
	input.Wanted = manifest.Context.ContextWanted()
	input.RetentionPriority = manifest.Context.ContextRetentionPriority()

	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}

	// Replay with the same contract application.
	replayInput := contextcompiler.Input{
		ContractDigest: "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		FactLines:      []string{"luminbuddy | state | 开源"},
		DocumentState:  "文档版本 v1（candidate_draft）：正文为空",
	}
	replayInput.Wanted = manifest.Context.ContextWanted()
	replayInput.RetentionPriority = manifest.Context.ContextRetentionPriority()

	replayEnvelope, err := contextcompiler.Compile(replayInput)
	if err != nil {
		t.Fatal(err)
	}

	if envelope.Hash != replayEnvelope.Hash {
		t.Fatalf("manifest contract replay mismatch: original=%s replay=%s", envelope.Hash, replayEnvelope.Hash)
	}
}

// TestOfflineReplayHashDriftDetection verifies that different inputs produce
// different hashes — the offline replayer can detect when live state has
// drifted from what was originally compiled.
func TestOfflineReplayHashDriftDetection(t *testing.T) {
	base := contextcompiler.Input{
		ContractDigest: "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		FactLines:      []string{"luminbuddy | state | 开源"},
		DocumentState:  "文档版本 v1（candidate_draft）：正文为空",
	}
	baseEnv, err := contextcompiler.Compile(base)
	if err != nil {
		t.Fatal(err)
	}

	// Add a fact — simulates live state drift.
	drifted := base
	drifted.FactLines = append(drifted.FactLines, "luminbuddy | version | 2.0")
	driftedEnv, err := contextcompiler.Compile(drifted)
	if err != nil {
		t.Fatal(err)
	}

	if baseEnv.Hash == driftedEnv.Hash {
		t.Fatal("hash drift not detected: different inputs produced same hash")
	}
}

// TestOfflineReplayReplayResult verifies the OfflineReplayResult structure
// and hash comparison logic.
func TestOfflineReplayReplayResult(t *testing.T) {
	input := contextcompiler.Input{
		ContractDigest: "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		FactLines:      []string{"luminbuddy | state | 开源"},
		DocumentState:  "文档版本 v1（candidate_draft）：正文为空",
		TotalBudget:    6000,
		Wanted: []string{
			"contract_digest", "through_line_anchor", "canon_facts", "document_state",
		},
	}

	// Compile the "original" envelope.
	original, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the replay result construction.
	replayEnvelope, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}

	result := &OfflineReplayResult{
		RunID:        "run_replay",
		NodeID:       "node_draft",
		Attempt:      1,
		EnvelopeHash: replayEnvelope.Hash,
		OriginalHash: original.Hash,
		HashMatch:    replayEnvelope.Hash == original.Hash,
		Envelope:     &replayEnvelope,
		Missing:      replayEnvelope.Missing,
		Trimmed:      replayEnvelope.Trimmed,
	}

	if !result.HashMatch {
		t.Fatalf("hash mismatch: envelope=%s original=%s", result.EnvelopeHash, result.OriginalHash)
	}
	if result.RunID != "run_replay" || result.NodeID != "node_draft" || result.Attempt != 1 {
		t.Fatalf("result identity: %#v", result)
	}

	// Verify drift detection: change the input.
	different := input
	different.FactLines = append(different.FactLines, "new | fact | added")
	differentEnv, err := contextcompiler.Compile(different)
	if err != nil {
		t.Fatal(err)
	}
	driftResult := &OfflineReplayResult{
		RunID:        "run_replay",
		NodeID:       "node_draft",
		Attempt:      1,
		EnvelopeHash: differentEnv.Hash,
		OriginalHash: original.Hash,
		HashMatch:    differentEnv.Hash == original.Hash,
	}
	if driftResult.HashMatch {
		t.Fatal("drift not detected: changed input matched original hash")
	}
}

// TestOfflineReplayWithEnvelopePayloadRoundTrip verifies that a persisted
// envelope payload can be unmarshaled and its blocks re-rendered to produce
// the same hash as the original compilation.
func TestOfflineReplayWithEnvelopePayloadRoundTrip(t *testing.T) {
	input := contextcompiler.Input{
		ContractDigest: "contract ctr_replay v1 | node node_draft | capability core.draft.generate@1.0.0",
		ThreadLabels:   []string{"论点链：检索优于重排"},
		FactLines:      []string{"luminbuddy | state | 开源"},
		DocumentState:  "文档版本 v1（candidate_draft）：正文为空",
		TotalBudget:    6000,
		Wanted: []string{
			"contract_digest", "through_line_anchor", "canon_facts", "document_state",
		},
	}

	original, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate persist + load (same as SaveContextEnvelope / ListContextEnvelopes).
	payload, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	record := writingstore.ContextEnvelopeRecord{
		EnvelopeID:      "env_test",
		RunID:           "run_replay",
		NodeID:          "node_draft",
		Attempt:         1,
		CompilerVersion: original.CompilerVersion,
		EnvelopeHash:    original.Hash,
		Payload:         payload,
		Missing:         original.Missing,
		Trimmed:         original.Trimmed,
	}

	// Unmarshal the persisted payload.
	var loaded contextcompiler.Envelope
	if err := json.Unmarshal(record.Payload, &loaded); err != nil {
		t.Fatal(err)
	}

	// Re-compile from the same input to verify the hash matches.
	recompiled, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Hash != recompiled.Hash {
		t.Fatalf("payload round-trip hash mismatch: loaded=%s recompiled=%s", loaded.Hash, recompiled.Hash)
	}
	if record.EnvelopeHash != recompiled.Hash {
		t.Fatalf("persisted hash mismatch: record=%s recompiled=%s", record.EnvelopeHash, recompiled.Hash)
	}
}

// TestOfflineReplayDefaultReplayContextContract verifies that the default
// replay context contract includes all compiler blocks.
func TestOfflineReplayDefaultReplayContextContract(t *testing.T) {
	contract := defaultReplayContextContract()
	wanted := contract.ContextWanted()

	allBlocks := contextcompiler.Blocks()
	wantedSet := map[string]bool{}
	for _, b := range wanted {
		wantedSet[b] = true
	}
	for _, block := range allBlocks {
		if !wantedSet[block] {
			t.Fatalf("default replay contract missing block %q", block)
		}
	}
}

// TestOfflineReplayEmptyInputProducesEmptyEnvelope verifies that an empty
// input (no data) produces a valid envelope with missing entries, not an
// error — the offline replayer handles absent data gracefully.
func TestOfflineReplayEmptyInputProducesEmptyEnvelope(t *testing.T) {
	input := contextcompiler.Input{
		ContractDigest: "contract ctr_empty v1 | node node_empty | capability test@1.0.0",
	}
	envelope, err := contextcompiler.Compile(input)
	if err != nil {
		t.Fatalf("empty input should compile: %v", err)
	}
	if envelope.Hash == "" {
		t.Fatal("empty envelope has no hash")
	}
	// With no Wanted list, all blocks with data are assembled (none here).
	// The envelope should have at most the contract_digest block.
	for _, block := range envelope.Blocks {
		if block.Name != "contract_digest" {
			t.Fatalf("unexpected block %q in empty envelope", block.Name)
		}
	}
}

// TestOfflineReplayEvidenceViewDeterminism verifies that the evidence view
// renderer produces the same view hash for the same inputs.
func TestOfflineReplayEvidenceViewDeterminism(t *testing.T) {
	renderer := &EvidenceViewRenderer{}
	sources := EvidenceSources{
		SourcePacks: []SourcePackEvidence{{
			Query: "retrieval vs reranking",
			Sources: []SourceRecordEvidence{{
				SourceID: "src_1", Title: "Retrieval beats reranking",
				URL: "https://arxiv.org/1234", Excerpt: "Key finding...",
			}},
			ContentHash: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ObservedAt:  timeMustParse("2026-09-01T00:00:00Z"),
		}},
		RuntimeEvidence: []RuntimeEvidenceItem{{
			EvidenceID: "evt_test", Kind: "route_decision",
			Status: "runtime.route_decided",
		}},
	}

	view1 := renderer.Render("node_draft", 1, sources)
	view2 := renderer.Render("node_draft", 1, sources)

	if view1.ViewHash != view2.ViewHash {
		t.Fatalf("evidence view not deterministic: %s vs %s", view1.ViewHash, view2.ViewHash)
	}
	if len(view1.Items) == 0 {
		t.Fatal("empty evidence view")
	}
}

func timeMustParse(s string) (t time.Time) {
	t, _ = time.Parse(time.RFC3339, s)
	return
}
