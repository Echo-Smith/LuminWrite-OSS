package scholar

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeDocreaderSidecar is an in-process stand-in for the docreader TCP
// sidecar. It speaks the bounded inline-byte protocol the production sidecar
// implements (docker/docreader/docreader-slim.py) and records what arrived:
// header line, exact byte count, and body digest — so the client's wire
// format is regression-pinned (WP1/WP2 PDF transport fix: bytes, not paths).
type fakeDocreaderSidecar struct {
	t           *testing.T
	listener    net.Listener
	response    string // bytes written back before close
	mu          sync.Mutex
	connections int
	header      string
	bodyDigest  string // sha256 hex of the received document bytes
	bodyLen     int
}

func newFakeDocreaderSidecar(t *testing.T, response string) *fakeDocreaderSidecar {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeDocreaderSidecar{t: t, listener: listener, response: response}
	go fake.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return fake
}

func (fake *fakeDocreaderSidecar) addr() string { return fake.listener.Addr().String() }

func (fake *fakeDocreaderSidecar) serve() {
	for {
		conn, err := fake.listener.Accept()
		if err != nil {
			return
		}
		go fake.handle(conn)
	}
}

func (fake *fakeDocreaderSidecar) handle(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	header, err := reader.ReadString('\n')
	if err != nil {
		return
	}
	header = strings.TrimRight(header, "\n")
	digest := sha256.New()
	var bodyLen int
	fields := strings.Fields(header)
	if len(fields) == 3 && fields[0] == "PARSEBYTES" {
		// Bounded inline-byte request: read exactly <size> bytes after the
		// header line, mirroring the production sidecar's read_exact.
		n, convErr := strconv.Atoi(fields[1])
		if convErr != nil || !strings.HasPrefix(fields[2], ".") {
			_, _ = conn.Write([]byte("ERROR: bad PARSEBYTES header\n"))
			return
		}
		copied, copyErr := io.Copy(digest, io.LimitReader(reader, int64(n)))
		if copyErr != nil || copied != int64(n) {
			_, _ = conn.Write([]byte("ERROR: short inline body\n"))
			return
		}
		bodyLen = n
	}
	fake.mu.Lock()
	fake.connections++
	fake.header = header
	fake.bodyDigest = hex.EncodeToString(digest.Sum(nil))
	fake.bodyLen = bodyLen
	fake.mu.Unlock()
	_, _ = conn.Write([]byte(fake.response))
}

func (fake *fakeDocreaderSidecar) state() (int, string, string, int) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return fake.connections, fake.header, fake.bodyDigest, fake.bodyLen
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func parseErrorCode(t *testing.T, err error) string {
	t.Helper()
	var parseErr *ParseError
	if !errors.As(err, &parseErr) {
		t.Fatalf("err=%v, want *ParseError", err)
	}
	return parseErr.Code
}

// TestDocreaderParserSendsInlineBytes pins the byte transport: the parser
// must stream the document bytes over the socket (bounded inline protocol)
// and never stage them locally or send a path.
func TestDocreaderParserSendsInlineBytes(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "提取的正文内容\n第二行")
	parser := NewDocreaderParser(fake.addr())
	document := []byte("%PDF-1.4 fake pdf bytes")

	text, scanned, err := parser.ExtractText(extractionContext{Content: document, MediaType: MediaTypePDF})
	if err != nil {
		t.Fatal(err)
	}
	if text != fake.response {
		t.Fatalf("text=%q, want %q", text, fake.response)
	}
	if scanned == nil || *scanned {
		t.Fatalf("scanned=%v, want false", scanned)
	}
	connections, header, bodyDigest, bodyLen := fake.state()
	if connections != 1 {
		t.Fatalf("connections=%d", connections)
	}
	if header != fmt.Sprintf("PARSEBYTES %d .pdf", len(document)) {
		t.Fatalf("header=%q", header)
	}
	if bodyLen != len(document) || bodyDigest != digestOf(document) {
		t.Fatalf("document transfer mismatch: len=%d digest=%s", bodyLen, bodyDigest)
	}
}

