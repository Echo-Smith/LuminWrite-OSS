package writingkernel

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Research evidence contract types (specs/research-review/contracts.md §2).
// These structs are the Go mirror of the frozen research Artifacts:
// research_candidates, research_evidence_pack, research_outline /
// approved_research_outline, evidence_approval, and research_citation_index.
// Validation here is structural plus intra/inter-object reference checks;
// hash recomputation against stored content happens in the runtime layer.

const (
	ResearchCandidatesSchemaVersion    = "research-candidates/1"
	ResearchEvidencePackSchemaVersion  = "research-evidence-pack/1"
	ResearchOutlineSchemaVersion       = "research-outline/1"
	EvidenceApprovalSchemaVersion      = "evidence-approval/1"
	ResearchCitationIndexSchemaVersion = "research-citation-index/1"
)

// ArtifactRef points at one immutable Artifact version. All cross-object
// references carry version and content hash so a stale or tampered pointer
// can never silently resolve.
type ArtifactRef struct {
	ArtifactID  string `json:"artifact_id"`
	Version     int    `json:"version"`
	ContentHash string `json:"content_hash"`
}

func (r ArtifactRef) Validate() error {
	if strings.TrimSpace(r.ArtifactID) == "" {
		return errors.New("artifact_id must not be blank")
	}
	if r.Version < 1 {
		return errors.New("artifact version must be a positive integer")
	}
	if !hashPattern.MatchString(r.ContentHash) {
		return errors.New("artifact content_hash must be a lowercase sha256 digest")
	}
	return nil
}

// QueryOrigin records why a query exists: the user's original question, a
// system rewrite, or a fallback after empty results.
type QueryOrigin string

const (
	QueryOriginOriginal QueryOrigin = "original"
	QueryOriginRewrite  QueryOrigin = "rewrite"
	QueryOriginFallback QueryOrigin = "fallback"
)

func (v QueryOrigin) Valid() bool {
	switch v {
	case QueryOriginOriginal, QueryOriginRewrite, QueryOriginFallback:
		return true
	default:
		return false
	}
}

type QueryPlanEntry struct {
	QueryID string      `json:"query_id"`
	Text    string      `json:"text"`
	Origin  QueryOrigin `json:"origin"`
}

type ProviderResult struct {
	Provider   string `json:"provider"`
	Status     string `json:"status"`
	ErrorCode  string `json:"error_code,omitempty"`
	RequestRef string `json:"request_ref,omitempty"`
}

// SelectionStatus is the screening outcome. A relevance score is not a
// credibility signal; unscored papers may still enter the selected workset
// through an explicit fallback policy (contracts.md §2).
type SelectionStatus string

const (
	SelectionStatusSelected SelectionStatus = "selected"
	SelectionStatusDeferred SelectionStatus = "deferred"
	SelectionStatusExcluded SelectionStatus = "excluded"
)

func (v SelectionStatus) Valid() bool {
	switch v {
	case SelectionStatusSelected, SelectionStatusDeferred, SelectionStatusExcluded:
		return true
	default:
		return false
	}
}

type AcquisitionStatus string

const (
	AcquisitionNotAttempted      AcquisitionStatus = "not_attempted"
	AcquisitionMetadataOnly      AcquisitionStatus = "metadata_only"
	AcquisitionAbstractAvailable AcquisitionStatus = "abstract_available"
	AcquisitionFullTextAvailable AcquisitionStatus = "full_text_available"
	AcquisitionFailed            AcquisitionStatus = "failed"
)

func (v AcquisitionStatus) Valid() bool {
	switch v {
	case AcquisitionNotAttempted, AcquisitionMetadataOnly, AcquisitionAbstractAvailable, AcquisitionFullTextAvailable, AcquisitionFailed:
		return true
	default:
		return false
	}
}

type RelevanceStatus string

const (
	RelevanceScored   RelevanceStatus = "scored"
	RelevanceUnscored RelevanceStatus = "unscored"
)

func (v RelevanceStatus) Valid() bool {
	switch v {
	case RelevanceScored, RelevanceUnscored:
		return true
	default:
		return false
	}
}

type PaperSelection struct {
	Status SelectionStatus `json:"status"`
	Score  *float64        `json:"score"`
	Reason string          `json:"reason"`
}

