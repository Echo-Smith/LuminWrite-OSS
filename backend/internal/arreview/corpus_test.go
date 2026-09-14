package arreview

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

func hash(char byte) string { return "sha256:" + repeat(char, 64) }

// makePack assembles a frozen evidence pack shaped like the research path's
// t05 output: citable papers with verified quotes plus deliberate defects.
func makePack() writingkernel.ResearchEvidencePack {
	paper := func(id, title, doi string, scope writingkernel.ReadingScope, year int) writingkernel.PaperEvidence {
		yearValue := year
		venue := "Journal of Evidence"
		return writingkernel.PaperEvidence{
			PaperID: id,
			Bibliography: writingkernel.PaperBibliography{
				Title:   title,
				Authors: []string{"A. Author"},
				Year:    &yearValue,
				Venue:   &venue,
				DOI:     &doi,
			},
			ReadingScope: scope,
		}
	}
	longAbstract := strings.Repeat("证据句子描述了机制与比较结论。", 12) // >120 chars
	papers := []writingkernel.PaperEvidence{
		paper("p-01", "Alpha 证据", "10.1000/alpha", writingkernel.ReadingScopeAbstract, 2023),
		paper("p-02", "Beta 证据", "10.1000/beta", writingkernel.ReadingScopeAbstract, 2023),
		paper("p-03", "Gamma 证据", "10.1000/gamma", writingkernel.ReadingScopeAbstract, 2024),
		paper("p-04", "Delta 证据", "10.1000/delta", writingkernel.ReadingScopeFullText, 2024),
		paper("p-05", "Epsilon 证据", "10.1000/epsilon", writingkernel.ReadingScopeAbstract, 2025),
		paper("p-06", "Zeta 证据", "10.1000/zeta", writingkernel.ReadingScopeFullText, 2025),
		// Defects: unread, no DOI, duplicate DOI of p-01, short quote only.
		paper("p-07", "Unread 证据", "10.1000/unread", writingkernel.ReadingScopeUnread, 2022),
		paper("p-08", "No DOI 证据", "", writingkernel.ReadingScopeAbstract, 2022),
		paper("p-09", "Dup DOI 证据", "10.1000/alpha", writingkernel.ReadingScopeAbstract, 2021),
		paper("p-10", "Short 证据", "10.1000/short", writingkernel.ReadingScopeAbstract, 2021),
	}
	var evidence []writingkernel.Evidence
	addQuote := func(paperID, quote string, scope writingkernel.EvidenceScope) {
		evidence = append(evidence, writingkernel.Evidence{
			EvidenceID:    paperID + "-q" + string(rune('a'+len(evidence)%26)),
			PaperID:       paperID,
			Quote:         quote,
			EvidenceScope: scope,
		})
	}
	for _, p := range papers[:6] {
		addQuote(p.PaperID, longAbstract, writingkernel.EvidenceScopeAbstract)
	}
	addQuote("p-10", "太短的引用。", writingkernel.EvidenceScopeAbstract)
	return writingkernel.ResearchEvidencePack{
		SchemaVersion: writingkernel.ResearchEvidencePackSchemaVersion,
		ContractHash:  hash('1'),
		Papers:        papers,
		Evidence:      evidence,
		Coverage: writingkernel.EvidenceCoverage{
			Topics: []string{"机制比较", "边界条件"},
		},
	}
}

func TestBuildCorpusConvertsEligiblePapers(t *testing.T) {
	pack := makePack()
	corpus, warnings, err := BuildCorpus(pack, "研究问题：机制比较")
	if err != nil {
		t.Fatalf("BuildCorpus: %v", err)
	}
	if len(corpus.Sources) != 6 {
		t.Fatalf("expected 6 usable sources, got %d (warnings: %v)", len(corpus.Sources), warnings)
	}
	if corpus.SourceCount != 6 || corpus.FailedSources != len(warnings) || len(warnings) != 4 {
		t.Fatalf("corpus accounting wrong: count=%d failed=%d warnings=%v",
			corpus.SourceCount, corpus.FailedSources, warnings)
	}
	// Deterministic ordering and id assignment.
	if corpus.Sources[0].SourceID != "R01" || corpus.Sources[0].Title != "Alpha 证据" {
		t.Fatalf("first source wrong: %+v", corpus.Sources[0])
	}
	// p-04 was read full-text, but its only verified quote is abstract-scope,
	// so the corpus scope marker stays public_abstract (the fallback path is
	// covered by its own test below).
	if corpus.Sources[3].EvidenceScope != "public_abstract" {
		t.Fatalf("full-text paper scope wrong: %+v", corpus.Sources[3])
	}
	// DOI normalization strips the resolver prefix.
	if corpus.Sources[0].DOI != "10.1000/alpha" {
		t.Fatalf("doi normalization wrong: %q", corpus.Sources[0].DOI)
	}
	if corpus.Topic != "研究问题：机制比较" {
		t.Fatalf("topic propagation wrong: %q", corpus.Topic)
	}
}

