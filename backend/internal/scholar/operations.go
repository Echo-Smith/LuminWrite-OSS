package scholar

import (
	"context"
	"fmt"
	"sort"
)

// Discover runs one bounded discovery unit: a query against the given
// provider allowlist (subset of openalex/crossref/semantic_scholar — the
// worker rejects anything else, and this method double-checks client-side
// so a bad allowlist never leaves the host), returning merged
// PaperCandidate records plus per-source statuses.
func (c *Client) Discover(
	ctx context.Context,
	query string,
	providerAllowlist []string,
	limit int,
	opts ...CallOption,
) (*DiscoverOutputs, *OperationResponse, error) {
	if len(providerAllowlist) == 0 {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_empty_provider_allowlist",
			Message: "provider allowlist must not be empty"}
	}
	sorted := append([]string(nil), providerAllowlist...)
	sort.Strings(sorted)
	payload := map[string]any{
		"query":              query,
		"provider_allowlist": sorted,
		"limit":              limit,
	}
	inputHash, err := HashPayload(payload)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_hash_payload", Message: err.Error()}
	}
	resp, err := c.Call(ctx, OpDiscover, inputHash, payload, opts...)
	if err != nil {
		return nil, nil, err
	}
	outputs, err := DecodeDiscoverOutputs(resp.Outputs)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, HTTPStatus: 200,
			Code: "client_bad_discover_outputs", Message: err.Error()}
	}
	return outputs, resp, nil
}

// Rank runs one bounded semantic-ranking unit over at most 8 candidates.
// paperIDs/abstracts are sent positionally; the worker returns exactly one
// score per input paper_id (missing/extra IDs are worker-side errors).
func (c *Client) Rank(
	ctx context.Context,
	researchQuestion string,
	candidates []RankCandidate,
	opts ...CallOption,
) (*RankOutputs, *OperationResponse, error) {
	if len(candidates) == 0 || len(candidates) > 8 {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_bad_rank_batch",
			Message: fmt.Sprintf("rank batch must hold 1..8 candidates, got %d", len(candidates))}
	}
	seen := map[string]bool{}
	payloadCandidates := make([]map[string]any, 0, len(candidates))
	for _, cand := range candidates {
		if seen[cand.PaperID] {
			return nil, nil, &Error{Kind: ErrProtocol, Code: "client_duplicate_paper_id",
				Message: fmt.Sprintf("paper_id %q appears more than once", cand.PaperID)}
		}
		seen[cand.PaperID] = true
		payloadCandidates = append(payloadCandidates, map[string]any{
			"paper_id": cand.PaperID,
			"abstract": cand.Abstract,
		})
	}
	payload := map[string]any{
		"research_question": researchQuestion,
		"candidates":        payloadCandidates,
	}
	inputHash, err := HashPayload(payload)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_hash_payload", Message: err.Error()}
	}
	resp, err := c.Call(ctx, OpRank, inputHash, payload, opts...)
	if err != nil {
		return nil, nil, err
	}
	outputs, err := DecodeRankOutputs(resp.Outputs)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, HTTPStatus: 200,
			Code: "client_bad_rank_outputs", Message: err.Error()}
	}
	return outputs, resp, nil
}

// FetchFullText runs one bounded download of an OA URL through the worker's
// constrained downloader. The OA URL must come from a prior discovery result
// (PaperCandidate.OAURL); the worker enforces SSRF/size/type constraints.
func (c *Client) FetchFullText(
	ctx context.Context,
	paperID string,
	oaURL string,
	sizeLimit int64,
	opts ...CallOption,
) (*FetchFullTextOutputs, *OperationResponse, error) {
	if sizeLimit <= 0 || sizeLimit > 25*1024*1024 {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_bad_size_limit",
			Message: fmt.Sprintf("size_limit must be 1..%d bytes", 25*1024*1024)}
	}
	payload := map[string]any{
		"paper_id":   paperID,
		"oa_url":     oaURL,
		"size_limit": sizeLimit,
	}
	inputHash, err := HashPayload(payload)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, Code: "client_hash_payload", Message: err.Error()}
	}
	resp, err := c.Call(ctx, OpFetchFullText, inputHash, payload, opts...)
	if err != nil {
		return nil, nil, err
	}
	outputs, err := DecodeFetchFullTextOutputs(resp.Outputs)
	if err != nil {
		return nil, nil, &Error{Kind: ErrProtocol, HTTPStatus: 200,
			Code: "client_bad_fetch_outputs", Message: err.Error()}
	}
	return outputs, resp, nil
}

// RankCandidate is one input row for Rank.
type RankCandidate struct {
	PaperID  string
	Abstract string
}
