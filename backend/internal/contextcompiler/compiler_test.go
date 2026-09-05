package contextcompiler

import (
	"strings"
	"testing"
)

func richInput() Input {
	return Input{
		ProjectID:        "prj_test",
		ContractDigest:   "长文报告：检索优于重排的论证",
		ThreadLabels:     []string{"论点链：数据口径一致", "论点链：检索优于重排"},
		FactLines:        []string{"luminbuddy | state | 开源", "acme labs | location | 上海"},
		TerminologyLines: []string{"生成式检索（GSR）：由模型直接生成检索结果；禁止：AI搜索"},
		DecisionLines:    []string{"全文统一用“引擎”指代底层引擎"},
		QuestionLines:    []string{"数据口径以哪家年报为准？"},
		EntityCards:      []string{"Acme Labs [organization] 别名：ACME"},
		EvidenceLines:    []string{"doc_store#3：营收口径引用"},
		DocumentState:    "第二章已完成，第三章草稿中",
		StyleDirectives:  []string{"避免口语化", "段落不超过 5 句"},
		Wanted:           []string{"contract_digest", ResidentBlock, "canon_facts", "terminology", "open_decisions", "entities_cards", "source_evidence", "document_state", "style_directives"},
	}
}

func TestCompileIsDeterministicAndHashesRenderedBlocks(t *testing.T) {
	first, err := Compile(richInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := Compile(richInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash != second.Hash || first.CompilerVersion != CompilerVersion {
		t.Fatalf("non-deterministic: %s vs %s", first.Hash, second.Hash)
	}
	rendered, err := first.Render()
	if err != nil {
		t.Fatal(err)
	}
	// The hash must cover exactly the rendered blocks: metadata-only changes
	// (e.g. a caller mutating diagnostics afterwards) cannot shift it.
	third := first
	third.diagnostics = append(third.diagnostics, Diagnostic{Scope: "test", Message: "late note"})
	if third.Hash != first.Hash {
		t.Fatal("metadata changed the hash")
	}
	if !strings.Contains(rendered, "论点链：检索优于重排") {
		t.Fatalf("resident thread missing from render: %s", rendered)
	}
}

func TestCompileResidentLayerFailsClosedOnOverflow(t *testing.T) {
	input := richInput()
	for i := 0; i < ResidentBudget+1; i++ {
		input.ThreadLabels = append(input.ThreadLabels, "线索行")
	}
	if _, err := Compile(input); err == nil || !strings.Contains(err.Error(), "fails closed") {
		t.Fatalf("resident overflow err=%v", err)
	}
}

func TestCompileRetentionWalkRespectsBudgetUnderOverflow(t *testing.T) {
	input := richInput()
	// 1400 leaves 200 tokens of share budget beside the resident
	// protection; the fact block alone carries far more, so the retention
	// walk must trim it — down through reclaimed room, never past the
	// total budget.
	facts := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		facts = append(facts, "主体 | state | 状态行")
	}
	input.FactLines = facts
	input.TotalBudget = 1400
	envelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]Block{}
	for _, block := range envelope.Blocks {
		seen[block.Name] = block
	}
	// Resident survives untouched at its own budget.
	if seen[ResidentBlock].Tokens > ResidentBudget {
		t.Fatalf("resident=%d", seen[ResidentBlock].Tokens)
	}
	// The oversized block was honestly trimmed, never silently dropped.
	if len(envelope.Trimmed) == 0 {
		t.Fatalf("expected trims under a %d-token budget, got blocks=%v", 1400, envelope.Blocks)
	}
	for _, trimmed := range envelope.Trimmed {
		if trimmed.KeptTok >= trimmed.OriginalTok {
			t.Fatalf("trim not reducing: %#v", trimmed)
		}
		block := seen[trimmed.Block]
		if trimmed.KeptTok == 0 {
			if _, present := seen[trimmed.Block]; present {
				t.Fatalf("dropped block %s still assembled", trimmed.Block)
			}
			continue
		}
		if block.Tokens != trimmed.KeptTok {
			t.Fatalf("trim record disagrees with assembled block %s: %d vs %d", trimmed.Block, block.Tokens, trimmed.KeptTok)
		}
	}
	// M5 retention semantics: canon_facts reclaims the budget the resident
	// layer left unused and the share table's slack, so it legitimately
	// keeps more than its static share — but the total can never cross the
	// budget, and a block trimmed to nothing is an explicit missing entry.
	if seen["canon_facts"].Tokens <= retainedShare(t, 1400, "canon_facts") {
		t.Fatalf("canon_facts did not reclaim room: %d <= share %d", seen["canon_facts"].Tokens, retainedShare(t, 1400, "canon_facts"))
	}
	if envelope.TotalTokens > 1400 {
		t.Fatalf("total %d exceeds the budget", envelope.TotalTokens)
	}
	sum := 0
	for _, block := range envelope.Blocks {
		sum += block.Tokens
	}
	if envelope.TotalTokens != sum {
		t.Fatalf("total_tokens %d != sum of blocks %d", envelope.TotalTokens, sum)
	}
	missing := map[string]string{}
	for _, entry := range envelope.Missing {
		missing[entry.Block] = entry.Reason
	}
	for _, trimmed := range envelope.Trimmed {
		if trimmed.KeptTok == 0 && missing[trimmed.Block] != "dropped over budget" {
			t.Fatalf("dropped block %s not missing: %v", trimmed.Block, envelope.Missing)
		}
	}
	// Diagnostics stay out of the payload.
	rendered, err := envelope.Render()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rendered, "trimmed from") {
		t.Fatal("diagnostics leaked into the payload")
	}
}

