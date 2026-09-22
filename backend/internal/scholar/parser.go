package scholar

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Document parsing into bounded, hash-verified text blocks. TXT/Markdown are
// segmented natively; PDF bytes are delegated to the docreader sidecar (the
// shared parsing surface used by knowledge-base uploads) and its extracted
// text is segmented the same way. A hard parse failure is a typed error,
// never a silent empty document.
const (
	// BlockTargetCodepoints is the ~target block size (Unicode code points).
	BlockTargetCodepoints = 1200
	// MaxBlocksPerDoc caps blocks per document; hitting it marks the output
	// truncated with complete=false.
	MaxBlocksPerDoc = 400
	// MinChunkCodepoints is the minimum worthwhile whitespace-split point.
	MinChunkCodepoints = 40
	// ParserVersion is recorded in pack provenance.
	ParserVersion = "parser/1"

	MediaTypePDF = "application/pdf"
	MediaTypeTextPlain = "text/plain"
	MediaTypeTextMarkdown = "text/markdown"
)

// ParseError is a typed parse failure mapped to an operation error.
type ParseError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *ParseError) Error() string { return e.Code + ": " + e.Message }

// TextBlock is one bounded block: stable deterministic id, sha256 over its
// UTF-8 text, and an optional 1-based page number (never fabricated for
// TXT/abstract — contracts.md §2).
type TextBlock struct {
	BlockID   string `json:"block_id"`
	Text      string `json:"text"`
	Page      *int64 `json:"page"`
	BlockHash string `json:"block_hash"`
}

// ParseCoverage records what the parse covered.
type ParseCoverage struct {
	MediaType       string `json:"media_type"`
	ParserVersion   string `json:"parser_version"`
	TotalBlocks     int    `json:"total_blocks"`
	TotalCodepoints int    `json:"total_codepoints"`
	Complete        bool   `json:"complete"`
	Truncated       bool   `json:"truncated"`
	LikelyScanned   bool   `json:"likely_scanned"`
}

// ParseOutputs is the parse operation's result.
type ParseOutputs struct {
	Blocks    []TextBlock   `json:"blocks"`
	Coverage  ParseCoverage `json:"coverage"`
	Warnings  []string      `json:"warnings"`
}

// DocumentParser supplies extracted text for binary documents (PDF). The
// production implementation delegates to the docreader TCP sidecar; tests
// inject a stub. Implementations must NOT write caller-supplied bytes to
// attacker-influenced paths.
type DocumentParser interface {
	// ExtractText returns the document's text layer for a binary document
	// (media type application/pdf). likelyScanned reports the extractor's
	// scanned-document guess (nil when it has no clue).
	ExtractText(ctx extractionContext) (text string, likelyScanned *bool, err error)
}

// extractionContext carries one parse unit: raw bytes and their media type.
type extractionContext struct {
	Content   []byte
	MediaType string
}

// ParseDocument parses one document into the parse operation's outputs.
// Raises *ParseError for unreadable or unsupported payloads.
func ParseDocument(ctx parsingContext) (*ParseOutputs, error) {
	var documentText string
	var likelyScanned bool
	var pageBoundaries bool

	switch ctx.MediaType {
	case MediaTypePDF:
		if ctx.ExtractText == nil {
			return nil, &ParseError{Code: "parse_extractor_unconfigured",
				Message: "no document parser configured for application/pdf"}
		}
		text, scanned, err := ctx.ExtractText.ExtractText(extractionContext{Content: ctx.Content, MediaType: ctx.MediaType})
		if err != nil {
			return nil, err
		}
		documentText = text
		if scanned != nil {
			likelyScanned = *scanned
		} else {
			// No clue from the extractor: empty or near-empty text on a PDF
			// means the text layer is missing (likely a scan).
			likelyScanned = utf8.RuneCountInString(strings.TrimSpace(text)) < 32
		}
		pageBoundaries = true
	case MediaTypeTextPlain, MediaTypeTextMarkdown:
		decoded, err := decodeUTF8(ctx.Content)
		if err != nil {
			return nil, &ParseError{Code: "parse_text_invalid",
				Message: "document is not valid UTF-8: " + err.Error()}
		}
		documentText = decoded
	default:
		return nil, &ParseError{Code: "unsupported_media_type",
			Message: fmt.Sprintf("media_type must be %s, %s or %s", MediaTypePDF, MediaTypeTextPlain, MediaTypeTextMarkdown)}
	}

	// For PDF text the extractor emits \f page separators so blocks keep
	// their true 1-based page numbers; plain text splits on blank lines.
	blocks, complete := blocksFromPlainText(documentText, pageBoundaries)
	totalCodepoints := utf8.RuneCountInString(documentText)

	warnings := []string{}
	if len(blocks) == 0 {
		warnings = append(warnings, "document produced no text blocks")
	}
	if likelyScanned {
		warnings = append(warnings,
			"PDF text layer appears empty or image-based; likely a scanned document — "+
				"text extraction may be incomplete")
	}

	return &ParseOutputs{
		Blocks:   blocks,
		Coverage: ParseCoverage{
			MediaType:       ctx.MediaType,
			ParserVersion:   ParserVersion,
			TotalBlocks:     len(blocks),
			TotalCodepoints: totalCodepoints,
			Complete:        complete,
			Truncated:       !complete,
			LikelyScanned:   likelyScanned,
		},
		Warnings: warnings,
	}, nil
}

