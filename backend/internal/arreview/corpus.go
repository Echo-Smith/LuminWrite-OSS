package arreview

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// Schema versions of the two input files the Go host writes into the sidecar
// project directory before dispatching a run.
const (
	CorpusSchemaVersion = "lumin-review-corpus/1"
	SpecSchemaVersion   = "lumin-review-spec/1"
	// GeneratorBoundaryWriter is stamped into the candidate's evidence
	// boundary so provenance never claims the manuscript was produced by the
	// upstream managed profile.
	GeneratorBoundaryWriter = "LuminBuddy AR-012 候选生成器"
)

// MinCorpusSources is the smallest frozen corpus the sidecar pipeline can
// meaningfully compare against: below this even scaled grounding gates
// degenerate. The research path's own INSUFFICIENT_EVIDENCE gate normally
// catches this earlier; the check here is defense in depth.
const MinCorpusSources = 5

// ErrInsufficientCorpus reports a frozen evidence pack that cannot back a
// candidate comparison at all.
var ErrInsufficientCorpus = errors.New("arreview: frozen evidence pack cannot back a review corpus")

// CorpusSource mirrors the sidecar's ReviewSource schema (the fork accepts
// public_abstract and full_text scopes via the spec whitelist).
type CorpusSource struct {
	SourceID             string   `json:"source_id"`
	Title                string   `json:"title"`
	Authors              []string `json:"authors"`
	Year                 int      `json:"year"`
	Journal              string   `json:"journal"`
	DOI                  string   `json:"doi"`
	CanonicalURL         string   `json:"canonical_url"`
	EvidenceURL          string   `json:"evidence_url"`
	Abstract             string   `json:"abstract"`
	EvidenceScope        string   `json:"evidence_scope"`
	Themes               []string `json:"themes"`
	CrossrefUpdateStatus string   `json:"crossref_update_status"`
}

// Corpus is the REVIEW_CORPUS.json document. The bytes must be deterministic
// for a given frozen pack (no timestamps): the worker rebuilds them at
// execution time and the exchange volume refuses a content-derived directory
// whose files changed.
type Corpus struct {
	SchemaVersion  string         `json:"schema_version"`
	Topic          string         `json:"topic"`
	EvidencePolicy string         `json:"evidence_policy"`
	SourceCount    int            `json:"source_count"`
	FailedSources  int            `json:"failed_sources"`
	Sources        []CorpusSource `json:"sources"`
}