// retainedShare mirrors the compiler's share derivation: the block's weight
// applied to the budget left after the resident protection. Keeping this
// computation in the test (not importing an unexported accessor) pins the
// weight table without letting the test read the answer from the trim
// record it is checking.
func retainedShare(t *testing.T, total int, block string) int {
	t.Helper()
	weights := map[string]int{
		"contract_digest": 12, "canon_facts": 22, "terminology": 10,
		"open_decisions": 10, "entities_cards": 12, "source_evidence": 14,
		"document_state": 12, "style_directives": 8,
	}
	sum := 0
	for _, name := range Blocks() {
		if name == ResidentBlock {
			continue
		}
		sum += weights[name]
	}
	remaining := total - ResidentBudget
	if remaining < len(Blocks()) {
		remaining = len(Blocks())
	}
	return remaining * weights[block] / sum
}

func TestCompileMissingBlocksAreExplicit(t *testing.T) {
	input := Input{
		ProjectID:      "prj_test",
		ContractDigest: "空项目首章",
		Wanted:         []string{"contract_digest", ResidentBlock, "canon_facts", "terminology"},
	}
	envelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]string{}
	for _, entry := range envelope.Missing {
		missing[entry.Block] = entry.Reason
	}
	if missing[ResidentBlock] != "no resident threads supplied" || missing["canon_facts"] != "no data supplied" || missing["terminology"] != "no data supplied" {
		t.Fatalf("missing=%v", envelope.Missing)
	}
	// The contract digest is the only block with data; under the allowlist the
	// undeclared blocks stay out entirely.
	if len(envelope.Blocks) != 1 || envelope.Blocks[0].Name != "contract_digest" {
		t.Fatalf("blocks=%v", envelope.Blocks)
	}
}

func TestCompileResidentThreadsAreSorted(t *testing.T) {
	input := richInput()
	input.ThreadLabels = []string{"乙线索", "甲线索"}
	envelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	// Code-point order: 乙 (U+4E59) sorts before 甲 (U+7532).
	for _, block := range envelope.Blocks {
		if block.Name == ResidentBlock && block.Body != "乙线索\n甲线索" {
			t.Fatalf("resident not sorted: %q", block.Body)
		}
	}
	rotated := input
	rotated.ThreadLabels = []string{"甲线索", "乙线索"}
	second, err := Compile(rotated)
	if err != nil {
		t.Fatal(err)
	}
	if second.Hash != envelope.Hash {
		t.Fatal("input order changed the hash: threads must be normalized")
	}
}

func TestCompileWantedIsAnAllowlist(t *testing.T) {
	// Without a manifest contract, every supplied block is assembled.
	open, err := Compile(richInput())
	if err != nil {
		t.Fatal(err)
	}
	if len(open.Blocks) < 8 {
		t.Fatalf("open compile=%v", open.Blocks)
	}
	// With a contract, undeclared blocks stay out even though data exists —
	// this is what makes a capability's context contract binding.
	input := richInput()
	input.Wanted = []string{"contract_digest", ResidentBlock, "canon_facts"}
	envelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, block := range envelope.Blocks {
		names[block.Name] = true
	}
	if !names["contract_digest"] || !names[ResidentBlock] || !names["canon_facts"] {
		t.Fatalf("declared blocks missing: %v", envelope.Blocks)
	}
	for _, undeclared := range []string{"terminology", "open_decisions", "entities_cards", "source_evidence", "document_state", "style_directives"} {
		if names[undeclared] {
			t.Fatalf("undeclared block %s was assembled", undeclared)
		}
	}
	if len(envelope.Missing) != 0 {
		t.Fatalf("supplied declared blocks must not be missing: %v", envelope.Missing)
	}
}

