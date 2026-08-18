package services

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

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
// Protocol: send "PARSE <filepath>\n" over TCP, receive parsed text.
func (f *FileParser) parseWithDocreader(ctx context.Context, filename string, fileContent io.Reader) (string, error) {
	// Save to temporary file (docreader expects file paths)
	tmpFile, err := os.CreateTemp("", "docreader-*"+filepath.Ext(filename))
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := io.Copy(tmpFile, fileContent); err != nil {
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}

	// Call docreader via gRPC
	// The docreader gRPC API is based on WeKnora's docreader service.
	// We use a simple gRPC client to send the file path and get parsed text.
	content, err := f.callDocreaderTCP(ctx, tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("docreader parsing failed: %w", err)
	}

	return content, nil
}

// callDocreaderTCP sends a file to the docreader TCP service and returns parsed text.
// Protocol: send "PARSE <filepath>\n" over TCP, receive parsed text until connection close.
func (f *FileParser) callDocreaderTCP(ctx context.Context, filePath string) (string, error) {
	// Connect to docreader via TCP
	// The slim docreader service accepts a file path and returns parsed text.

	conn, err := net.DialTimeout("tcp", f.docreaderAddr, 10*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to connect to docreader: %w", err)
	}
	defer conn.Close()

	// Simple protocol: send file path, receive parsed text
	// Format: "PARSE <filepath>\n"
	// Response: text content until connection close
	cmd := fmt.Sprintf("PARSE %s\n", filePath)
	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", fmt.Errorf("failed to send request to docreader: %w", err)
	}

	// Read response
	respBytes, err := io.ReadAll(io.LimitReader(conn, 50*1024*1024)) // 50MB limit
	if err != nil {
		return "", fmt.Errorf("failed to read docreader response: %w", err)
	}

	content := string(respBytes)
	if len(content) == 0 {
		return "", fmt.Errorf("docreader returned empty content")
	}

	return content, nil
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