// BuildCorpus converts the frozen evidence pack into the sidecar corpus.
//
// Conversion rules (honest by construction):
//   - Only papers the research path actually read (abstract or full_text
//     scope) are eligible; unread papers can never be cited.
//   - The abstract text is the paper's longest verified evidence quote —
//     abstract-scope quotes preferred, full-text quotes accepted with the
//     scope marker set accordingly. Quotes below the sidecar's 120-char
//     floor are skipped.
//   - Papers without a usable quote, a year, a non-empty title, or a DOI are
//     excluded with a returned reason: upstream deduplicates on normalized
//     DOI, so DOI-less entries would falsely collide on the empty string.
//   - Duplicate normalized DOIs keep the first occurrence (deterministic by
//     PaperID order) and exclude the rest.
func BuildCorpus(pack writingkernel.ResearchEvidencePack, centralQuestion string) (*Corpus, []string, error) {
	papers := make([]writingkernel.PaperEvidence, len(pack.Papers))
	copy(papers, pack.Papers)
	sort.Slice(papers, func(i, j int) bool { return papers[i].PaperID < papers[j].PaperID })

	quotesByPaper := make(map[string][]writingkernel.Evidence)
	for _, entry := range pack.Evidence {
		quotesByPaper[entry.PaperID] = append(quotesByPaper[entry.PaperID], entry)
	}
	for _, quotes := range quotesByPaper {
		sort.Slice(quotes, func(i, j int) bool {
			if len(quotes[i].Quote) != len(quotes[j].Quote) {
				return len(quotes[i].Quote) > len(quotes[j].Quote)
			}
			return quotes[i].EvidenceID < quotes[j].EvidenceID
		})
	}

	var (
		sources  []CorpusSource
		warnings []string
	)
	seenDOI := make(map[string]string)
	for index, paper := range papers {
		exclude := func(reason string) {
			warnings = append(warnings, fmt.Sprintf("paper %s excluded: %s", paper.PaperID, reason))
		}
		switch paper.ReadingScope {
		case writingkernel.ReadingScopeAbstract, writingkernel.ReadingScopeFullText:
		default:
			exclude(fmt.Sprintf("reading scope %q is not readable", paper.ReadingScope))
			continue
		}
		if paper.Bibliography.Title == "" {
			exclude("empty title")
			continue
		}
		if paper.Bibliography.Year == nil {
			exclude("missing year")
			continue
		}
		doi := NormalizeDOI(deref(paper.Bibliography.DOI))
		if doi == "" {
			exclude("missing DOI (upstream deduplicates on DOI, empty would collide)")
			continue
		}
		if first, taken := seenDOI[doi]; taken {
			exclude(fmt.Sprintf("duplicate DOI with %s", first))
			continue
		}
		abstract, scope := longestQuote(quotesByPaper[paper.PaperID])
		if abstract == "" {
			exclude("no verified evidence quote of at least 120 characters")
			continue
		}
		seenDOI[doi] = paper.PaperID
		sources = append(sources, CorpusSource{
			SourceID:             fmt.Sprintf("R%02d", index+1),
			Title:                paper.Bibliography.Title,
			Authors:              orEmpty(paper.Bibliography.Authors),
			Year:                 *paper.Bibliography.Year,
			Journal:              deref(paper.Bibliography.Venue),
			DOI:                  doi,
			CanonicalURL:         canonicalURL(doi, paper.Bibliography.CanonicalURL),
			EvidenceURL:          canonicalURL(doi, paper.Bibliography.CanonicalURL),
			Abstract:             abstract,
			EvidenceScope:        scope,
			Themes:               []string{},
			CrossrefUpdateStatus: "not_checked",
		})
	}

	if len(sources) < MinCorpusSources {
		return nil, warnings, fmt.Errorf("%w: %d usable sources after exclusions (minimum %d)",
			ErrInsufficientCorpus, len(sources), MinCorpusSources)
	}
	return &Corpus{
		SchemaVersion:  CorpusSchemaVersion,
		Topic:          centralQuestion,
		EvidencePolicy: "frozen_evidence_pack_from_research_review_run",
		SourceCount:    len(sources),
		FailedSources:  len(warnings),
		Sources:        sources,
	}, warnings, nil
}

// longestQuote picks the paper's longest abstract-scope quote, falling back
// to full-text quotes (scope marker full_text). Quotes below the sidecar's
// 120-character floor never qualify.
func longestQuote(quotes []writingkernel.Evidence) (string, string) {
	bestAbstract := ""
	bestFullText := ""
	for _, entry := range quotes {
		quote := strings.TrimSpace(entry.Quote)
		if len([]rune(quote)) < 120 {
			continue
		}
		switch entry.EvidenceScope {
		case writingkernel.EvidenceScopeAbstract:
			if bestAbstract == "" {
				bestAbstract = quote
			}
		case writingkernel.EvidenceScopeFullText:
			if bestFullText == "" {
				bestFullText = quote
			}
		}
	}
	if bestAbstract != "" {
		return bestAbstract, "public_abstract"
	}
	if bestFullText != "" {
		return bestFullText, "full_text"
	}
	return "", ""
}

// NormalizeDOI mirrors the fork's normalization: trim, lowercase, strip
// resolver prefixes and trailing punctuation.
func NormalizeDOI(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	for _, prefix := range []string{"https://doi.org/", "http://doi.org/", "doi:"} {
		value = strings.TrimPrefix(value, prefix)
	}
	return strings.TrimRight(value, "/.,; ")
}