type PaperAcquisition struct {
	Status    AcquisitionStatus `json:"status"`
	OAURL     string            `json:"oa_url,omitempty"`
	License   string            `json:"license,omitempty"`
	ErrorCode string            `json:"error_code,omitempty"`
}

type PaperCandidate struct {
	PaperID         string           `json:"paper_id"`
	DOI             *string          `json:"doi"`
	Title           string           `json:"title"`
	Authors         []string         `json:"authors"`
	Year            *int             `json:"year"`
	Venue           *string          `json:"venue"`
	CanonicalURL    *string          `json:"canonical_url"`
	Aliases         []string         `json:"aliases"`
	Abstract        *string          `json:"abstract"`
	Selection       PaperSelection   `json:"selection"`
	Acquisition     PaperAcquisition `json:"acquisition"`
	RelevanceStatus RelevanceStatus  `json:"relevance_status"`
}

// ResearchCandidates is the research-candidates/1 Artifact content.
type ResearchCandidates struct {
	SchemaVersion   string           `json:"schema_version"`
	ContractHash    string           `json:"contract_hash"`
	QueryPlan       []QueryPlanEntry `json:"query_plan"`
	ProviderResults []ProviderResult `json:"provider_results"`
	Papers          []PaperCandidate `json:"papers"`
	PolicyVersion   string           `json:"policy_version"`
	Provenance      []string         `json:"provenance"`
}

func (c ResearchCandidates) Validate() error {
	if c.SchemaVersion != ResearchCandidatesSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ResearchCandidatesSchemaVersion)
	}
	if !hashPattern.MatchString(c.ContractHash) {
		return errors.New("contract_hash must be a lowercase sha256 digest")
	}
	if c.QueryPlan == nil || c.ProviderResults == nil || c.Papers == nil || c.Provenance == nil {
		return errors.New("query_plan, provider_results, papers, and provenance must be present")
	}
	if strings.TrimSpace(c.PolicyVersion) == "" {
		return errors.New("policy_version must not be blank")
	}
	seenQueries := make(map[string]struct{}, len(c.QueryPlan))
	for i, query := range c.QueryPlan {
		if strings.TrimSpace(query.QueryID) == "" {
			return fmt.Errorf("query_plan[%d].query_id must not be blank", i)
		}
		if _, exists := seenQueries[query.QueryID]; exists {
			return fmt.Errorf("duplicate query_plan query_id %q", query.QueryID)
		}
		seenQueries[query.QueryID] = struct{}{}
		if strings.TrimSpace(query.Text) == "" {
			return fmt.Errorf("query_plan[%d].text must not be blank", i)
		}
		if !query.Origin.Valid() {
			return fmt.Errorf("query_plan[%d] has invalid origin %q", i, query.Origin)
		}
	}
	for i, provider := range c.ProviderResults {
		if strings.TrimSpace(provider.Provider) == "" || strings.TrimSpace(provider.Status) == "" {
			return fmt.Errorf("provider_results[%d] must declare provider and status", i)
		}
	}
	seenPapers := make(map[string]struct{}, len(c.Papers))
	for i, paper := range c.Papers {
		if strings.TrimSpace(paper.PaperID) == "" {
			return fmt.Errorf("papers[%d].paper_id must not be blank", i)
		}
		if _, exists := seenPapers[paper.PaperID]; exists {
			return fmt.Errorf("duplicate paper_id %q", paper.PaperID)
		}
		seenPapers[paper.PaperID] = struct{}{}
		if strings.TrimSpace(paper.Title) == "" {
			return fmt.Errorf("papers[%d].title must not be blank", i)
		}
		if paper.Authors == nil || paper.Aliases == nil {
			return fmt.Errorf("papers[%d].authors and aliases must be present", i)
		}
		if paper.DOI != nil && strings.TrimSpace(*paper.DOI) == "" {
			return fmt.Errorf("papers[%d].doi must be null or non-blank", i)
		}
		if !paper.Selection.Status.Valid() {
			return fmt.Errorf("papers[%d] has invalid selection status %q", i, paper.Selection.Status)
		}
		if strings.TrimSpace(paper.Selection.Reason) == "" {
			return fmt.Errorf("papers[%d].selection.reason must not be blank", i)
		}
		if paper.Selection.Score != nil && (*paper.Selection.Score < 0 || *paper.Selection.Score > 3) {
			return fmt.Errorf("papers[%d].selection.score must be within 0..3", i)
		}
		if !paper.Acquisition.Status.Valid() {
			return fmt.Errorf("papers[%d] has invalid acquisition status %q", i, paper.Acquisition.Status)
		}
		if !paper.RelevanceStatus.Valid() {
			return fmt.Errorf("papers[%d] has invalid relevance_status %q", i, paper.RelevanceStatus)
		}
	}
	return nil
}

