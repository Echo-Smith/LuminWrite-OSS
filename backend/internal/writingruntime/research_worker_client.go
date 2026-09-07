package writingruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/scholar"
)

// Typed parse/read access to the Scholar Worker, layered on the T04 client
// (Client.Call + HashPayload already enforce the envelope, echo validation,
// and the cross-language canonical input_hash). T05 keeps these two adapters
// in the runtime package so the scholar client's T04 surface stays frozen;
// promoting them into internal/scholar is a mechanical follow-up.

// ParseOutputs mirrors the worker parse response outputs
// (services/scholar-worker/src/lumin_scholar/parser.py).
type ParseOutputs struct {
	Blocks   []ParsedBlock `json:"blocks"`
	Coverage ParseCoverage `json:"coverage"`
}

// ParsedBlock is one hash-verified text block. Page is non-nil only for
// paginated sources (PDF, 1-based); TXT/MD/abstract blocks never carry a page.
type ParsedBlock struct {
	BlockID   string `json:"block_id"`
	Text      string `json:"text"`
	Page      *int   `json:"page"`
	BlockHash string `json:"block_hash"`
}

// ParseCoverage reports the parse boundary: truncated marks the
// MAX_BLOCKS_PER_DOC cap, likely_scanned marks a PDF without /Font resources.
type ParseCoverage struct {
	MediaType       string `json:"media_type"`
	ParserVersion   string `json:"parser_version"`
	TotalBlocks     int    `json:"total_blocks"`
	TotalCodepoints int    `json:"total_codepoints"`
	Complete        bool   `json:"complete"`
	Truncated       bool   `json:"truncated"`
	LikelyScanned   *bool  `json:"likely_scanned"`
}

// ReadOutputs mirrors the worker read response outputs
// (services/scholar-worker/src/lumin_scholar/reader.py). The worker has
// already self-validated every evidence entry against the request blocks;
// the host re-verifies (research_pack.go) before anything enters a pack.
type ReadOutputs struct {
	PaperID             string              `json:"paper_id"`
	Claims              []ReaderClaimOutput `json:"claims"`
	Evidence            []ReaderEvidence    `json:"evidence"`
	Limitations         []string            `json:"limitations"`
	BlocksRead          []string            `json:"blocks_read"`
	ReaderVersion       string              `json:"reader_version,omitempty"`
	ReaderPolicyVersion string              `json:"reader_policy_version,omitempty"`
}

// ReaderClaimOutput is one reader claim before pack assembly assigns the
// kernel claim ids and review status (all claims start pending).
type ReaderClaimOutput struct {
	ClaimID     string   `json:"claim_id"`
	Text        string   `json:"text"`
	Kind        string   `json:"kind"`
	EvidenceIDs []string `json:"evidence_ids"`
	Limitations []string `json:"limitations"`
}

// ReaderEvidence is one worker-validated excerpt: quote must equal
// block text[start_char:end_char] under Unicode code point offsets.
type ReaderEvidence struct {
	EvidenceID    string `json:"evidence_id"`
	BlockID       string `json:"block_id"`
	BlockHash     string `json:"block_hash"`
	Quote         string `json:"quote"`
	StartChar     int    `json:"start_char"`
	EndChar       int    `json:"end_char"`
	Page          *int   `json:"page"`
	EvidenceScope string `json:"evidence_scope"`
}

// ReaderBlock is one block sent to the read operation (≤24, contracts.md §4):
// text plus its parse hash so the worker can verify integrity before reading.
type ReaderBlock struct {
	BlockID   string `json:"block_id"`
	Text      string `json:"text"`
	BlockHash string `json:"block_hash"`
	Page      *int   `json:"page,omitempty"`
}

// ReaderPolicy pins the reader/prompt version the ledger input_hash covers.
type ReaderPolicy struct {
	ReaderPolicyVersion string `json:"reader_policy_version"`
}