// parsingContext is one bounded parse unit.
type parsingContext struct {
	Content    []byte
	MediaType  string
	ExtractText DocumentParser
}

// decodeUTF8 strictly validates UTF-8.
func decodeUTF8(data []byte) (string, error) {
	if !utf8.Valid(data) {
		return "", fmt.Errorf("invalid UTF-8 bytes at offset %d", firstInvalidUTF8(data))
	}
	return string(data), nil
}

func firstInvalidUTF8(data []byte) int {
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			return i
		}
		i += size
	}
	return len(data)
}

// boundChunks splits one source segment into <= target-code-point chunks.
// Long blocks split at whitespace within the window when present; a giant
// unbroken token hard-splits at the target.
func boundChunks(text string, target int) []string {
	if utf8.RuneCountInString(text) <= target {
		if text == "" {
			return nil
		}
		return []string{text}
	}
	runes := []rune(text)
	var chunks []string
	cursor := 0
	for cursor < len(runes) {
		windowEnd := cursor + target
		if windowEnd > len(runes) {
			windowEnd = len(runes)
		}
		if windowEnd < len(runes) {
			// Prefer a whitespace break within the tail of the window.
			window := runes[cursor:windowEnd]
			breakAt := -1
			for i := len(window) - 1; i > MinChunkCodepoints; i-- {
				if window[i] == ' ' {
					breakAt = i
					break
				}
			}
			if breakAt > MinChunkCodepoints {
				windowEnd = cursor + breakAt + 1
			}
		}
		chunks = append(chunks, string(runes[cursor:windowEnd]))
		cursor = windowEnd
	}
	return chunks
}

// emitBoundedBlock appends one block; returns false when the per-document
// cap is hit.
func emitBoundedBlock(text string, page *int64, blocks *[]TextBlock) bool {
	for _, chunk := range boundChunks(text, BlockTargetCodepoints) {
		if len(*blocks) >= MaxBlocksPerDoc {
			return false
		}
		*blocks = append(*blocks, TextBlock{
			BlockID:   MakeBlockID(len(*blocks)+1, chunk),
			Text:      chunk,
			Page:      page,
			BlockHash: hashText(chunk),
		})
	}
	return true
}

// blocksFromPlainText segments a document into bounded blocks. hardSplit
// (PDF text layer) takes one segment per \f page and never merges across
// pages; plain text/markdown takes one segment per blank-line paragraph.
func blocksFromPlainText(document string, hardSplit bool) ([]TextBlock, bool) {
	type segment struct {
		text string
		page *int64
	}
	var segments []segment
	if hardSplit {
		for index, pageText := range strings.Split(document, "\f") {
			stripped := strings.TrimSpace(pageText)
			if stripped == "" {
				continue
			}
			page := int64(index + 1)
			segments = append(segments, segment{text: stripped, page: &page})
		}
	} else {
		for _, paragraph := range strings.Split(document, "\n\n") {
			stripped := strings.TrimSpace(paragraph)
			if stripped == "" {
				continue
			}
			segments = append(segments, segment{text: stripped})
		}
	}

	blocks := []TextBlock{}
	complete := true
	for _, seg := range segments {
		if !emitBoundedBlock(seg.text, seg.page, &blocks) {
			complete = false
			break
		}
	}
	return blocks, complete
}

// MakeBlockID is the deterministic block id: ordinal + 8 hex of the text
// hash, so a re-parse of identical content yields identical ids (cache reuse
// across runs keeps working).
func MakeBlockID(index int, text string) string {
	return fmt.Sprintf("blk-%04d-%s", index, hashText(text)[:8])
}
