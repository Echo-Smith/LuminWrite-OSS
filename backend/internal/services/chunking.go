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

// ChunkConfig controls how text is split into chunks.
type ChunkConfig struct {
	Size          int      // Target chunk size in runes
	Overlap       int      // Overlap between adjacent chunks in runes
	SplitMarkers []string // Priority-ordered split markers (try \n\n first, then \n, then 。)
}

// DefaultChunkConfig returns the default chunking configuration.
func DefaultChunkConfig() ChunkConfig {
	return ChunkConfig{
		Size:          512,
		Overlap:       50,
		SplitMarkers:  []string{"\n\n", "\n", "。", "！", "？", "；"},
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
	if len(config.SplitMarkers) == 0 {
		config.SplitMarkers = []string{"\n\n", "\n", "。"}
	}

	// Normalize whitespace
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	runes := []rune(normalized)
	totalLen := len(runes)

	if totalLen <= config.Size {
		return []*Chunk{{
			Index:    0,
			Title:     extractTitle(normalized),
			Content:  normalized,
			StartPos: 0,
			EndPos:   totalLen,
		}}
	}

	var chunks []*Chunk
	chunkIdx := 0
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
		title := extractTitle(content)

		chunks = append(chunks, &Chunk{
			Index:    chunkIdx,
			Title:    title,
			Content:  content,
			StartPos: pos,
			EndPos:   end,
		})

		chunkIdx++
		// Move forward, accounting for overlap
		nextStart := end - config.Overlap
		if nextStart <= pos {
			nextStart = pos + 1 // Ensure progress
		}
		pos = nextStart
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
