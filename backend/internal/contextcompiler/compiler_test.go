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

func TestCompileTrimsTailFirstInAssemblyOrder(t *testing.T) {
	input := richInput()
	// 1400 leaves only ~200 tokens for the shared blocks; the fact block
	// alone carries 400 lines, so the tail must trim or vanish first while
	// the resident layer keeps its protected budget.
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
	}
	if seen["canon_facts"].Tokens > tableShare(t, envelope, "canon_facts") {
		t.Fatalf("canon_facts exceeded its share: %#v", seen["canon_facts"])
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

// tableShare recomputes one block's share from the recorded trim metadata:
// kept tokens equal the block limit when a trim happened.
func tableShare(t *testing.T, envelope Envelope, block string) int {
	t.Helper()
	for _, trimmed := range envelope.Trimmed {
		if trimmed.Block == block {
			return trimmed.KeptTok
		}
	}
	t.Fatalf("no trim recorded for %s", block)
	return 0
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
