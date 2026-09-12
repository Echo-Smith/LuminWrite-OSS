package arreview

import (
	"strings"
	"testing"
)

func citationMap() []byte {
	return []byte(`{"R01": 1, "R02": 2, "R03": 3}`)
}

func corpus() *Corpus {
	return &Corpus{Sources: []CorpusSource{
		{SourceID: "R01"}, {SourceID: "R02"}, {SourceID: "R03"},
	}}
}

func TestBuildMetricsMeasuresCandidateAndBaseline(t *testing.T) {
	manuscript := []byte("# 标题\n\n机制比较的结论[1]，另一结论[2][3]，重复引用[1]。\n\n> 证据边界：占位。\n\n# 参考文献\n\n1. Alpha.\n2. Beta.\n3. Gamma.\n")
	lumin := "基底稿引用了证据[@ev_a]与[@ev_b]，还有一次[@ev_a]。"

	metrics, err := BuildMetrics(manuscript, citationMap(), corpus(), lumin)
	if err != nil {
		t.Fatalf("BuildMetrics: %v", err)
	}
	candidate := metrics.Candidate
	if candidate.CitationOccurrences != 4 {
		t.Fatalf("occurrences = %d, want 4", candidate.CitationOccurrences)
	}
	if candidate.UniqueCitations != 3 {
		t.Fatalf("unique = %d, want 3", candidate.UniqueCitations)
	}
	if candidate.DistinctSources != 3 {
		t.Fatalf("distinct = %d, want 3", candidate.DistinctSources)
	}
	if len(candidate.UnresolvedCitations) != 0 {
		t.Fatalf("unexpected unresolved: %v", candidate.UnresolvedCitations)
	}
	if candidate.SourceCoverage != 1.0 {
		t.Fatalf("coverage = %v, want 1", candidate.SourceCoverage)
	}
	if candidate.CJKCharacters == 0 || candidate.Headings == 0 {
		t.Fatalf("candidate text metrics empty: %+v", candidate)
	}
	if metrics.Baseline.EvidenceCitations != 3 || metrics.Baseline.UniqueCitations != 2 {
		t.Fatalf("baseline metrics wrong: %+v", metrics.Baseline)
	}
	if !strings.HasPrefix(candidate.Manuscript, "sha256:") || !strings.HasPrefix(candidate.CitationMap, "sha256:") {
		t.Fatalf("metrics must pin content hashes: %+v", candidate)
	}
}

func TestBuildMetricsReportsUnresolvedCitations(t *testing.T) {
	manuscript := []byte("结论一[1]，结论二[9]。")
	metrics, err := BuildMetrics(manuscript, citationMap(), corpus(), "")
	if err != nil {
		t.Fatalf("BuildMetrics: %v", err)
	}
	if len(metrics.Candidate.UnresolvedCitations) != 1 || metrics.Candidate.UnresolvedCitations[0] != "[9]" {
		t.Fatalf("unresolved citations wrong: %v", metrics.Candidate.UnresolvedCitations)
	}
	if metrics.Candidate.DistinctSources != 1 {
		t.Fatalf("distinct = %d, want 1", metrics.Candidate.DistinctSources)
	}
}

func TestBuildMetricsRejectsMalformedCitationMap(t *testing.T) {
	if _, err := BuildMetrics([]byte("正文"), []byte(`not-json`), corpus(), ""); err == nil {
		t.Fatal("malformed citation map must be rejected")
	}
	if _, err := BuildMetrics([]byte("正文"), []byte(`["not","an","object"]`), corpus(), ""); err == nil {
		t.Fatal("array citation map must be rejected")
	}
}
