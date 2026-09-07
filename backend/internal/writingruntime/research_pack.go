package writingruntime

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Research evidence pack assembly (design.md §4, contracts.md §2). The pack
// is the frozen artifact the evidence gate confirms: papers with their exact
// reading coverage, evidence with verified code point offsets, claims with
// their kind semantics, and an honest coverage projection. Every evidence
// entry is re-verified here against the stored parsed blocks — the worker's
// self-validation is defense in depth, never the last word.

// PaperReadResult is one selected paper's terminal read outcome, ready for
// pack assembly. The zero reading scope (unread) carries no evidence.
type PaperReadResult struct {
	PaperID           string
	Bibliography      writingkernel.PaperBibliography
	Origin            writingkernel.PaperOrigin
	MaterialRef       *writingkernel.ArtifactRef
	SelectionReason   string
	RelevanceStatus   writingkernel.RelevanceStatus
	ReadingScope      writingkernel.ReadingScope
	DocumentRef       *writingkernel.ArtifactRef
	ParsedDocumentRef *writingkernel.ArtifactRef
	// Blocks are the full parsed block list (text + hash); the pack verifier
	// checks every evidence entry against the block it names.
	Blocks       []ParsedBlock
	BlocksByID   map[string]ParsedBlock
	ReadBlockIDs []string
	TotalBlocks  int
	Truncated    bool
	License      *string
	// Claims/Evidence are the kernel-mapped, validated outputs of the read
	// sub-task. Empty for unread or degraded papers.
	Claims   []writingkernel.Claim
	Evidence []writingkernel.Evidence
	// UnreadReason explains a paper that produced no citable evidence
	// (acquisition failure, likely scanned, no OA URL, full text required).
	UnreadReason string
	// InputTokens/OutputTokens accumulate the worker usage across this
	// paper's phases for the node's ExecutionResult.
	InputTokens  int64
	OutputTokens int64
}

// EvidenceQuota is the per-paper reading requirement the executor applies.
type EvidenceQuota struct {
	MinCitableSources   int
	EvidenceRequirement string // writingkernel.EvidenceRequirementAbstractAllowed / FullTextRequired
}

// ErrInsufficientEvidence is the INSUFFICIENT_EVIDENCE sentinel
// (contracts.md §3 → 422): the selected workset cannot support the review,
// so the run pauses with candidates and coverage preserved.
var ErrInsufficientEvidence = errors.New("writingruntime: insufficient citable sources for the review")

// ErrEvidenceInvalid marks an evidence entry that failed block-level
// verification: quote offsets, block hash, or block ownership. It blocks
// pack assembly fail-closed (contracts.md §3 EVIDENCE_INVALID).
var ErrEvidenceInvalid = errors.New("writingruntime: evidence failed verification")

// Citable counts whether one paper may back the review under the contract's
// evidence requirement: full_text_required only counts full-text readings.
func (result PaperReadResult) Citable(requirement string) bool {
	if len(result.Evidence) == 0 {
		return false
	}
	if requirement == writingkernel.EvidenceRequirementFullTextRequired {
		return result.ReadingScope == writingkernel.ReadingScopeFullText
	}
	return result.ReadingScope != writingkernel.ReadingScopeUnread
}

