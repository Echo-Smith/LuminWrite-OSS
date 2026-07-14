package services

import (
	"context"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/luminbuddy/writing-agent-v2/internal/database"
)

// SensitiveCheckService provides sensitive word detection and filtering.
type SensitiveCheckService struct {
	adminRepo *database.AdminRepo
	mu        sync.RWMutex
	words     []compiledSensitiveWord
	lastLoad  time.Time
	ttl       time.Duration
}

type compiledSensitiveWord struct {
	word        string
	category    string
	severity    string
	action      string
	replacement string
	regex       *regexp.Regexp
}

// SensitiveResult holds the result of a sensitive word check.
type SensitiveResult struct {
	Passed   bool                  `json:"passed"`
	Hits     []SensitiveHit        `json:"hits,omitempty"`
	Cleaned  string                `json:"cleaned,omitempty"`
	Summary  string                `json:"summary"`
}

// SensitiveHit represents a single sensitive word match.
type SensitiveHit struct {
	Word        string `json:"word"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Action      string `json:"action"`
	Replacement string `json:"replacement,omitempty"`
	Count       int    `json:"count"`
}

// NewSensitiveCheckService creates a new SensitiveCheckService.
func NewSensitiveCheckService(adminRepo *database.AdminRepo) *SensitiveCheckService {
	return &SensitiveCheckService{
		adminRepo: adminRepo,
		ttl:       5 * time.Minute,
	}
}

// maybeRefresh reloads the word list from DB if the cache is stale.
func (s *SensitiveCheckService) maybeRefresh(ctx context.Context) {
	s.mu.RLock()
	stale := time.Since(s.lastLoad) > s.ttl
	s.mu.RUnlock()

	if !stale && len(s.words) > 0 {
		return
	}

	if s.adminRepo == nil {
		return
	}

	words, _, err := s.adminRepo.ListSensitiveWords(ctx, "")
	if err != nil {
		slog.Warn("failed to load sensitive words", "error", err)
		return
	}

	var compiled []compiledSensitiveWord
	for _, w := range words {
		if !w.IsActive {
			continue
		}
		replacement := ""
		if w.Replacement != nil {
			replacement = *w.Replacement
		}

		// Compile as a case-insensitive regex
		pattern := regexp.QuoteMeta(w.Word)
		re, err := regexp.Compile("(?i)" + pattern)
		if err != nil {
			slog.Warn("failed to compile sensitive word regex", "word", w.Word, "error", err)
			continue
		}

		compiled = append(compiled, compiledSensitiveWord{
			word:        w.Word,
			category:    w.Category,
			severity:    w.Severity,
			action:      w.Action,
			replacement: replacement,
			regex:       re,
		})
	}

	s.mu.Lock()
	s.words = compiled
	s.lastLoad = time.Now()
	s.mu.Unlock()

	slog.Info("sensitive words loaded", "count", len(compiled))
}

// Check analyzes the given text for sensitive words.
func (s *SensitiveCheckService) Check(ctx context.Context, text string) *SensitiveResult {
	s.maybeRefresh(ctx)

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.words) == 0 {
		return &SensitiveResult{
			Passed:  true,
			Summary: "no sensitive words configured",
		}
	}

	hitsMap := map[string]*SensitiveHit{}
	cleaned := text
	hasBlock := false

	for _, w := range s.words {
		matches := w.regex.FindAllStringIndex(text, -1)
		if len(matches) == 0 {
			continue
		}

		hit, exists := hitsMap[w.word]
		if !exists {
			hit = &SensitiveHit{
				Word:     w.word,
				Category: w.category,
				Severity: w.severity,
				Action:   w.action,
			}
			hitsMap[w.word] = hit
		}
		hit.Count += len(matches)

		// Apply action
		switch w.action {
		case "block":
			hasBlock = true
		case "replace":
			if w.replacement != "" {
				cleaned = w.regex.ReplaceAllString(cleaned, w.replacement)
				hit.Replacement = w.replacement
			} else {
				// Default replacement: mask with asterisks
				masked := strings.Repeat("*", len([]rune(w.word)))
				cleaned = w.regex.ReplaceAllString(cleaned, masked)
				hit.Replacement = masked
			}
		case "warn":
			// Just warn, don't modify
		}
	}

	hits := make([]SensitiveHit, 0, len(hitsMap))
	for _, h := range hitsMap {
		hits = append(hits, *h)
	}

	passed := len(hits) == 0 && !hasBlock

	summary := "passed"
	if hasBlock {
		summary = "blocked: sensitive content detected"
	} else if len(hits) > 0 {
		summary = "warning: " + strings.Join(func() []string {
			var words []string
			for _, h := range hits {
				if h.Action == "warn" {
					words = append(words, h.Word)
				}
			}
			return words
		}(), ", ")
	}

	return &SensitiveResult{
		Passed:  passed,
		Hits:    hits,
		Cleaned: cleaned,
		Summary: summary,
	}
}

// HasBlock checks if the text contains any blocking-level sensitive words.
func (s *SensitiveCheckService) HasBlock(ctx context.Context, text string) bool {
	result := s.Check(ctx, text)
	return !result.Passed
}

// Clean replaces all sensitive words in the text according to their action.
func (s *SensitiveCheckService) Clean(ctx context.Context, text string) string {
	result := s.Check(ctx, text)
	return result.Cleaned
}