// BuildSpecMapping produces the REVIEW_SPEC.json mapping for one frozen task.
// Threshold scaling mirrors the fork's write_scaled_spec: a small frozen pack
// lowers the mechanical gates proportionally instead of guaranteeing failure,
// while coverage vocabulary is anchored to the pack's own topics plus three
// genre-generic entries. The approved outline's section titles become the
// required headings — the outline the human gate approved *is* the genre.
//
// abstractRunes is the total evidence-corpus size in code points (the sum of
// every source's verified abstract). The mechanical floor scales with it: a
// thin corpus cannot substantiate the preset 4500-character floor, and a
// model forced to pad manufactures claims — the 12-case blind evaluation
// measured 15 unsupported claims on the thinnest cell vs 3 on the richest
// (2026-09-13 计分对照报告). Scaling is transport-side calibration of the
// genre floor, not a relaxation of per-claim grounding.
func BuildSpecMapping(centralQuestion string, headings, packTopics []string, corpusSize, abstractRunes int) (map[string]any, error) {
	trimmedHeadings := make([]string, 0, len(headings))
	seen := make(map[string]bool)
	for _, heading := range headings {
		heading = strings.TrimSpace(heading)
		if heading == "" || len([]rune(heading)) > 200 {
			return nil, fmt.Errorf("%w: outline heading %q is empty or over 200 chars", ErrProtocol, heading)
		}
		if seen[heading] {
			return nil, fmt.Errorf("%w: duplicate outline heading %q", ErrProtocol, heading)
		}
		seen[heading] = true
		trimmedHeadings = append(trimmedHeadings, heading)
	}
	if len(trimmedHeadings) < 3 || len(trimmedHeadings) > 24 {
		return nil, fmt.Errorf("%w: outline must yield 3..24 headings, got %d", ErrProtocol, len(trimmedHeadings))
	}
	topic := truncate(strings.TrimSpace(centralQuestion), 200)
	if topic == "" {
		return nil, fmt.Errorf("%w: central question is required", ErrProtocol)
	}

	size := corpusSize
	if size < 1 {
		size = 1
	}
	minDistinct := min(15, size)
	minCited := min(12, size)
	minOccurrences := min(24, 2*minDistinct)
	minInText := minInTextCitations(minCited)

	coverage := make(map[string]any, len(packTopics)+3)
	for index, topicName := range packTopics {
		name := coverageTerm(topicName)
		if name == "" {
			continue
		}
		coverage[fmt.Sprintf("topic_%d", index)] = []string{truncate(name, 200)}
	}
	coverage["genre_limitations"] = []string{"局限", "证据边界"}
	coverage["genre_future_work"] = []string{"研究方向", "研究空白", "未来"}
	coverage["genre_synthesis"] = []string{"综合判断", "综合来看", "文献共识"}

	// Length floor tracks the corpus the candidate can honestly cite (see
	// BuildSpecMapping doc): 4500 for rich corpora, floored at 1800 for thin
	// ones. Draft targets scale with the floor, preserving the preset's
	// 7000/11000 shape at the 4500 cap.
	minCJK := min(4500, max(1800, abstractRunes/2))
	draftMin := minCJK + 2500
	draftMax := draftMin + 4000

	return map[string]any{
		"schema_version":           SpecSchemaVersion,
		"topic":                    topic,
		"title":                    truncate(topic, 300),
		"required_headings":        trimmedHeadings,
		"coverage_terms":           coverage,
		"topic_mention_terms":      []string{},
		"editor_discipline":        "综述主题领域",
		"boundary_writer":          GeneratorBoundaryWriter,
		"boundary_marker":          "公开题录与公开摘要",
		"terminology_hint":         "",
		"min_cjk_characters":       minCJK,
		"min_distinct_sources":     minDistinct,
		"min_citation_occurrences": minOccurrences,
		"min_cited_sources":        minCited,
		"min_in_text_citations":    minInText,
		"draft_min_chars":          draftMin,
		"draft_max_chars":          draftMax,
		"allowed_evidence_scopes":  []string{"public_abstract", "full_text"},
		"repair_excerpt_headings":  repairExcerptHeadings(trimmedHeadings),
	}, nil
}