// PaperBibliography carries the citation metadata for one paper.
type PaperBibliography struct {
	Title        string   `json:"title"`
	Authors      []string `json:"authors"`
	Year         *int     `json:"year"`
	Venue        *string  `json:"venue"`
	DOI          *string  `json:"doi"`
	CanonicalURL *string  `json:"canonical_url"`
}

// PaperOrigin distinguishes user-uploaded material from externally
// discovered papers. User material must carry an owner-authorized reference.
type PaperOrigin string

const (
	PaperOriginUserMaterial PaperOrigin = "user_material"
	PaperOriginExternal     PaperOrigin = "external"
)

func (v PaperOrigin) Valid() bool {
	switch v {
	case PaperOriginUserMaterial, PaperOriginExternal:
		return true
	default:
		return false
	}
}

// ReadingScope is the coverage boundary of what was actually read.
// full_text means a readable full text existed and the recorded blocks were
// read; it never claims every page was inspected — truncated and
// read_block_ids carry the exact boundary. unread papers cannot produce
// citable evidence.
type ReadingScope string

const (
	ReadingScopeUnread   ReadingScope = "unread"
	ReadingScopeAbstract ReadingScope = "abstract"
	ReadingScopeFullText ReadingScope = "full_text"
)

func (v ReadingScope) Valid() bool {
	switch v {
	case ReadingScopeUnread, ReadingScopeAbstract, ReadingScopeFullText:
		return true
	default:
		return false
	}
}

type PaperEvidence struct {
	PaperID           string            `json:"paper_id"`
	Bibliography      PaperBibliography `json:"bibliography"`
	Origin            PaperOrigin       `json:"origin"`
	MaterialRef       *ArtifactRef      `json:"material_ref"`
	SelectionReason   string            `json:"selection_reason"`
	RelevanceStatus   RelevanceStatus   `json:"relevance_status"`
	ReadingScope      ReadingScope      `json:"reading_scope"`
	DocumentRef       *ArtifactRef      `json:"document_ref"`
	ParsedDocumentRef *ArtifactRef      `json:"parsed_document_ref"`
	ReadBlockIDs      []string          `json:"read_block_ids"`
	TotalBlocks       int               `json:"total_blocks"`
	Truncated         bool              `json:"truncated"`
	License           *string           `json:"license"`
}

// EvidenceScope marks which document a quote came from. Abstract documents
// are separate content Artifacts hashed on the abstract body; a full-text
// PDF hash must never back an abstract quote.
type EvidenceScope string

const (
	EvidenceScopeAbstract EvidenceScope = "abstract"
	EvidenceScopeFullText EvidenceScope = "full_text"
)

func (v EvidenceScope) Valid() bool {
	switch v {
	case EvidenceScopeAbstract, EvidenceScopeFullText:
		return true
	default:
		return false
	}
}

// Evidence is one verifiable excerpt.
//
// Offset semantics (contracts.md §2): StartChar/EndChar are Unicode code
// point offsets into the parsed block text, left-closed right-open
// ([start, end)). They are neither byte offsets nor grapheme clusters —
// "🚀" is one code point (four UTF-8 bytes), "👨‍👩‍👧" is five code points
// (one grapheme). The invariant `quote == block_text[start_char:end_char]`
// is checked by VerifyEvidenceQuote after the block hash is verified.
type Evidence struct {
	EvidenceID        string        `json:"evidence_id"`
	PaperID           string        `json:"paper_id"`
	DocumentRef       ArtifactRef   `json:"document_ref"`
	ParsedDocumentRef *ArtifactRef  `json:"parsed_document_ref"`
	BlockID           string        `json:"block_id"`
	BlockHash         string        `json:"block_hash"`
	Quote             string        `json:"quote"`
	StartChar         int           `json:"start_char"`
	EndChar           int           `json:"end_char"`
	Page              *int          `json:"page"`
	Section           *string       `json:"section"`
	EvidenceScope     EvidenceScope `json:"evidence_scope"`
}

