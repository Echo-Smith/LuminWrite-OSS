package services

import (
	"strings"
	"unicode/utf8"
)

// ─── Text Chunking Strategy ────────────────────────────
// Chunking splits a document into smaller pieces for fine-grained retrieval.
// Each chunk gets its own embedding, enabling precise semantic search.
//
// The chunker supports:
//   - Configurable chunk size (in runes, not bytes — Chinese-safe)
//   - Configurable overlap between adjacent chunks
//   - Split markers (e.g., "\n\n", "\n", "。") for natural boundaries
//   - Title extraction for each chunk (first line or sentence)
//   - Minimum chunk size enforcement (filters out tiny fragments)
//   - Duplicate/near-duplicate chunk detection

// ChunkConfig controls how text is split into chunks.
type ChunkConfig struct {
	Size          int      // Target chunk size in runes
	Overlap       int      // Overlap between adjacent chunks in runes
	SplitMarkers  []string // Priority-ordered split markers (try \n\n first, then \n, then 。)
	MinChunkSize  int      // Minimum chunk size in runes; shorter chunks are merged or discarded
}

// DefaultChunkConfig returns the default chunking configuration.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		Size:         512,
		Overlap:      50,
		SplitMarkers: []string{"\n\n", "\n", "。", "！", "？", "；"},
		MinChunkSize: 80,
	}
}

// Chunk represents a single piece of a document.
type Chunk struct {
	Index    int    `json:"index"`
	Title    string `json:"title,omitempty"`
	Content  string `json:"content"`
	StartPos int    `json:"start_pos"` // Character offset in original document
	EndPos   int    `json:"end_pos"`   // Character offset (exclusive)
}

// ChunkText splits text into chunks using the given configuration.
// It tries to split at natural boundaries (paragraphs, sentences) first,
// falling back to hard splits when chunks exceed the target size.
//
// Improvements over the original:
//   - Skips empty or near-empty chunks (just whitespace)
//   - Enforces minimum chunk size — tiny tail fragments are merged into the previous chunk
//   - Fixes the overlap bug: when remaining text < overlap, produces one final chunk instead of degenerate fragments
//   - Deduplicates near-identical chunks (same content after trimming)
func ChunkText(text string, config ChunkConfig) []*Chunk {
	if config.Size <= 0 {
		config.Size = 512
	}
	if config.Overlap < 0 {
		config.Overlap = 0
	}
	if config.Overlap >= config.Size {
		config.Overlap = config.Size / 4
	}
	if config.MinChunkSize <= 0 {
		config.MinChunkSize = 80
	}
	if len(config.SplitMarkers) == 0 {
		config.SplitMarkers = []string{"\n\n", "\n", "。"}
	}

	// Normalize whitespace
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	runes := []rune(normalized)
	totalLen := len(runes)

	if totalLen <= config.Size {
		cleaned := strings.TrimSpace(normalized)
		if utf8.RuneCountInString(cleaned) == 0 {
			return nil
		}
		return []*Chunk{{
			Index:    0,
			Title:    extractTitle(cleaned),
			Content:  cleaned,
			StartPos: 0,
			EndPos:   totalLen,
		}}
	}

	var rawChunks []*Chunk
	pos := 0

	for pos < totalLen {
		end := pos + config.Size
		if end > totalLen {
			end = totalLen
		}

		// Try to find a natural split point near the target end
		if end < totalLen {
			splitPos := findSplitPoint(runes, pos, end, config.SplitMarkers)
			if splitPos > pos {
				end = splitPos
			}
		}

		// Extract chunk content
		content := string(runes[pos:end])

		// Skip chunks that are entirely whitespace
		if strings.TrimSpace(content) != "" {
			rawChunks = append(rawChunks, &Chunk{
				Index:    len(rawChunks),
				Title:    extractTitle(content),
				Content:  content,
				StartPos: pos,
				EndPos:   end,
			})
		}

		// ── Fix overlap bug: if remaining text is shorter than overlap,
		// produce one final chunk and stop. This prevents degenerate
		// 1-char-shorter fragments from accumulating. ──
		remaining := totalLen - end
		if remaining <= config.Overlap {
			// Grab any remaining text as the final chunk
			if remaining > 0 && strings.TrimSpace(string(runes[end:])) != "" {
				finalContent := string(runes[end:])
				rawChunks = append(rawChunks, &Chunk{
					Index:    len(rawChunks),
					Title:    extractTitle(finalContent),
					Content:  finalContent,
					StartPos: end,
					EndPos:   totalLen,
				})
			}
			break
		}

		// Move forward, accounting for overlap
		nextStart := end - config.Overlap
		if nextStart <= pos {
			nextStart = pos + 1 // Ensure progress
		}
		pos = nextStart
	}

	// ── Post-process: merge tiny tail chunks into the previous chunk ──
	if len(rawChunks) <= 1 {
		return rawChunks
	}

	minRunes := config.MinChunkSize
	var chunks []*Chunk
	for _, c := range rawChunks {
		contentRunes := utf8.RuneCountInString(strings.TrimSpace(c.Content))
		if contentRunes < minRunes && len(chunks) > 0 {
			// Merge into previous chunk
			prev := chunks[len(chunks)-1]
			prev.Content = prev.Content + "\n" + c.Content
			prev.EndPos = c.EndPos
			if prev.Title == "" {
				prev.Title = c.Title
			}
		} else {
			c.Index = len(chunks)
			chunks = append(chunks, c)
		}
	}

	// ── Deduplicate: remove chunks with identical trimmed content ──
	if len(chunks) > 1 {
		seen := make(map[string]bool, len(chunks))
		deduped := chunks[:0]
		for _, c := range chunks {
			key := strings.TrimSpace(c.Content)
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			c.Index = len(deduped)
			deduped = append(deduped, c)
		}
		chunks = deduped
	}

	return chunks
}