func TestCompileRejectsUnknownWantedBlock(t *testing.T) {
	input := richInput()
	input.Wanted = []string{"contract_digest", "secret_block"}
	if _, err := Compile(input); err == nil || !strings.Contains(err.Error(), "not a compiler block") {
		t.Fatalf("err=%v", err)
	}
	if !ValidBlock("canon_facts") || ValidBlock("secret_block") || len(Blocks()) != 9 {
		t.Fatalf("block registry mismatch: valid=%v blocks=%v", ValidBlock("canon_facts"), Blocks())
	}
}

// mixedFactLine is a representative rendered fact line: CJK subject,
// fullwidth punctuation, and Latin words.
const mixedFactLine = "引擎， uses engine ok"

func TestTokenCountIsSegmentationAware(t *testing.T) {
	// M5a (docs/18 §18.11): the M3 one-token-per-line approximation is
	// replaced. Pin the estimator's contract directly — a case table would
	// only restate the implementation.
	if got := tokenCount("主体状态行"); got != 5 {
		t.Fatalf("cjk: got %d, want 5 (one token per rune)", got)
	}
	if got := tokenCount("engine"); got != 2 {
		t.Fatalf("latin word of 6 runes: got %d, want ceil(6/4)=2", got)
	}
	if got := tokenCount("a cat"); got != 3 {
		t.Fatalf("two words plus space: got %d, want 3", got)
	}
	if got := tokenCount("引擎，推进。"); got != 6 {
		t.Fatalf("cjk plus fullwidth punctuation: got %d, want 6", got)
	}
	if got := tokenCount("甲\n乙"); got != 3 {
		t.Fatalf("newlines are separators: got %d, want 3", got)
	}
	if got := tokenCount(mixedFactLine); got != 10 {
		t.Fatalf("mixed line: got %d, want 10 (3 cjk + 1 punct + space + uses 1 + space + engine 2 + space + ok 1)", got)
	}
	// A one-line body of 40 Latin runes costs ~10 tokens, not 1 like M3.
	if got := tokenCount(strings.Repeat("word", 10)); got != 10 {
		t.Fatalf("40-rune latin line: got %d, want 10", got)
	}
}

func TestCompilePressureThresholds(t *testing.T) {
	// The resident protection sets a floor on legal budgets, so the fixture
	// needs enough content to sit between the thresholds of a legal budget.
	// 105 fact lines (~1470 tokens) at budget 2000 land at ~0.8 pressure
	// with nothing trimmed; at 1500 the pool binds and the walk compresses.
	input := richInput()
	for i := 0; i < 105; i++ {
		input.FactLines = append(input.FactLines, "主体 | state | 状态行")
	}
	low, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if low.Pressure() >= PressureWarnThreshold {
		t.Fatalf("fixture too rich for the low-pressure case: %.2f", low.Pressure())
	}
	for _, diagnostic := range low.Diagnostics() {
		if diagnostic.Scope == "pressure" {
			t.Fatalf("low-pressure envelope must not carry pressure diagnostics: %v", low.Diagnostics())
		}
	}

	// Shrink the budget until the same content sits between the thresholds
	// with nothing trimmed: the envelope must carry an out-of-band pressure
	// diagnostic, and since the payload is unchanged, the hash must not
	// change either. The walk is deterministic, so a linear scan over
	// budgets finds the band; if the fixture drifts, the scan fails loudly.
	warned := (*Envelope)(nil)
	for budget := 1500; budget <= 6000; budget += 10 {
		tight := input
		tight.TotalBudget = budget
		candidate, err := Compile(tight)
		if err != nil {
			t.Fatal(err)
		}
		if len(candidate.Trimmed) == 0 && candidate.Pressure() >= PressureWarnThreshold && candidate.Pressure() < PressureCompressThreshold {
			warned = &candidate
			break
		}
	}
	if warned == nil {
		t.Fatal("no budget lands the fixture in the warn band without trimming")
	}
	if warned.Hash != low.Hash {
		t.Fatal("pressure diagnostics changed the hash")
	}
	found := false
	for _, diagnostic := range warned.Diagnostics() {
		if diagnostic.Scope == "pressure" && strings.HasPrefix(diagnostic.Message, "warn:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected warn diagnostic, got %v", warned.Diagnostics())
	}

	// Cross the compress threshold with a still smaller budget: content now
	// exceeds the pool, the walk trims, and the diagnostic says compress.
	squeezed := input
	squeezed.TotalBudget = 1500
	compressed, err := Compile(squeezed)
	if err != nil {
		t.Fatal(err)
	}
	if compressed.Pressure() < PressureCompressThreshold {
		t.Fatalf("compress case pressure=%.2f", compressed.Pressure())
	}
	if len(compressed.Trimmed) == 0 {
		t.Fatal("compress fixture must trim")
	}
	found = false
	for _, diagnostic := range compressed.Diagnostics() {
		if diagnostic.Scope == "pressure" && strings.HasPrefix(diagnostic.Message, "compress:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected compress diagnostic, got %v", compressed.Diagnostics())
	}
}

