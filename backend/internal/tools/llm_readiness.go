package tools

import "strings"

// IsConfigured reports whether the client has a non-placeholder credential.
// It intentionally does not imply that the provider has been probed or is
// reachable; deployment readiness tracks that separately.
//
// Edition note: readiness probing (internal/server/readiness.go) is OSS-PLUS
// surface, so this method lives in an edition-owned file and the shared
// deepseek.go stays byte-identical with Commercial (docs/29 §7).
func (c *LLMClient) IsConfigured() bool {
	if c == nil {
		return false
	}
	apiKey := strings.ToLower(strings.TrimSpace(c.apiKey))
	if apiKey == "" {
		return false
	}
	switch apiKey {
	case "your-api-key", "your-deepseek-api-key", "placeholder":
		return false
	}
	return !strings.HasPrefix(apiKey, "your-")
}