// BuildEvidencePack assembles and validates the research-evidence-pack/1
// content. Verification is strict: any evidence whose stored block hash or
// quote offsets do not match aborts assembly with ErrEvidenceInvalid.
func BuildEvidencePack(contractHash string, candidatesRef writingkernel.ArtifactRef, results []PaperReadResult, quota EvidenceQuota, provenance []string) (writingkernel.ResearchEvidencePack, error) {
	if len(results) == 0 {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: no papers were read", ErrInsufficientEvidence)
	}
	papers := make([]writingkernel.PaperEvidence, 0, len(results))
	var evidence []writingkernel.Evidence
	var claims []writingkernel.Claim
	gaps := []string{}
	for _, result := range results {
		paper := writingkernel.PaperEvidence{
			PaperID: result.PaperID, Bibliography: result.Bibliography,
			Origin: result.Origin, MaterialRef: result.MaterialRef,
			SelectionReason: result.SelectionReason, RelevanceStatus: result.RelevanceStatus,
			ReadingScope: result.ReadingScope, DocumentRef: result.DocumentRef,
			ParsedDocumentRef: result.ParsedDocumentRef,
			ReadBlockIDs:      orEmptySlice(result.ReadBlockIDs),
			TotalBlocks:       result.TotalBlocks, Truncated: result.Truncated,
			License: result.License,
		}
		if paper.ReadBlockIDs == nil {
			paper.ReadBlockIDs = []string{}
		}
		papers = append(papers, paper)
		if result.UnreadReason != "" {
			gaps = append(gaps, fmt.Sprintf("paper %s unread: %s", result.PaperID, result.UnreadReason))
		}
		// Cross-paper id namespaces: the worker scopes ids per paper; the
		// pack needs global uniqueness, so ids are minted deterministically
		// from (paper, worker id).
		evidenceIDMap := make(map[string]string, len(result.Evidence))
		for entryIndex, entry := range result.Evidence {
			kernelID := "ev_" + writingstore.StableID("research", result.PaperID, entry.EvidenceID)
			if _, duplicate := evidenceIDMap[entry.EvidenceID]; duplicate {
				return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: paper %s evidence %q duplicated", ErrEvidenceInvalid, result.PaperID, entry.EvidenceID)
			}
			evidenceIDMap[entry.EvidenceID] = kernelID
			verified := entry
			verified.EvidenceID = kernelID
			verified.DocumentRef = writingkernel.ArtifactRef{ArtifactID: "art_research_document", Version: 1, ContentHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000"}
			if result.DocumentRef != nil {
				verified.DocumentRef = *result.DocumentRef
			}
			if err := verifyEvidenceAgainstBlocks(result, entryIndex, verified); err != nil {
				return writingkernel.ResearchEvidencePack{}, err
			}
			evidence = append(evidence, verified)
		}
		for _, claim := range result.Claims {
			bound := make([]string, 0, len(claim.EvidenceIDs))
			for _, evidenceID := range claim.EvidenceIDs {
				kernelID, ok := evidenceIDMap[evidenceID]
				if !ok {
					return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: claim %q references unverified evidence %q", ErrEvidenceInvalid, claim.ClaimID, evidenceID)
				}
				bound = append(bound, kernelID)
			}
			claims = append(claims, writingkernel.Claim{ClaimID: "clm_" + writingstore.StableID("research", result.PaperID, claim.ClaimID),
				PaperID: result.PaperID, Text: claim.Text, Kind: claim.Kind,
				EvidenceIDs: bound, ReviewStatus: writingkernel.ClaimReviewPending,
				Limitations: orEmptySlice(claim.Limitations)})
		}
	}
	if evidence == nil {
		evidence = []writingkernel.Evidence{}
	}
	if claims == nil {
		claims = []writingkernel.Claim{}
	}
	citable := 0
	for _, result := range results {
		if result.Citable(quota.EvidenceRequirement) {
			citable++
		}
	}
	if citable < quota.MinCitableSources {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: %d citable sources, contract requires %d (%s)",
			ErrInsufficientEvidence, citable, quota.MinCitableSources, quota.EvidenceRequirement)
	}
	pack := writingkernel.ResearchEvidencePack{
		SchemaVersion: writingkernel.ResearchEvidencePackSchemaVersion,
		ContractHash:  contractHash,
		CandidatesRef: candidatesRef,
		Papers:        papers,
		Evidence:      evidence,
		Claims:        claims,
		Coverage: writingkernel.EvidenceCoverage{
			Topics:         coverageTopics(claims),
			Gaps:           gaps,
			Contradictions: []string{}, // v1: contradiction mining is not implemented; the honest projection is empty
		},
		Provenance: provenance,
	}
	if err := pack.Validate(); err != nil {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: pack failed kernel validation: %v", ErrEvidenceInvalid, err)
	}
	return pack, nil
}

// verifyEvidenceAgainstBlocks re-checks one evidence entry against the stored
// parsed block content: block existence, block hash equality (sha256 over the
// block's UTF-8 text), scope consistency with the paper's reading scope, and
// the contracts.md §2 offset invariant
// quote == block_text[start_char:end_char] (Unicode code points).
func verifyEvidenceAgainstBlocks(result PaperReadResult, evidenceIndex int, evidence writingkernel.Evidence) error {
	block, ok := result.BlocksByID[evidence.BlockID]
	if !ok {
		return fmt.Errorf("%w: evidence %q names unknown block %q", ErrEvidenceInvalid, evidence.EvidenceID, evidence.BlockID)
	}
	if evidence.BlockHash != block.BlockHash {
		return fmt.Errorf("%w: evidence %q block hash does not match stored block %q", ErrEvidenceInvalid, evidence.EvidenceID, evidence.BlockID)
	}
	if !writingkernel.VerifyEvidenceQuote(evidence, block.Text) {
		return fmt.Errorf("%w: evidence %q quote does not match block_text[start:end]", ErrEvidenceInvalid, evidence.EvidenceID)
	}
	if evidence.EvidenceScope == writingkernel.EvidenceScopeFullText && result.ReadingScope != writingkernel.ReadingScopeFullText {
		return fmt.Errorf("%w: evidence %q claims full-text scope but paper %s was read as %q",
			ErrEvidenceInvalid, evidence.EvidenceID, result.PaperID, result.ReadingScope)
	}
	_ = evidenceIndex
	return nil
}

// coverageTopics is the v1 placeholder projection: the most frequent
// non-trivial tokens across claim texts. It is a frequency aggregate, not a
// semantic topic model — T06/T07 replace it with real topic synthesis.
func coverageTopics(claims []writingkernel.Claim) []string {
	stop := map[string]struct{}{"the": {}, "and": {}, "of": {}, "to": {}, "in": {}, "a": {}, "is": {}, "are": {}, "for": {}, "on": {}, "with": {}, "that": {}, "this": {}}
	counts := map[string]int{}
	for _, claim := range claims {
		for _, token := range strings.FieldsFunc(claim.Text, func(r rune) bool {
			isCJK := r >= 0x4E00 && r <= 0x9FFF
			return !isCJK && !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
		}) {
			lower := strings.ToLower(token)
			if len(lower) < 2 {
				continue
			}
			if _, skip := stop[lower]; skip {
				continue
			}
			counts[lower]++
		}
	}
	type topicCount struct {
		topic string
		count int
	}
	var ranked []topicCount
	for topic, count := range counts {
		if count < 2 {
			continue
		}
		ranked = append(ranked, topicCount{topic: topic, count: count})
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].count != ranked[j].count {
			return ranked[i].count > ranked[j].count
		}
		return ranked[i].topic < ranked[j].topic
	})
	if len(ranked) > 5 {
		ranked = ranked[:5]
	}
	topics := make([]string, 0, len(ranked))
	for _, entry := range ranked {
		topics = append(topics, fmt.Sprintf("%s (%d)", entry.topic, entry.count))
	}
	return topics
}
