package services

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// docreaderMaxInlineBytes 与 scholar 侧对齐的后端预检上限（docreader 容器侧
// 上限为 32 MiB，见 DOCREADER_MAX_INLINE_BYTES）。
const docreaderMaxInlineBytes = 25 * 1024 * 1024

// errDocreaderRejected 标记 docreader 拒绝/无法解析该文档（空、超限、ERROR）。
var errDocreaderRejected = errors.New("docreader rejected document")

// ─── File Parser Service ───────────────────────────────
// FileParser handles file upload and parsing for the knowledge base.
// It uses the docreader TCP sidecar (slim image, ~150MB) for
// high-quality parsing of PDF, Word, Excel, and other document formats.
//
// For simple text formats (txt, md), it reads directly without docreader.
//
// Supported formats:
//   - Direct read: .txt, .md, .csv, .json
//   - Via docreader: .pdf, .docx, .doc, .pptx, .ppt, .xlsx, .xls
//   - Via docreader: .jpg, .jpeg, .png, .webp, .gif, .bmp, .heic, .heif (OCR)

// FileParser handles file parsing and knowledge import.
type FileParser struct {
	kbManager     *KbManager
	chunker       ChunkConfig
	docreaderAddr string // gRPC address (e.g., "docreader:50051")
}

// NewFileParser creates a new file parser.
func NewFileParser(kbManager *KbManager, chunkConfig ChunkConfig, docreaderAddr string) *FileParser {
	return &FileParser{
		kbManager:     kbManager,
		chunker:       chunkConfig,
		docreaderAddr: docreaderAddr,
	}
}

// ParseAndImport parses a file and imports its content into the knowledge base.
// It returns the document ID of the newly created knowledge entry.
func (f *FileParser) ParseAndImport(ctx context.Context, userID, filename string, fileContent io.Reader, title string) (string, error) {
	if f.kbManager == nil || !f.kbManager.IsConfigured() {
		return "", fmt.Errorf("knowledge base not configured")
	}

	ext := strings.ToLower(filepath.Ext(filename))

	// Determine parsing strategy based on file extension
	var content string
	var sourceType string = "file"
	var parseErr error

	if isDirectReadFormat(ext) {
		// Read directly for simple text formats
		content, parseErr = f.readDirect(fileContent)
	} else if f.docreaderAddr != "" {
		// Use docreader gRPC sidecar for complex formats
		content, parseErr = f.parseWithDocreader(ctx, filename, fileContent)
	} else {
		// Fallback: try direct read
		content, parseErr = f.readDirect(fileContent)
		if parseErr != nil {
			return "", fmt.Errorf("docreader not configured and direct read failed for %s: %w", ext, parseErr)
		}
	}

	if parseErr != nil {
		return "", fmt.Errorf("failed to parse file %s: %w", filename, parseErr)
	}

	if len([]rune(content)) < 10 {
		return "", fmt.Errorf("parsed content too short for %s (len=%d)", filename, len([]rune(content)))
	}

	// Use provided title or filename
	if title == "" {
		title = filename
	}

	// Add document to knowledge base
	metadata := map[string]interface{}{
		"source":      "file",
		"file_name":   filename,
		"file_format": ext,
		"imported_at": time.Now().Format(time.RFC3339),
	}

	doc, err := f.kbManager.AddDocument(ctx, userID, title, content, sourceType, metadata)
	if err != nil {
		return "", fmt.Errorf("failed to add document: %w", err)
	}

	// Chunk the content and store chunks
	chunks := ChunkText(content, f.chunker)
	for _, chunk := range chunks {
		_, err := f.kbManager.AddChunk(ctx, doc.ID, userID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
			"start_pos":  chunk.StartPos,
			"end_pos":    chunk.EndPos,
			"file_name":  filename,
		})
		if err != nil {
			slog.Warn("failed to add chunk", "index", chunk.Index, "error", err)
		}
	}

	// Update chunk count
	if err := f.kbManager.UpdateChunkCount(ctx, doc.ID, len(chunks)); err != nil {
		slog.Warn("failed to update chunk count", "error", err)
	}

	slog.Info("file imported", "filename", filename, "title", title, "chunks", len(chunks), "doc_id", doc.ID)
	return doc.ID, nil
}

