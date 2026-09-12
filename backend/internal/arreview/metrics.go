package arreview

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// The candidate manuscript cites with numeric markers [n]; the mapping file
// the sidecar emits translates those back to corpus ids (Rxx). The lumin
// baseline draft cites evidence ids inline as [@ev_*].
var (
	numberedCitationPattern = regexp.MustCompile(`\[(\d{1,3})\]`)
	evCitationPattern       = regexp.MustCompile(`\[@ev_[A-Za-z0-9_-]+\]`)
	headingPattern          = regexp.MustCompile(`(?m)^#{1,6}\s+\S`)
)

// CandidateMetrics are the mechanical measurements of one sidecar manuscript.
type CandidateMetrics struct {
	CJKCharacters       int      `json:"cjk_characters"`
	Headings            int      `json:"headings"`
	CitationOccurrences int      `json:"citation_occurrences"`
	UniqueCitations     int      `json:"unique_citations"`
	DistinctSources     int      `json:"distinct_sources"`
	UnresolvedCitations []string `json:"unresolved_citations"`
	SourceCoverage      float64  `json:"source_coverage"`
	CitationMap         string   `json:"citation_map_sha256"`
	Manuscript          string   `json:"manuscript_sha256"`
}

// BaselineMetrics are the same-unit measurements of the lumin draft.
type BaselineMetrics struct {
	CJKCharacters     int `json:"cjk_characters"`
	EvidenceCitations int `json:"evidence_citations"`
	UniqueCitations   int `json:"unique_evidence_citations"`
}

// ComparisonMetrics is the review-metrics/1 artifact content: purely
// mechanical, hash-referenced numbers for the offline blind-eval workflow.
// It deliberately contains no quality verdict — the 12-case human blind
// evaluation owns that (design.md §8), and grounding-style checks alone are
// explicitly not evidence of correctness.
type ComparisonMetrics struct {
	SchemaVersion string           `json:"schema_version"`
	Generator     string           `json:"generator"`
	Candidate     CandidateMetrics `json:"candidate"`
	Baseline      BaselineMetrics  `json:"baseline"`
	Warnings      []string         `json:"warnings"`
}

// BuildMetrics measures the candidate manuscript against its citation map and
// the corpus, and measures the lumin baseline draft in the same units.
//
// Every [n] in the candidate must resolve through the citation map to a
// corpus id within range; unresolved markers are listed, not dropped — the
// report stays honest about anything the mechanical check could not tie back
// to the frozen pack.
func BuildMetrics(manuscript []byte, citationMapJSON []byte, corpus *Corpus, luminDraft string) (*ComparisonMetrics, error) {
	var mapping map[string]int
	if err := json.Unmarshal(citationMapJSON, &mapping); err != nil {
		return nil, fmt.Errorf("%w: sidecar citation map is not a JSON object: %v", ErrProtocol, err)
	}
	byNumber := make(map[int]string, len(mapping))
	for sourceID, number := range mapping {
		if number <= 0 {
			continue
		}
		byNumber[number] = sourceID
	}

	// Body only: the reference list legitimately repeats every number.
	body := splitReferenceList(string(manuscript))

	distinct := make(map[string]bool)
	uniqueNumbers := make(map[int]bool)
	var unresolved []string
	occurrences := 0
	for _, match := range numberedCitationPattern.FindAllStringSubmatch(body, -1) {
		occurrences++
		var number int
		fmt.Sscanf(match[1], "%d", &number)
		uniqueNumbers[number] = true
		sourceID, ok := byNumber[number]
		if !ok || number > len(corpus.Sources) {
			unresolved = append(unresolved, match[0])
			continue
		}
		distinct[sourceID] = true
	}
	sort.Strings(unresolved)

	coverage := 0.0
	if len(corpus.Sources) > 0 {
		coverage = float64(len(distinct)) / float64(len(corpus.Sources))
	}

	luminBody := splitReferenceList(luminDraft)
	evMatches := evCitationPattern.FindAllString(luminBody, -1)
	uniqueEvidence := make(map[string]bool, len(evMatches))
	for _, marker := range evMatches {
		uniqueEvidence[marker] = true
	}

	return &ComparisonMetrics{
		SchemaVersion: "review-metrics/1",
		Generator:     GeneratorVersion,
		Candidate: CandidateMetrics{
			CJKCharacters:       CountCJK(body),
			Headings:            len(headingPattern.FindAllString(body, -1)),
			CitationOccurrences: occurrences,
			UniqueCitations:     len(uniqueNumbers),
			DistinctSources:     len(distinct),
			UnresolvedCitations: unresolved,
			SourceCoverage:      coverage,
			CitationMap:         HashContent(citationMapJSON),
			Manuscript:          HashContent(manuscript),
		},
		Baseline: BaselineMetrics{
			CJKCharacters:     CountCJK(luminBody),
			EvidenceCitations: len(evMatches),
			UniqueCitations:   len(uniqueEvidence),
		},
		Warnings: []string{},
	}, nil
}

func splitReferenceList(manuscript string) string {
	for _, marker := range []string{"\n# 参考文献", "\n# References"} {
		if index := strings.Index(manuscript, marker); index >= 0 {
			return manuscript[:index]
		}
	}
	return manuscript
}