// ClaimKind separates verifiable assertions from synthesis and hypotheses.
// rejected claims are never sent to the Writer as support material.
type ClaimKind string

const (
	ClaimKindSourceAssertion ClaimKind = "source_assertion"
	ClaimKindInterpretation  ClaimKind = "interpretation"
	ClaimKindHypothesis      ClaimKind = "hypothesis"
)

func (v ClaimKind) Valid() bool {
	switch v {
	case ClaimKindSourceAssertion, ClaimKindInterpretation, ClaimKindHypothesis:
		return true
	default:
		return false
	}
}

// ReviewStatus: approved records that a designated review action completed;
// it is not a scientific-truth guarantee. Writing authorization for the pack
// is intentionally separate from claim review.
type ClaimReviewStatus string

const (
	ClaimReviewPending  ClaimReviewStatus = "pending"
	ClaimReviewApproved ClaimReviewStatus = "approved"
	ClaimReviewRejected ClaimReviewStatus = "rejected"
)

func (v ClaimReviewStatus) Valid() bool {
	switch v {
	case ClaimReviewPending, ClaimReviewApproved, ClaimReviewRejected:
		return true
	default:
		return false
	}
}

type Claim struct {
	ClaimID      string            `json:"claim_id"`
	PaperID      string            `json:"paper_id"`
	Text         string            `json:"text"`
	Kind         ClaimKind         `json:"kind"`
	EvidenceIDs  []string          `json:"evidence_ids"`
	ReviewStatus ClaimReviewStatus `json:"review_status"`
	Limitations  []string          `json:"limitations"`
}

type EvidenceCoverage struct {
	Topics         []string `json:"topics"`
	Gaps           []string `json:"gaps"`
	Contradictions []string `json:"contradictions"`
}

// ResearchEvidencePack is the frozen research-evidence-pack/1 Artifact
// content. Its own hash lives in the Artifact envelope, never inside the
// content (which would be circular).
type ResearchEvidencePack struct {
	SchemaVersion string           `json:"schema_version"`
	ContractHash  string           `json:"contract_hash"`
	CandidatesRef ArtifactRef      `json:"candidates_ref"`
	Papers        []PaperEvidence  `json:"papers"`
	Evidence      []Evidence       `json:"evidence"`
	Claims        []Claim          `json:"claims"`
	Coverage      EvidenceCoverage `json:"coverage"`
	Provenance    []string         `json:"provenance"`
}

