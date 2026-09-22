package scholar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"time"
)

// hashText/hashBytes implement the shared "sha256:<hex>" convention for
// document and block hashes (cross-language convention pinned in
// contracts.md §2).
func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return sha256Prefix + hex.EncodeToString(sum[:])
}

func hashText(text string) string {
	return hashBytes([]byte(text))
}

const sha256Prefix = "sha256:"

// Executor holds the operation dependencies: the LLM configuration (rank +
// read), the binary-document text extractor (parse), and the outbound pacing
// hook (discover/rank/fetch). Constructed by NewClient; tests build one
// directly with injected stubs.
type Executor struct {
	cfg     LLMConfig
	parser  DocumentParser
	limiter RateLimiter
}

// llm returns the effective LLM configuration: explicit override first, then
// the environment.
func (e *Executor) llm() (LLMConfig, bool) {
	if e.cfg.BaseURL != "" || e.cfg.Model != "" || e.cfg.APIKey != "" {
		return e.cfg, true
	}
	return LLMConfigFromEnv(nil)
}

// discoverCallTimeout bounds one provider fan-out unit.
const discoverCallTimeout = 90 * time.Second

// Discover runs one bounded discovery unit: a query against the given
// provider allowlist (subset of openalex/crossref/semantic_scholar —
// anything else is rejected before any network call), returning merged
// PaperCandidate records plus per-source statuses. All-provider failure
// surfaces as papers empty with every status entry an error — the caller
// maps that to a distinct aggregate error (never "no results").
func (e *Executor) Discover(ctx context.Context, query string, providerAllowlist []string, limit int) (*DiscoverOutputs, Usage, []string, error) {
	if len(providerAllowlist) == 0 {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_empty_provider_allowlist",
			Message: "provider allowlist must not be empty"}
	}
	if err := checkAllowlist(providerAllowlist); err != nil {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_unknown_provider", Message: err.Error()}
	}
	sorted := append([]string(nil), providerAllowlist...)
	sort.Strings(sorted)

	if limit <= 0 {
		limit = 10
	}
	unitCtx, cancel := context.WithTimeout(ctx, discoverCallTimeout)
	defer cancel()

	var records []RawProviderRecord
	statuses := []ProviderResult{}
	for _, provider := range sorted {
		if e.limiter != nil {
			if err := e.limiter.Wait(unitCtx, OpDiscover); err != nil {
				return nil, Usage{}, nil, &Error{Kind: ErrRemote, Code: "op_rate_limited",
					Message: "rate limiter rejected discover before send: " + err.Error()}
			}
		}
		recs, status := searchProvider(unitCtx, provider, query, limit)
		statuses = append(statuses, status)
		records = append(records, recs...)
	}

	papers, mergeWarnings := mergeRecords(records)
	warnings := append([]string{}, mergeWarnings...)
	for _, status := range statuses {
		if status.Status == "error" && status.ErrorCode != nil {
			warnings = append(warnings, "provider "+status.Provider+" error: "+*status.ErrorCode)
		}
	}
	return &DiscoverOutputs{Papers: papers, ProviderResults: statuses}, Usage{}, warnings, nil
}

// Rank runs one bounded semantic-ranking unit over at most 8 candidates.
// Every input paper_id must come back exactly once with a 0..3 score;
// missing/extra/duplicate IDs are errors (fail-closed, never coerced).
func (e *Executor) Rank(ctx context.Context, researchQuestion string, candidates []RankCandidate) (*RankOutputs, Usage, []string, error) {
	if len(candidates) == 0 || len(candidates) > 8 {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_bad_rank_batch",
			Message: fmt.Sprintf("rank batch must hold 1..8 candidates, got %d", len(candidates))}
	}
	cfg, ok := e.llm()
	if !ok {
		return nil, Usage{}, nil, &Error{Kind: ErrRemote, Code: "llm_not_configured",
			Message: "semantic ranking requires SCHOLAR_LLM_BASE_URL, SCHOLAR_LLM_MODEL and SCHOLAR_LLM_API_KEY to be configured"}
	}
	seen := map[string]bool{}
	for _, cand := range candidates {
		if seen[cand.PaperID] {
			return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_duplicate_paper_id",
				Message: fmt.Sprintf("paper_id %q appears more than once", cand.PaperID)}
		}
		seen[cand.PaperID] = true
	}
	if e.limiter != nil {
		if err := e.limiter.Wait(ctx, OpRank); err != nil {
			return nil, Usage{}, nil, &Error{Kind: ErrRemote, Code: "op_rate_limited",
				Message: "rate limiter rejected rank before send: " + err.Error()}
		}
	}

	messages := buildRankMessages(researchQuestion, candidates)
	rawText, usage, callErr := chatCall(ctx, cfg, messages, 60*time.Second)
	if callErr != nil {
		return nil, usage, nil, callErr
	}
	scores, err := parseRankResponse(rawText, candidates)
	if err != nil {
		return nil, usage, nil, err
	}
	return &RankOutputs{Scores: scores}, usage, nil, nil
}

// FetchFullText runs one bounded download of an OA URL through the
// constrained downloader. The OA URL must come from a prior discovery result
// (PaperCandidate.OAURL); the downloader enforces SSRF/size/type constraints
// and returns the SHA-256 content hash with inline base64 content.
func (e *Executor) FetchFullText(ctx context.Context, paperID, oaURL string, sizeLimit int64) (*FetchFullTextOutputs, Usage, []string, error) {
	if sizeLimit <= 0 || sizeLimit > 25*1024*1024 {
		return nil, Usage{}, nil, &Error{Kind: ErrProtocol, Code: "client_bad_size_limit",
			Message: fmt.Sprintf("size_limit must be 1..%d bytes", 25*1024*1024)}
	}
	result, err := constrainedDownload(ctx, oaURL, sizeLimit, defaultIPPolicy{}, 0)
	if err != nil {
		return nil, Usage{}, nil, err
	}
	return &FetchFullTextOutputs{
		PaperID:             paperID,
		AcquisitionStatus:   "full_text_available",
		ContentHash:         result.ContentHash,
		SizeBytes:           result.SizeBytes,
		MediaType:           result.MediaType,
		ContentTypeReported: result.ContentTypeReported,
		LooksLikePDF:        result.LooksLikePDF,
		LikelyScanned:       result.LikelyScanned,
		FinalURL:            result.FinalURL,
		RedirectHops:        result.RedirectHops,
		ContentBase64:       base64Std(result.Content),
	}, Usage{}, nil, nil
}