// ResearchWorkerClient is the parse/read/fetch surface the read executor
// needs. *scholar.Client implements it via the adapters below; tests inject
// fakes that count calls (cache-reuse assertions).
type ResearchWorkerClient interface {
	FetchFullText(ctx context.Context, paperID, oaURL string, sizeLimit int64, opts ...scholar.CallOption) (*scholar.FetchFullTextOutputs, *scholar.OperationResponse, error)
	ParseDocument(ctx context.Context, document []byte, mediaType, parserVersion string, opts ...scholar.CallOption) (*ParseOutputs, *scholar.OperationResponse, error)
	ReadPaper(ctx context.Context, researchQuestion, paperID string, blocks []ReaderBlock, policy ReaderPolicy, opts ...scholar.CallOption) (*ReadOutputs, *scholar.OperationResponse, error)
}

// ScholarParseRead adapts *scholar.Client to ResearchWorkerClient.
type ScholarParseRead struct{ Client *scholar.Client }

// ParseDocument calls the worker parse op with an inline base64 document.
func (adapter ScholarParseRead) ParseDocument(ctx context.Context, document []byte, mediaType, parserVersion string, opts ...scholar.CallOption) (*ParseOutputs, *scholar.OperationResponse, error) {
	payload := map[string]any{
		"document":       base64.StdEncoding.EncodeToString(document),
		"media_type":     mediaType,
		"parser_version": parserVersion,
	}
	inputHash, err := scholar.HashPayload(payload)
	if err != nil {
		return nil, nil, &scholar.Error{Kind: scholar.ErrProtocol, Code: "client_hash_payload", Message: err.Error()}
	}
	resp, err := adapter.Client.Call(ctx, scholar.OpParse, inputHash, payload, opts...)
	if err != nil {
		return nil, nil, err
	}
	var outputs ParseOutputs
	if err := json.Unmarshal(resp.Outputs, &outputs); err != nil {
		return nil, nil, &scholar.Error{Kind: scholar.ErrProtocol, HTTPStatus: 200,
			Code: "client_bad_parse_outputs", Message: fmt.Sprintf("decode parse outputs: %v", err)}
	}
	return &outputs, resp, nil
}

// ReadPaper calls the worker read op over at most 24 verified blocks.
func (adapter ScholarParseRead) ReadPaper(ctx context.Context, researchQuestion, paperID string, blocks []ReaderBlock, policy ReaderPolicy, opts ...scholar.CallOption) (*ReadOutputs, *scholar.OperationResponse, error) {
	payloadBlocks := make([]map[string]any, 0, len(blocks))
	for _, block := range blocks {
		entry := map[string]any{
			"block_id":   block.BlockID,
			"text":       block.Text,
			"block_hash": block.BlockHash,
		}
		if block.Page != nil {
			entry["page"] = *block.Page
		}
		payloadBlocks = append(payloadBlocks, entry)
	}
	payload := map[string]any{
		"research_question": researchQuestion,
		"paper_id":          paperID,
		"blocks":            payloadBlocks,
		"reader_policy":     map[string]any{"reader_policy_version": policy.ReaderPolicyVersion},
	}
	inputHash, err := scholar.HashPayload(payload)
	if err != nil {
		return nil, nil, &scholar.Error{Kind: scholar.ErrProtocol, Code: "client_hash_payload", Message: err.Error()}
	}
	resp, err := adapter.Client.Call(ctx, scholar.OpRead, inputHash, payload, opts...)
	if err != nil {
		return nil, nil, err
	}
	var outputs ReadOutputs
	if err := json.Unmarshal(resp.Outputs, &outputs); err != nil {
		return nil, nil, &scholar.Error{Kind: scholar.ErrProtocol, HTTPStatus: 200,
			Code: "client_bad_read_outputs", Message: fmt.Sprintf("decode read outputs: %v", err)}
	}
	return &outputs, resp, nil
}