// Validate performs structural checks and all intra-pack reference checks:
// unique IDs, quote bounds, scope consistency, claim/evidence binding
// (source_assertion requires valid same-paper evidence), and the rule that
// unread papers cannot produce citable evidence.
func (p ResearchEvidencePack) Validate() error {
	if p.SchemaVersion != ResearchEvidencePackSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ResearchEvidencePackSchemaVersion)
	}
	if !hashPattern.MatchString(p.ContractHash) {
		return errors.New("contract_hash must be a lowercase sha256 digest")
	}
	if err := p.CandidatesRef.Validate(); err != nil {
		return fmt.Errorf("candidates_ref: %w", err)
	}
	if p.Papers == nil || p.Evidence == nil || p.Claims == nil || p.Coverage.Topics == nil || p.Coverage.Gaps == nil || p.Coverage.Contradictions == nil || p.Provenance == nil {
		return errors.New("papers, evidence, claims, coverage, and provenance must be present")
	}
	papers := make(map[string]PaperEvidence, len(p.Papers))
	for i, paper := range p.Papers {
		if err := validatePaperEvidence(paper); err != nil {
			return fmt.Errorf("papers[%d] (%s): %w", i, paper.PaperID, err)
		}
		if _, exists := papers[paper.PaperID]; exists {
			return fmt.Errorf("duplicate paper_id %q", paper.PaperID)
		}
		papers[paper.PaperID] = paper
	}
	evidenceIndex := make(map[string]Evidence, len(p.Evidence))
	for i, evidence := range p.Evidence {
		if err := validateEvidence(evidence); err != nil {
			return fmt.Errorf("evidence[%d] (%s): %w", i, evidence.EvidenceID, err)
		}
		if _, exists := evidenceIndex[evidence.EvidenceID]; exists {
			return fmt.Errorf("duplicate evidence_id %q", evidence.EvidenceID)
		}
		evidenceIndex[evidence.EvidenceID] = evidence
		paper, ok := papers[evidence.PaperID]
		if !ok {
			return fmt.Errorf("evidence[%d] references unknown paper_id %q", i, evidence.PaperID)
		}
		if paper.ReadingScope == ReadingScopeUnread {
			return fmt.Errorf("evidence[%d] references unread paper %q; unread papers cannot produce citable evidence", i, evidence.PaperID)
		}
		if evidence.EvidenceScope == EvidenceScopeFullText && paper.ReadingScope != ReadingScopeFullText {
			return fmt.Errorf("evidence[%d] claims full-text scope but paper %q was not read as full text", i, evidence.PaperID)
		}
	}
	for i, claim := range p.Claims {
		if err := validateClaim(claim, evidenceIndex); err != nil {
			return fmt.Errorf("claims[%d] (%s): %w", i, claim.ClaimID, err)
		}
		if _, ok := papers[claim.PaperID]; !ok {
			return fmt.Errorf("claims[%d] references unknown paper_id %q", i, claim.PaperID)
		}
	}
	claimIDs := make(map[string]struct{}, len(p.Claims))
	for _, claim := range p.Claims {
		claimIDs[claim.ClaimID] = struct{}{}
	}
	for i, contradiction := range p.Coverage.Contradictions {
		if _, isClaim := claimIDs[contradiction]; isClaim {
			continue
		}
		if _, isPaper := papers[contradiction]; isPaper {
			continue
		}
		return fmt.Errorf("coverage.contradictions[%d] references unknown claim or paper %q", i, contradiction)
	}
	return nil
}

func validatePaperEvidence(paper PaperEvidence) error {
	if strings.TrimSpace(paper.PaperID) == "" {
		return errors.New("paper_id must not be blank")
	}
	if strings.TrimSpace(paper.Bibliography.Title) == "" {
		return errors.New("bibliography.title must not be blank")
	}
	if paper.Bibliography.Authors == nil {
		return errors.New("bibliography.authors must be present")
	}
	if !paper.Origin.Valid() {
		return fmt.Errorf("invalid origin %q", paper.Origin)
	}
	if paper.Origin == PaperOriginUserMaterial && paper.MaterialRef == nil {
		return errors.New("user material must carry an owner-authorized material_ref")
	}
	if !paper.RelevanceStatus.Valid() {
		return fmt.Errorf("invalid relevance_status %q", paper.RelevanceStatus)
	}
	if !paper.ReadingScope.Valid() {
		return fmt.Errorf("invalid reading_scope %q", paper.ReadingScope)
	}
	if strings.TrimSpace(paper.SelectionReason) == "" {
		return errors.New("selection_reason must not be blank")
	}
	if paper.ReadBlockIDs == nil {
		return errors.New("read_block_ids must be present")
	}
	if paper.TotalBlocks < 0 {
		return errors.New("total_blocks must not be negative")
	}
	if len(paper.ReadBlockIDs) > paper.TotalBlocks {
		return errors.New("read_block_ids cannot exceed total_blocks")
	}
	seenBlocks := make(map[string]struct{}, len(paper.ReadBlockIDs))
	for i, blockID := range paper.ReadBlockIDs {
		if strings.TrimSpace(blockID) == "" {
			return fmt.Errorf("read_block_ids[%d] must not be blank", i)
		}
		if _, exists := seenBlocks[blockID]; exists {
			return fmt.Errorf("duplicate read_block_id %q", blockID)
		}
		seenBlocks[blockID] = struct{}{}
	}
	if paper.ReadingScope == ReadingScopeUnread && len(paper.ReadBlockIDs) > 0 {
		return errors.New("unread paper cannot record read blocks")
	}
	if paper.ReadingScope != ReadingScopeUnread {
		if paper.DocumentRef == nil {
			return errors.New("read papers must reference the read document_ref")
		}
		if err := paper.DocumentRef.Validate(); err != nil {
			return fmt.Errorf("document_ref: %w", err)
		}
	}
	if paper.ParsedDocumentRef != nil {
		if err := paper.ParsedDocumentRef.Validate(); err != nil {
			return fmt.Errorf("parsed_document_ref: %w", err)
		}
	}
	if paper.MaterialRef != nil {
		if err := paper.MaterialRef.Validate(); err != nil {
			return fmt.Errorf("material_ref: %w", err)
		}
	}
	return nil
}