// findSplitPoint searches for the best split marker near the target end position.
// It looks backwards from `end` to find the last occurrence of any split marker
// within a reasonable window (50% of chunk size).
func findSplitPoint(runes []rune, start, end int, markers []string) int {
	searchStart := start + (end-start)/2 // Start searching from halfway point

	for _, marker := range markers {
		markerRunes := []rune(marker)
		markerLen := len(markerRunes)

		// Search backwards from end
		for i := end - markerLen; i >= searchStart; i-- {
			match := true
			for j := 0; j < markerLen; j++ {
				if runes[i+j] != markerRunes[j] {
					match = false
					break
				}
			}
			if match {
				return i + markerLen // Split after the marker
			}
		}
	}

	// If no marker found, try to split at a space or punctuation
	for i := end - 1; i >= searchStart; i-- {
		if isSplitChar(runes[i]) {
			return i + 1
		}
	}

	return end // No good split point found, use target end
}

// isSplitChar returns true if the character is a natural split boundary.
func isSplitChar(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '。', '！', '？', '；', '，', '.', '!', '?', ';', ',':
		return true
	}
	return false
}

// extractTitle extracts a title from the beginning of a chunk.
// Uses the first line or first sentence (up to ~60 chars).
func extractTitle(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	// Try first line
	if idx := strings.Index(text, "\n"); idx > 0 && idx <= 80 {
		line := strings.TrimSpace(text[:idx])
		if len([]rune(line)) >= 4 && len([]rune(line)) <= 60 {
			return line
		}
	}

	// Try first sentence (ending with 。！？.)
	runes := []rune(text)
	for i, r := range runes {
		if r == '。' || r == '！' || r == '？' || r == '.' || r == '!' || r == '?' {
			if i > 0 && i <= 60 {
				return strings.TrimSpace(string(runes[:i+1]))
			}
		}
	}

	// Fallback: first 60 chars
	if utf8.RuneCountInString(text) <= 60 {
		return text
	}
	runes = []rune(text)
	return string(runes[:60]) + "..."
}
