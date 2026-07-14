package engine

import "context"

// SensitiveChecker checks text for sensitive content.
// Implementations include sensitive word detection, content safety filtering, etc.
type SensitiveChecker interface {
	// Check analyzes the given text for sensitive content.
	Check(ctx context.Context, text string) *SensitiveCheckResult
}

// SensitiveCheckResult holds the result of a sensitive content check.
type SensitiveCheckResult struct {
	Passed  bool            `json:"passed"`          // true if no blocking-level hits
	Hits    []SensitiveHit  `json:"hits,omitempty"`  // all sensitive word matches
	Summary string          `json:"summary"`         // human-readable summary
}

// SensitiveHit represents a single sensitive word match.
type SensitiveHit struct {
	Word        string `json:"word"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`     // low | medium | high
	Action      string `json:"action"`       // warn | block | replace
	Count       int    `json:"count"`
}