func validateEvidence(evidence Evidence) error {
	if strings.TrimSpace(evidence.EvidenceID) == "" {
		return errors.New("evidence_id must not be blank")
	}
	if strings.TrimSpace(evidence.PaperID) == "" {
		return errors.New("paper_id must not be blank")
	}
	if err := evidence.DocumentRef.Validate(); err != nil {
		return fmt.Errorf("document_ref: %w", err)
	}
	if evidence.ParsedDocumentRef != nil {
		if err := evidence.ParsedDocumentRef.Validate(); err != nil {
			return fmt.Errorf("parsed_document_ref: %w", err)
		}
	}
	if strings.TrimSpace(evidence.BlockID) == "" {
		return errors.New("block_id must not be blank")
	}
	if !hashPattern.MatchString(evidence.BlockHash) {
		return errors.New("block_hash must be a lowercase sha256 digest")
	}
	if strings.TrimSpace(evidence.Quote) == "" {
		return errors.New("quote must not be blank")
	}
	// Offsets are Unicode code points, left-closed right-open: start <= end.
	if evidence.StartChar < 0 || evidence.StartChar > evidence.EndChar {
		return errors.New("start_char must be >= 0 and <= end_char")
	}
	if evidence.Page != nil && *evidence.Page < 1 {
		return errors.New("page must be null or start at 1")
	}
	if !evidence.EvidenceScope.Valid() {
		return fmt.Errorf("invalid evidence_scope %q", evidence.EvidenceScope)
	}
	return nil
}

func validateClaim(claim Claim, evidenceIndex map[string]Evidence) error {
	if strings.TrimSpace(claim.ClaimID) == "" {
		return errors.New("claim_id must not be blank")
	}
	if strings.TrimSpace(claim.PaperID) == "" {
		return errors.New("paper_id must not be blank")
	}
	if strings.TrimSpace(claim.Text) == "" {
		return errors.New("text must not be blank")
	}
	if !claim.Kind.Valid() {
		return fmt.Errorf("invalid kind %q", claim.Kind)
	}
	if !claim.ReviewStatus.Valid() {
		return fmt.Errorf("invalid review_status %q", claim.ReviewStatus)
	}
	if claim.EvidenceIDs == nil || claim.Limitations == nil {
		return errors.New("evidence_ids and limitations must be present")
	}
	if claim.Kind == ClaimKindSourceAssertion && len(claim.EvidenceIDs) == 0 {
		return errors.New("source_assertion requires at least one same-paper evidence")
	}
	for i, evidenceID := range claim.EvidenceIDs {
		evidence, ok := evidenceIndex[evidenceID]
		if !ok {
			return fmt.Errorf("evidence_ids[%d] references unknown evidence %q", i, evidenceID)
		}
		if claim.Kind == ClaimKindSourceAssertion && evidence.PaperID != claim.PaperID {
			return fmt.Errorf("source_assertion evidence %q belongs to paper %q, not %q", evidenceID, evidence.PaperID, claim.PaperID)
		}
	}
	return nil
}

// VerifyEvidenceQuote checks the §2 invariant
// quote == block_text[start_char:end_char] under Unicode code point offsets.
// Call it only after the block/document hashes are verified against stored
// content.
func VerifyEvidenceQuote(evidence Evidence, blockText string) bool {
	runes := []rune(blockText)
	if evidence.StartChar < 0 || evidence.EndChar > len(runes) || evidence.StartChar > evidence.EndChar {
		return false
	}
	return string(runes[evidence.StartChar:evidence.EndChar]) == evidence.Quote
}

type OutlineSection struct {
	SectionID    string   `json:"section_id"`
	Title        string   `json:"title"`
	CentralPoint string   `json:"central_point"`
	EvidenceIDs  []string `json:"evidence_ids"`
	Gaps         []string `json:"gaps"`
}

