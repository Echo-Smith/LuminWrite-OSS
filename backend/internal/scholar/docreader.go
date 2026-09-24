package scholar

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"time"
)

// maxUploadDocumentBytes is the bounded byte protocol's client-side cap. It
// matches the research fetch ceiling (researchFetchSizeLimit /
// downloader.DefaultSizeLimit = 25 MiB): a document the downloader allowed
// through must fit the parse transport, and anything larger is rejected
// locally with a typed error instead of shipping ~33 MiB of base64 or
// tripping the sidecar's cap.
const maxUploadDocumentBytes = 25 * 1024 * 1024

// DocreaderParser extracts PDF text through the docreader TCP sidecar — the
// same parsing surface knowledge-base uploads use (docker/docreader).
//
// Transport (bounded inline-byte protocol): the client sends
// "PARSEBYTES <size> .pdf\n" followed by exactly <size> raw document bytes.
// The sidecar stages those bytes inside its own container (its private
// /tmp/docreader staging volume), parses, and answers with UTF-8 text until
// connection close. No document path ever crosses the wire, so the default
// compose deployment needs NO shared volume between backend and docreader —
// the previous path-based protocol sent a backend-local /tmp path the
// sidecar could not see and every parse degenerated into an empty
// extraction ("likely scanned").
//
// Known trade-off (accepted in the rewrite decision): docreader returns
// flat extracted text without page boundaries, so parsed PDF blocks carry
// no page numbers (page=null, like TXT) — evidence quotes bind to block
// ids and verified text, not page numbers.
type DocreaderParser struct {
	// Addr is the docreader TCP address, e.g. "docreader:50051".
	Addr string
	// Timeout bounds one extraction call (dial gets its own 10s bound).
	Timeout time.Duration
}

// NewDocreaderParser builds a parser for a docreader sidecar address.
func NewDocreaderParser(addr string) *DocreaderParser {
	return &DocreaderParser{Addr: addr, Timeout: 120 * time.Second}
}

// docreaderErrorCode maps sidecar-level failures to typed parse errors.
func docreaderErrorCode(err error) *ParseError {
	if pe, ok := err.(*ParseError); ok {
		return pe
	}
	return &ParseError{Code: "parse_extractor_unavailable",
		Message: "docreader extraction failed: " + err.Error(), Retryable: true}
}

// ExtractText implements DocumentParser.
func (p *DocreaderParser) ExtractText(req extractionContext) (string, *bool, error) {
	if p.Addr == "" {
		return "", nil, &ParseError{Code: "parse_extractor_unconfigured",
			Message: "docreader address is not configured"}
	}
	// Client-side bound before any bytes move: the sidecar enforces the same
	// ceiling (DOCREADER_MAX_INLINE_BYTES, 32 MiB by default) but a local
	// typed error beats a wasted upload and an ambiguous ERROR response.
	if len(req.Content) == 0 {
		return "", nil, &ParseError{Code: "parse_document_empty",
			Message: "document has no bytes to extract"}
	}
	if len(req.Content) > maxUploadDocumentBytes {
		return "", nil, &ParseError{Code: "parse_document_too_large",
			Message: fmt.Sprintf("document is %d bytes; the docreader transport allows at most %d bytes (25 MiB design cap)", len(req.Content), maxUploadDocumentBytes)}
	}

	timeout := p.Timeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	conn, err := net.DialTimeout("tcp", p.Addr, 10*time.Second)
	if err != nil {
		return "", nil, docreaderErrorCode(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))

	// Bounded inline-byte request: header line, then the raw document. The
	// sidecar reads exactly <size> bytes, stages them in its own container,
	// and never consults any caller-supplied path.
	if _, err := fmt.Fprintf(conn, "PARSEBYTES %d .pdf\n", len(req.Content)); err != nil {
		return "", nil, docreaderErrorCode(err)
	}
	if _, err := io.Copy(conn, bytes.NewReader(req.Content)); err != nil {
		return "", nil, docreaderErrorCode(fmt.Errorf("sending document: %w", err))
	}

	// The sidecar answers with the parsed text and closes the connection;
	// cap the read to stay inside the payload ceilings (25 MiB source text
	// ceiling; the sidecar's markdown output stays below it).
	const maxExtract = 32 << 20
	raw := make([]byte, 0, 64*1024)
	buf := make([]byte, 64*1024)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			raw = append(raw, buf[:n]...)
			if len(raw) > maxExtract {
				return "", nil, docreaderErrorCode(fmt.Errorf("extracted text exceeds %d bytes", maxExtract))
			}
		}
		if err != nil {
			if err.Error() == "EOF" || err == context.Canceled {
				break
			}
			// net.Conn Read returns io.EOF on close; treat anything else as
			// a failure only if we have no data at all.
			if len(raw) == 0 {
				return "", nil, docreaderErrorCode(err)
			}
			break
		}
	}

	text := string(raw)
	trimmed := trimErrorMarker(text)
	scanned := false
	if trimmed == "" {
		scanned = true
	}
	return trimmed, &scanned, nil
}

// trimErrorMarker normalises the sidecar's "ERROR: ..." responses into a
// typed failure; empty responses are returned as empty (the caller flags
// likely_scanned).
func trimErrorMarker(text string) string {
	if len(text) >= 7 && text[:7] == "ERROR: " {
		return ""
	}
	return text
}