// TestDocreaderParserStreamsMultiChunkDocument proves the full body crosses
// the wire even when it spans many TCP writes (2 MiB pseudo-random pattern).
func TestDocreaderParserStreamsMultiChunkDocument(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "ok")
	parser := NewDocreaderParser(fake.addr())
	document := make([]byte, 2<<20)
	for i := range document {
		document[i] = byte(i*31 + i>>8)
	}
	if _, _, err := parser.ExtractText(extractionContext{Content: document, MediaType: MediaTypePDF}); err != nil {
		t.Fatal(err)
	}
	_, _, bodyDigest, bodyLen := fake.state()
	if bodyLen != len(document) || bodyDigest != digestOf(document) {
		t.Fatalf("multi-chunk document transfer mismatch: len=%d", bodyLen)
	}
}

// TestDocreaderParserRejectsOversizedDocumentLocally: above the 25 MiB
// design cap the parser fails with a typed error BEFORE dialing — no
// connection attempt, no upload.
func TestDocreaderParserRejectsOversizedDocumentLocally(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "unreachable")
	parser := NewDocreaderParser(fake.addr())
	oversized := make([]byte, maxUploadDocumentBytes+1)

	_, _, err := parser.ExtractText(extractionContext{Content: oversized, MediaType: MediaTypePDF})
	if code := parseErrorCode(t, err); code != "parse_document_too_large" {
		t.Fatalf("code=%s, want parse_document_too_large", code)
	}
	if connections, _, _, _ := fake.state(); connections != 0 {
		t.Fatalf("oversized document reached the sidecar: connections=%d", connections)
	}
}

// TestDocreaderParserRejectsEmptyDocumentLocally: no bytes, no call.
func TestDocreaderParserRejectsEmptyDocumentLocally(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "unreachable")
	parser := NewDocreaderParser(fake.addr())

	_, _, err := parser.ExtractText(extractionContext{Content: nil, MediaType: MediaTypePDF})
	if code := parseErrorCode(t, err); code != "parse_document_empty" {
		t.Fatalf("code=%s, want parse_document_empty", code)
	}
	if connections, _, _, _ := fake.state(); connections != 0 {
		t.Fatalf("empty document reached the sidecar: connections=%d", connections)
	}
}

// TestDocreaderParserEmptyResponseFlagsScanned preserves the extraction
// semantic: an empty extraction is reported as likely_scanned, never as a
// silent success.
func TestDocreaderParserEmptyResponseFlagsScanned(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "")
	parser := NewDocreaderParser(fake.addr())

	text, scanned, err := parser.ExtractText(extractionContext{Content: []byte("%PDF-1.4"), MediaType: MediaTypePDF})
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("text=%q, want empty", text)
	}
	if scanned == nil || !*scanned {
		t.Fatalf("scanned=%v, want true", scanned)
	}
}

// TestDocreaderParserErrorResponseFlagsScanned: sidecar-level "ERROR: ..."
// answers degrade to empty + likely_scanned (the caller's warning channel),
// matching the pre-existing extraction contract.
func TestDocreaderParserErrorResponseFlagsScanned(t *testing.T) {
	fake := newFakeDocreaderSidecar(t, "ERROR: markitdown returned empty content")
	parser := NewDocreaderParser(fake.addr())

	text, scanned, err := parser.ExtractText(extractionContext{Content: []byte("%PDF-1.4"), MediaType: MediaTypePDF})
	if err != nil {
		t.Fatal(err)
	}
	if text != "" {
		t.Fatalf("text=%q, want empty", text)
	}
	if scanned == nil || !*scanned {
		t.Fatalf("scanned=%v, want true", scanned)
	}
}

// TestDocreaderParserUnconfigured: no address is a typed configuration
// failure, not a dial error.
func TestDocreaderParserUnconfigured(t *testing.T) {
	parser := &DocreaderParser{}
	_, _, err := parser.ExtractText(extractionContext{Content: []byte("x"), MediaType: MediaTypePDF})
	if code := parseErrorCode(t, err); code != "parse_extractor_unconfigured" {
		t.Fatalf("code=%s, want parse_extractor_unconfigured", code)
	}
}