func TestCompileRetentionPriorityOverridesOrder(t *testing.T) {
	// Three content-heavy blocks so that rank order, not just size, decides
	// who survives: under the default order the tail (document_state) is
	// trimmed while canon_facts and source_evidence keep most of the pool.
	rawDocumentState := strings.Repeat("第二章已完成，第三章草稿中\n", 80)
	input := richInput()
	for i := 0; i < 100; i++ {
		input.FactLines = append(input.FactLines, "主体 | state | 状态行")
		input.EvidenceLines = append(input.EvidenceLines, "doc_store#3：营收口径引用")
	}
	input.DocumentState = strings.TrimRight(rawDocumentState, "\n")
	input.TotalBudget = 2000
	defaultEnvelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	defaultTokens := map[string]int{}
	for _, block := range defaultEnvelope.Blocks {
		defaultTokens[block.Name] = block.Tokens
	}
	if defaultTokens["document_state"] >= tokenCount(input.DocumentState) {
		t.Fatalf("fixture: document_state not under pressure by default: %d", defaultTokens["document_state"])
	}

	// Elevate the tail: document_state ranked first keeps its full content
	// while the blocks the default order protected yield room instead.
	prioritized := input
	prioritized.RetentionPriority = []string{"style_directives", "document_state"}
	prioritizedEnvelope, err := Compile(prioritized)
	if err != nil {
		t.Fatal(err)
	}
	if prioritizedEnvelope.TotalTokens > 2000 {
		t.Fatalf("prioritized total %d exceeds budget", prioritizedEnvelope.TotalTokens)
	}
	prioritizedTokens := map[string]int{}
	for _, block := range prioritizedEnvelope.Blocks {
		prioritizedTokens[block.Name] = block.Tokens
	}
	if got := prioritizedTokens["document_state"]; got != tokenCount(input.DocumentState) {
		t.Fatalf("elevated document_state trimmed: %d != %d", got, tokenCount(input.DocumentState))
	}
	if got := prioritizedTokens["canon_facts"]; got >= defaultTokens["canon_facts"] {
		t.Fatalf("default-protected canon_facts did not yield: %d vs %d", got, defaultTokens["canon_facts"])
	}
	// An elevated block that fits anyway is untouched.
	if prioritizedTokens["style_directives"] != defaultTokens["style_directives"] {
		t.Fatalf("style_directives changed without pressure: %d vs %d", prioritizedTokens["style_directives"], defaultTokens["style_directives"])
	}
	// The resident layer always outranks any declared priority.
	if prioritizedTokens[ResidentBlock] != defaultTokens[ResidentBlock] {
		t.Fatalf("resident layer changed under retention priority: %d vs %d", prioritizedTokens[ResidentBlock], defaultTokens[ResidentBlock])
	}
	// Determinism: the same priority produces the same hash.
	again, err := Compile(prioritized)
	if err != nil {
		t.Fatal(err)
	}
	if again.Hash != prioritizedEnvelope.Hash {
		t.Fatal("retention priority broke determinism")
	}
}

func TestCompileRejectsUnknownRetentionPriority(t *testing.T) {
	input := richInput()
	input.RetentionPriority = []string{"canon_facts", "secret_block"}
	if _, err := Compile(input); err == nil || !strings.Contains(err.Error(), "not a compiler block") {
		t.Fatalf("err=%v", err)
	}
	// Duplicates and blanks are ignored, not rejected.
	input.RetentionPriority = []string{"canon_facts", "", "canon_facts"}
	if _, err := Compile(input); err != nil {
		t.Fatalf("benign duplicates rejected: %v", err)
	}
}

func TestCompileVersion2AccountsByTokensNotLines(t *testing.T) {
	// Pin the estimator swap (M3 → M5): accounting must reflect
	// tokenization, not line counts, and the version must say 2.
	input := richInput()
	envelope, err := Compile(input)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.CompilerVersion != 2 {
		t.Fatalf("compiler version %d", envelope.CompilerVersion)
	}
	checked := 0
	for _, block := range envelope.Blocks {
		if block.Name != ResidentBlock {
			continue
		}
		checked++
		lines := strings.Count(block.Body, "\n") + 1
		if block.Tokens == lines && tokenCount(block.Body) != lines {
			t.Fatalf("resident accounting is line-based: tokens=%d lines=%d", block.Tokens, lines)
		}
		if block.Tokens != tokenCount(block.Body) {
			t.Fatalf("block tokens %d disagree with estimator %d", block.Tokens, tokenCount(block.Body))
		}
	}
	if checked == 0 {
		t.Fatal("resident block absent")
	}
}