// readDirect reads simple text formats (txt, md, csv, json) directly.
func (f *FileParser) readDirect(fileContent io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(fileContent, 10*1024*1024)) // 10MB limit
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// parseWithDocreader sends the file to the docreader TCP service for parsing.
// The slim docreader service supports PDF, Word, PPT, Excel, images, etc.
// 传输采用有界字节协议（PARSEBYTES）：文件字节内联发送，不依赖跨容器可见的
// 临时路径——compose 部署下 backend 与 docreader 是不同容器，路径协议不可用。
func (f *FileParser) parseWithDocreader(ctx context.Context, filename string, fileContent io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(fileContent, docreaderMaxInlineBytes+1))
	if err != nil {
		return "", fmt.Errorf("failed to read upload: %w", err)
	}
	if len(data) == 0 {
		return "", fmt.Errorf("%w: empty upload for %s", errDocreaderRejected, filename)
	}
	if len(data) > docreaderMaxInlineBytes {
		return "", fmt.Errorf("%w: %s exceeds %d bytes", errDocreaderRejected, filename, docreaderMaxInlineBytes)
	}

	content, err := f.callDocreaderTCP(ctx, filepath.Ext(filename), data)
	if err != nil {
		return "", fmt.Errorf("docreader parsing failed: %w", err)
	}

	return content, nil
}

// callDocreaderTCP sends document bytes to the docreader TCP service and returns
// parsed text. Protocol: "PARSEBYTES <size> <ext>\n" followed by exactly <size>
// raw bytes; response is parsed text until connection close, or "ERROR ..." on
// rejection (empty/ERROR both fail the import — ERROR text must not be stored).
func (f *FileParser) callDocreaderTCP(ctx context.Context, ext string, content []byte) (string, error) {
	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", f.docreaderAddr)
	if err != nil {
		return "", fmt.Errorf("failed to connect to docreader: %w", err)
	}
	defer conn.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}

	header := fmt.Sprintf("PARSEBYTES %d %s\n", len(content), ext)
	if _, err := io.WriteString(conn, header); err != nil {
		return "", fmt.Errorf("failed to send request to docreader: %w", err)
	}
	if _, err := conn.Write(content); err != nil {
		return "", fmt.Errorf("failed to send document bytes to docreader: %w", err)
	}

	respBytes, err := io.ReadAll(io.LimitReader(conn, 50*1024*1024)) // 50MB limit
	if err != nil {
		return "", fmt.Errorf("failed to read docreader response: %w", err)
	}

	response := strings.TrimRight(string(respBytes), "\n")
	if response == "" {
		return "", fmt.Errorf("%w: docreader returned empty content", errDocreaderRejected)
	}
	if strings.HasPrefix(response, "ERROR") {
		return "", fmt.Errorf("%w: %s", errDocreaderRejected, response)
	}

	return response, nil
}

// isDirectReadFormat returns true for formats that can be read directly as text.
func isDirectReadFormat(ext string) bool {
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".json", ".html", ".htm", ".xml", ".yaml", ".yml", ".log":
		return true
	}
	return false
}

// isImageFormat returns true for image formats that require OCR.
func isImageFormat(ext string) bool {
	switch ext {
	case ".jpg", ".jpeg", ".png", ".webp", ".gif", ".bmp", ".heic", ".heif":
		return true
	}
	return false
}

// isDocumentFormat returns true for document formats that require docreader.
func isDocumentFormat(ext string) bool {
	switch ext {
	case ".pdf", ".docx", ".doc", ".pptx", ".ppt", ".xlsx", ".xls":
		return true
	}
	return false
}

// Ensure tools import is used
var _ = tools.FormatVectorForPG