// coverageTerm normalizes one pack topic into a manuscript-matchable term.
// The research chain's coverage.topics are frequency projections of the shape
// "token (count)" (research_pack.coverageTopics); the fork's coverage gate
// matches terms as substrings of the manuscript, and no prose ever contains
// the provenance count. Stripping it is transport normalization, not gate
// weakening: what the candidate must discuss is the token itself.
var topicCountSuffix = regexp.MustCompile(`\s*\(\d+\)$`)

func coverageTerm(topic string) string {
	return strings.TrimSpace(topicCountSuffix.ReplaceAllString(topic, ""))
}

// CorpusAbstractRunes sums the code points of every source's abstract — the
// information budget the candidate may honestly cite.
func CorpusAbstractRunes(corpus *Corpus) int {
	total := 0
	for _, src := range corpus.Sources {
		total += len([]rune(src.Abstract))
	}
	return total
}

// CheckCorpusBudget enforces the deployment's candidate-generation floors on
// top of BuildCorpus's structural minimum: a source-count floor and a total
// abstract information budget. minRunes <= 0 disables the budget check;
// minSources below the structural floor has no effect. Both rejections wrap
// ErrInsufficientCorpus so the API layer maps them to 422
// AR_REVIEW_INSUFFICIENT_CORPUS. Thresholds are deployment-tunable
// (AR_REVIEW_MIN_SOURCES / AR_REVIEW_MIN_ABSTRACT_RUNES); the defaults
// derive from the 24-case blind evaluation (2026-09-13 计分对照报告：预算
// ~3600 字符的薄语料是无支持论断峰值区).
func CheckCorpusBudget(corpus *Corpus, minSources, minRunes int) error {
	if minSources < MinCorpusSources {
		minSources = MinCorpusSources
	}
	if len(corpus.Sources) < minSources {
		return fmt.Errorf("%w: %d usable sources below the %d-source floor",
			ErrInsufficientCorpus, len(corpus.Sources), minSources)
	}
	if minRunes > 0 {
		if total := CorpusAbstractRunes(corpus); total < minRunes {
			return fmt.Errorf("%w: corpus information budget %d runes below the %d-rune floor (thin corpora force unsupported padding)",
				ErrInsufficientCorpus, total, minRunes)
		}
	}
	return nil
}

// repairExcerptHeadings mirrors the fork's heuristic: gap/controversy/outlook
// sections are where a bounded coverage repair edit is legitimate; without
// any such heading the last two sections bound the edit instead.
func repairExcerptHeadings(headings []string) []string {
	tokens := []string{"空白", "争议", "未来", "展望", "局限", "结语"}
	var picks []string
	for _, heading := range headings {
		for _, token := range tokens {
			if strings.Contains(heading, token) {
				picks = append(picks, heading)
				break
			}
		}
		if len(picks) == 3 {
			return picks
		}
	}
	if len(picks) == 0 {
		if len(headings) >= 2 {
			picks = headings[len(headings)-2:]
		} else {
			picks = headings
		}
	}
	return picks
}

// minInTextCitations scales the in-text floor at the upstream 18/12 ratio
// (1.5×) without ever dropping below the distinct-cited floor.
func minInTextCitations(minCited int) int {
	if minCited >= 12 {
		return 18
	}
	return max(minCited, (3*minCited+1)/2)
}

func canonicalURL(doi string, explicit *string) string {
	if explicit != nil && strings.TrimSpace(*explicit) != "" {
		return strings.TrimSpace(*explicit)
	}
	return "https://doi.org/" + doi
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func orEmpty(value []string) []string {
	if value == nil {
		return []string{}
	}
	return value
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

// CountCJK counts Han characters — the same unit both upstream gates use.
func CountCJK(text string) int {
	count := 0
	for _, r := range text {
		if unicode.Is(unicode.Han, r) {
			count++
		}
	}
	return count
}