// ResearchOutline is the research-outline/1 Artifact content. Sections
// without evidence are allowed only for non-factual synthesis purposes
// (introduction, research gaps); that intent rule is enforced by the
// outline reviewer, not by this structural check.
type ResearchOutline struct {
	SchemaVersion   string           `json:"schema_version"`
	ContractHash    string           `json:"contract_hash"`
	EvidencePackRef ArtifactRef      `json:"evidence_pack_ref"`
	Sections        []OutlineSection `json:"sections"`
	Limitations     []string         `json:"limitations"`
}

func (o ResearchOutline) Validate() error {
	if o.SchemaVersion != ResearchOutlineSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ResearchOutlineSchemaVersion)
	}
	if !hashPattern.MatchString(o.ContractHash) {
		return errors.New("contract_hash must be a lowercase sha256 digest")
	}
	if err := o.EvidencePackRef.Validate(); err != nil {
		return fmt.Errorf("evidence_pack_ref: %w", err)
	}
	if o.Sections == nil || o.Limitations == nil {
		return errors.New("sections and limitations must be present")
	}
	seen := make(map[string]struct{}, len(o.Sections))
	for i, section := range o.Sections {
		if strings.TrimSpace(section.SectionID) == "" {
			return fmt.Errorf("sections[%d].section_id must not be blank", i)
		}
		if _, exists := seen[section.SectionID]; exists {
			return fmt.Errorf("duplicate section_id %q", section.SectionID)
		}
		seen[section.SectionID] = struct{}{}
		if strings.TrimSpace(section.Title) == "" || strings.TrimSpace(section.CentralPoint) == "" {
			return fmt.Errorf("sections[%d].title and central_point must not be blank", i)
		}
		if section.EvidenceIDs == nil || section.Gaps == nil {
			return fmt.Errorf("sections[%d].evidence_ids and gaps must be present", i)
		}
	}
	return nil
}

// ApprovedResearchOutline is an outline whose version was confirmed at the
// outline gate. It must reference the source outline revision it approves
// and the persisted gate decision.
type ApprovedResearchOutline struct {
	ResearchOutline
	SourceOutlineRef ArtifactRef `json:"source_outline_ref"`
	GateDecisionRef  ArtifactRef `json:"gate_decision_ref"`
}

func (o ApprovedResearchOutline) Validate() error {
	if err := o.ResearchOutline.Validate(); err != nil {
		return fmt.Errorf("approved outline: %w", err)
	}
	if err := o.SourceOutlineRef.Validate(); err != nil {
		return fmt.Errorf("source_outline_ref: %w", err)
	}
	if err := o.GateDecisionRef.Validate(); err != nil {
		return fmt.Errorf("gate_decision_ref: %w", err)
	}
	return nil
}

// ValidateOutlineAgainstPack checks cross-artifact references: the outline
// must bind to the same contract, and every section evidence ID must exist
// in the referenced evidence pack.
func ValidateOutlineAgainstPack(outline ResearchOutline, pack ResearchEvidencePack) error {
	if err := outline.Validate(); err != nil {
		return err
	}
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("evidence pack: %w", err)
	}
	if outline.ContractHash != pack.ContractHash {
		return errors.New("outline and evidence pack belong to different contracts")
	}
	evidenceIDs := make(map[string]struct{}, len(pack.Evidence))
	for _, evidence := range pack.Evidence {
		evidenceIDs[evidence.EvidenceID] = struct{}{}
	}
	for i, section := range outline.Sections {
		for j, evidenceID := range section.EvidenceIDs {
			if _, ok := evidenceIDs[evidenceID]; !ok {
				return fmt.Errorf("sections[%d].evidence_ids[%d] references unknown evidence %q", i, j, evidenceID)
			}
		}
	}
	return nil
}

// EvidenceApproval is the server-created evidence gate decision record.
// Clients can never write actor_id or decided_at, and the only allowed
// decision is approve.
type EvidenceApproval struct {
	SchemaVersion   string      `json:"schema_version"`
	GateID          string      `json:"gate_id"`
	ActorID         string      `json:"actor_id"`
	DecidedAt       string      `json:"decided_at"`
	PlanHash        string      `json:"plan_hash"`
	EvidencePackRef ArtifactRef `json:"evidence_pack_ref"`
	Decision        string      `json:"decision"`
}