func TestBuildCorpusPrefersAbstractQuoteAndFallsBackToFullText(t *testing.T) {
	pack := makePack()
	pack.Evidence = append(pack.Evidence,
		writingkernel.Evidence{PaperID: "p-02", Quote: strings.Repeat("全文优先。", 40), EvidenceScope: writingkernel.EvidenceScopeFullText},
	)
	// p-02 now has a longer full-text quote; abstract quote must still win.
	corpus, _, err := BuildCorpus(pack, "问题")
	if err != nil {
		t.Fatalf("BuildCorpus: %v", err)
	}
	if corpus.Sources[1].EvidenceScope != "public_abstract" {
		t.Fatalf("abstract quote must be preferred: %+v", corpus.Sources[1])
	}

	// p-04 gains a long full-text quote, then its abstract quote is removed:
	// the full-text quote must take over with the full_text scope marker.
	pack.Evidence = append(pack.Evidence,
		writingkernel.Evidence{PaperID: "p-04", Quote: strings.Repeat("全文段落提供了更细的参数与数值边界。", 12), EvidenceScope: writingkernel.EvidenceScopeFullText},
	)
	filtered := pack
	filtered.Evidence = nil
	for _, entry := range pack.Evidence {
		if entry.PaperID == "p-04" && entry.EvidenceScope == writingkernel.EvidenceScopeAbstract {
			continue
		}
		filtered.Evidence = append(filtered.Evidence, entry)
	}
	corpus, _, err = BuildCorpus(filtered, "问题")
	if err != nil {
		t.Fatalf("BuildCorpus fallback: %v", err)
	}
	found := false
	for _, source := range corpus.Sources {
		if source.Title == "Delta 证据" {
			found = true
			if source.EvidenceScope != "full_text" {
				t.Fatalf("full-text fallback scope wrong: %+v", source)
			}
		}
	}
	if !found {
		t.Fatal("delta paper missing from fallback corpus")
	}
}

func TestBuildCorpusInsufficientPackFails(t *testing.T) {
	pack := makePack()
	pack.Papers = pack.Papers[:3]
	pack.Evidence = pack.Evidence[:3]
	_, _, err := BuildCorpus(pack, "问题")
	if !errors.Is(err, ErrInsufficientCorpus) {
		t.Fatalf("expected insufficient corpus, got %v", err)
	}
}

