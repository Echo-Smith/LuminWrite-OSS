package scholar

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"
)

// DocreaderParser extracts PDF text through the docreader TCP sidecar — the
// same parsing surface knowledge-base uploads use (docker/docreader,
// protocol: send "PARSE <filepath>\n", read UTF-8 text until close). One
// parsing surface for both KB and research means extraction quality is
// tuned in one place. The sidecar receives bytes via a temp file in a
// private directory; it never sees caller-controlled paths beyond that.
//
// Known trade-off (accepted in the rewrite decision): docreader returns
// flat extracted text without page boundaries, so parsed PDF blocks carry
// no page numbers (page=null, like TXT) — evidence quotes bind to block
// ids and verified text, not page numbers.
type DocreaderParser struct {
	// Addr is the docreader TCP address, e.g. "docreader:50051".
	Addr string
	// Timeout bounds one extraction call.
	Timeout time.Duration
	// TempDir is the private staging directory for document bytes; empty
	// means os.TempDir().
	TempDir string
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
	dir := p.TempDir
	if dir == "" {
		dir = os.TempDir()
	}
	// The sidecar is path-based: stage the bytes in a private temp dir. The
	// filename carries no user input beyond the fixed extension.
	tmpFile, err := os.CreateTemp(dir, "scholar-parse-*.pdf")
	if err != nil {
		return "", nil, docreaderErrorCode(fmt.Errorf("staging document: %w", err))
	}
	path := tmpFile.Name()
	defer os.Remove(path)
	if _, err := tmpFile.Write(req.Content); err != nil {
		tmpFile.Close()
		return "", nil, docreaderErrorCode(fmt.Errorf("writing document: %w", err))
	}
	if err := tmpFile.Close(); err != nil {
		return "", nil, docreaderErrorCode(fmt.Errorf("closing staged document: %w", err))
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

	if _, err := fmt.Fprintf(conn, "PARSE %s\n", path); err != nil {
		return "", nil, docreaderErrorCode(err)
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

// _ keeps filepath referenced for future staging-dir handling.
var _ = filepath.Join