func (a EvidenceApproval) Validate() error {
	if a.SchemaVersion != EvidenceApprovalSchemaVersion {
		return fmt.Errorf("schema_version must be %q", EvidenceApprovalSchemaVersion)
	}
	if strings.TrimSpace(a.GateID) == "" || strings.TrimSpace(a.ActorID) == "" {
		return errors.New("gate_id and actor_id must not be blank")
	}
	if _, err := time.Parse(time.RFC3339, a.DecidedAt); err != nil {
		return fmt.Errorf("decided_at must be RFC3339: %w", err)
	}
	if !hashPattern.MatchString(a.PlanHash) {
		return errors.New("plan_hash must be a lowercase sha256 digest")
	}
	if err := a.EvidencePackRef.Validate(); err != nil {
		return fmt.Errorf("evidence_pack_ref: %w", err)
	}
	if a.Decision != "approve" {
		return fmt.Errorf("decision must be %q", "approve")
	}
	return nil
}

// CitationEntry locates one citation marker inside the draft: BlockID names
// the draft block (the outline section) containing the marker, and
// StartChar/EndChar are Unicode code point offsets over the whole draft text
// (left-closed right-open), so the marker is self-locating for validators and
// the workbench. Rendering-level numbering may drift; CitationID/EvidenceID
// bindings may not.
type CitationEntry struct {
	CitationID string `json:"citation_id"`
	EvidenceID string `json:"evidence_id"`
	BlockID    string `json:"block_id"`
	StartChar  int    `json:"start_char"`
	EndChar    int    `json:"end_char"`
}

// ResearchCitationIndex is produced alongside the research draft and consumed
// by the citation validator (T07). DraftHash binds the index to one exact
// draft version.
type ResearchCitationIndex struct {
	SchemaVersion   string          `json:"schema_version"`
	ContractHash    string          `json:"contract_hash"`
	DraftHash       string          `json:"draft_hash"`
	EvidencePackRef ArtifactRef     `json:"evidence_pack_ref"`
	Citations       []CitationEntry `json:"citations"`
}

func (c ResearchCitationIndex) Validate() error {
	if c.SchemaVersion != ResearchCitationIndexSchemaVersion {
		return fmt.Errorf("schema_version must be %q", ResearchCitationIndexSchemaVersion)
	}
	if !hashPattern.MatchString(c.ContractHash) {
		return errors.New("contract_hash must be a lowercase sha256 digest")
	}
	if !hashPattern.MatchString(c.DraftHash) {
		return errors.New("draft_hash must be a lowercase sha256 digest")
	}
	if err := c.EvidencePackRef.Validate(); err != nil {
		return fmt.Errorf("evidence_pack_ref: %w", err)
	}
	if c.Citations == nil {
		return errors.New("citations must be present")
	}
	seen := make(map[string]struct{}, len(c.Citations))
	for i, citation := range c.Citations {
		if strings.TrimSpace(citation.CitationID) == "" {
			return fmt.Errorf("citations[%d].citation_id must not be blank", i)
		}
		if _, exists := seen[citation.CitationID]; exists {
			return fmt.Errorf("duplicate citation_id %q", citation.CitationID)
		}
		seen[citation.CitationID] = struct{}{}
		if citation.StartChar < 0 || citation.StartChar > citation.EndChar {
			return fmt.Errorf("citations[%d] has invalid code point interval", i)
		}
	}
	return nil
}

// ValidateCitationIndexAgainstPack checks that every citation resolves to
// evidence inside the referenced pack and that the pack/contract bindings
// match.
func ValidateCitationIndexAgainstPack(index ResearchCitationIndex, pack ResearchEvidencePack) error {
	if err := index.Validate(); err != nil {
		return err
	}
	if err := pack.Validate(); err != nil {
		return fmt.Errorf("evidence pack: %w", err)
	}
	if index.ContractHash != pack.ContractHash {
		return errors.New("citation index and evidence pack belong to different contracts")
	}
	evidenceIDs := make(map[string]struct{}, len(pack.Evidence))
	for _, evidence := range pack.Evidence {
		evidenceIDs[evidence.EvidenceID] = struct{}{}
	}
	for i, citation := range index.Citations {
		if _, ok := evidenceIDs[citation.EvidenceID]; !ok {
			return fmt.Errorf("citations[%d] references unknown evidence %q", i, citation.EvidenceID)
		}
	}
	return nil
}