func TestBuildSpecMappingScalesAndDerives(t *testing.T) {
	headings := []string{"引言", "机制比较", "边界条件", "文献争议与研究空白", "结语"}
	mapping, err := BuildSpecMapping("课后服务政策效果", headings, []string{"机制比较", "边界条件"}, 6, 9000)
	if err != nil {
		t.Fatalf("BuildSpecMapping: %v", err)
	}
	if mapping["schema_version"] != SpecSchemaVersion {
		t.Fatalf("schema version wrong: %v", mapping["schema_version"])
	}
	if mapping["min_distinct_sources"] != 6 || mapping["min_cited_sources"] != 6 ||
		mapping["min_citation_occurrences"] != 12 || mapping["min_in_text_citations"] != 9 {
		t.Fatalf("threshold scaling wrong: %v", mapping)
	}
	coverage := mapping["coverage_terms"].(map[string]any)
	if _, ok := coverage["topic_0"]; !ok {
		t.Fatalf("pack topics missing from coverage: %v", coverage)
	}
	if got := fmt.Sprint(coverage["topic_0"]); got != "[机制比较]" {
		t.Fatalf("coverage topic_0 wrong: %v", got)
	}
	// Research-chain topics carry a provenance frequency count ("x (6)"); the
	// gate matches terms as manuscript substrings, so the count is stripped
	// and a count-only topic contributes nothing.
	suffixed, err := BuildSpecMapping("课后服务政策效果", headings, []string{"自动选择 (6)", " (3)"}, 6, 9000)
	if err != nil {
		t.Fatalf("BuildSpecMapping suffixed: %v", err)
	}
	suffixCoverage := suffixed["coverage_terms"].(map[string]any)
	if got := fmt.Sprint(suffixCoverage["topic_0"]); got != "[自动选择]" {
		t.Fatalf("provenance count not stripped: %v", got)
	}
	if _, ok := suffixCoverage["topic_1"]; ok {
		t.Fatalf("count-only topic must be dropped: %v", suffixCoverage["topic_1"])
	}
	if _, ok := coverage["genre_limitations"]; !ok {
		t.Fatalf("genre coverage entries missing: %v", coverage)
	}
	excerpts := mapping["repair_excerpt_headings"].([]string)
	if len(excerpts) == 0 || excerpts[0] != "文献争议与研究空白" {
		t.Fatalf("repair excerpts wrong: %v", excerpts)
	}
	if mapping["boundary_writer"] != GeneratorBoundaryWriter {
		t.Fatalf("boundary writer wrong: %v", mapping["boundary_writer"])
	}

	// Large corpora keep upstream's preset thresholds.
	full, err := BuildSpecMapping("课后服务政策效果", headings, nil, 30, 9000)
	if err != nil {
		t.Fatalf("BuildSpecMapping large: %v", err)
	}
	if full["min_distinct_sources"] != 15 || full["min_citation_occurrences"] != 24 ||
		full["min_in_text_citations"] != 18 {
		t.Fatalf("preset thresholds wrong: %v", full)
	}
	if full["min_cjk_characters"] != 4500 || full["draft_min_chars"] != 7000 || full["draft_max_chars"] != 11000 {
		t.Fatalf("preset length thresholds wrong: %v", full)
	}

	// Thin corpora scale the length floor down instead of forcing the model
	// to pad (padding is where the blind evaluation counted fabrication):
	// floor = clamp(abstractRunes/2, 1800, 4500), draft targets follow.
	thin, err := BuildSpecMapping("课后服务政策效果", headings, nil, 6, 3600)
	if err != nil {
		t.Fatalf("BuildSpecMapping thin: %v", err)
	}
	if thin["min_cjk_characters"] != 1800 || thin["draft_min_chars"] != 4300 || thin["draft_max_chars"] != 8300 {
		t.Fatalf("thin-corpus length scaling wrong: %v", thin)
	}
}

func TestBuildSpecMappingValidatesHeadings(t *testing.T) {
	if _, err := BuildSpecMapping("问题", []string{"引言", "结语"}, nil, 6, 9000); err == nil {
		t.Fatal("two headings must be rejected")
	}
	if _, err := BuildSpecMapping("问题", []string{"甲", "甲", "乙"}, nil, 6, 9000); err == nil {
		t.Fatal("duplicate headings must be rejected")
	}
	if _, err := BuildSpecMapping("  ", []string{"甲", "乙", "丙"}, nil, 6, 9000); err == nil {
		t.Fatal("empty central question must be rejected")
	}
}

func TestCountCJK(t *testing.T) {
	if got := CountCJK("中文abc123English"); got != 2 {
		t.Fatalf("CountCJK = %d, want 2", got)
	}
}

func TestCheckCorpusBudget(t *testing.T) {
	corpus := &Corpus{Sources: make([]CorpusSource, 5)}
	for i := range corpus.Sources {
		corpus.Sources[i].Abstract = strings.Repeat("摘要", 300) // 600 runes each, 3000 total
	}
	// Defaults: structural 5-source floor holds, budget check disabled at 0.
	if err := CheckCorpusBudget(corpus, 0, 0); err != nil {
		t.Fatalf("default floors must pass a 5×600 corpus: %v", err)
	}
	// The tested unsafe zone: 5 sources × 600 runes fails a 6000-rune budget.
	err := CheckCorpusBudget(corpus, 5, 6000)
	if err == nil || !errors.Is(err, ErrInsufficientCorpus) {
		t.Fatalf("thin budget must be refused with ErrInsufficientCorpus, got %v", err)
	}
	// A 6-source floor rejects a 5-source corpus regardless of budget.
	if err := CheckCorpusBudget(corpus, 6, 0); err == nil || !errors.Is(err, ErrInsufficientCorpus) {
		t.Fatalf("source floor must be refused with ErrInsufficientCorpus, got %v", err)
	}
	// A richer corpus passes the same 6000-rune budget.
	rich := &Corpus{Sources: make([]CorpusSource, 6)}
	for i := range rich.Sources {
		rich.Sources[i].Abstract = strings.Repeat("摘要", 600) // 1200 runes each, 7200 total
	}
	if err := CheckCorpusBudget(rich, 5, 6000); err != nil {
		t.Fatalf("rich corpus must pass: %v", err)
	}
}
