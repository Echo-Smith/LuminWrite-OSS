package scholar

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Typed outputs mirroring the worker's T04 operation responses
// (services/scholar-worker/src/lumin_scholar/{discovery,operations}.py).
// The client validates the envelope; these types decode `outputs` for each
// operation so Go callers work with typed records instead of raw JSON.

// ProviderResult is one source's outcome in a discover response
// (status: ok | error; error_code present only on error).
type ProviderResult struct {
	Provider  string  `json:"provider"`
	Status    string  `json:"status"`
	Returned  int     `json:"returned"`
	ErrorCode *string `json:"error_code,omitempty"`
}

// PaperCandidate is a merged discovery record (contracts.md §2 shape).
// DOI is the R03-normalised value; Aliases keeps the original provider IDs
// (plus a "doi:<doi>" alias); PossibleDuplicate marks records that share a
// title but conflict on DOI and were therefore NOT merged.
type PaperCandidate struct {
	PaperID           string   `json:"paper_id"`
	DOI               *string  `json:"doi"`
	Title             *string  `json:"title"`
	Authors           []string `json:"authors"`
	Year              *int64   `json:"year"`
	Venue             *string  `json:"venue"`
	CanonicalURL      *string  `json:"canonical_url"`
	Aliases           []string `json:"aliases"`
	Abstract          *string  `json:"abstract"`
	OAURL             *string  `json:"oa_url"`
	Providers         []string `json:"providers"`
	PossibleDuplicate bool     `json:"possible_duplicate"`
}

// DiscoverOutputs is the decoded discover response.
type DiscoverOutputs struct {
	Papers          []PaperCandidate `json:"papers"`
	ProviderResults []ProviderResult `json:"provider_results"`
}

// RankScore is one per-paper relevance verdict (0..3; contracts.md §4).
type RankScore struct {
	PaperID string  `json:"paper_id"`
	Score   float64 `json:"score"`
	Reason  string  `json:"reason"`
}

// RankOutputs is the decoded rank response.
type RankOutputs struct {
	Scores []RankScore `json:"scores"`
}

// BlobTransfer mirrors the worker's blob envelope. T04 always uses
// inline_base64 (blob_url null); T05/T09 switches to task-scoped upload
// URLs served by the Go content service.
type BlobTransfer struct {
	Transport string  `json:"transport"`
	BlobURL   *string `json:"blob_url"`
}

// FetchFullTextOutputs is the decoded fetch_full_text response. Content is
// the raw bytes decoded from content_base64; ContentHash is the SHA-256 of
// those bytes ("sha256:<hex>").
type FetchFullTextOutputs struct {
	PaperID             string       `json:"paper_id"`
	AcquisitionStatus   string       `json:"acquisition_status"`
	ContentHash         string       `json:"content_hash"`
	SizeBytes           int64        `json:"size_bytes"`
	MediaType           string       `json:"media_type"`
	ContentTypeReported string       `json:"content_type_reported"`
	LooksLikePDF        bool         `json:"looks_like_pdf"`
	LikelyScanned       *bool        `json:"likely_scanned"`
	FinalURL            string       `json:"final_url"`
	RedirectHops        int          `json:"redirect_hops"`
	ContentBase64       string       `json:"content_base64"`
	Blob                BlobTransfer `json:"blob"`
}

// decodeOutputsJSON unmarshals raw outputs into T, requiring non-empty input.
func decodeOutputsJSON[T any](raw json.RawMessage) (*T, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("scholar: outputs is empty")
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("scholar: decode outputs: %w", err)
	}
	return &out, nil
}

// DecodeDiscoverOutputs parses a discover response's outputs.
func DecodeDiscoverOutputs(raw json.RawMessage) (*DiscoverOutputs, error) {
	return decodeOutputsJSON[DiscoverOutputs](raw)
}

// DecodeRankOutputs parses a rank response's outputs.
func DecodeRankOutputs(raw json.RawMessage) (*RankOutputs, error) {
	return decodeOutputsJSON[RankOutputs](raw)
}

// DecodeFetchFullTextOutputs parses a fetch_full_text response's outputs.
func DecodeFetchFullTextOutputs(raw json.RawMessage) (*FetchFullTextOutputs, error) {
	return decodeOutputsJSON[FetchFullTextOutputs](raw)
}

// Content decodes the inline base64 blob into raw bytes. The hash itself is
// verified by the caller (Go host) against ContentHash before commit.
func (f *FetchFullTextOutputs) Content() []byte {
	decoded, err := base64.StdEncoding.DecodeString(f.ContentBase64)
	if err != nil {
		return nil
	}
	return decoded
}
