package tools

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// JiaozhenClient wraps the Tencent News "较真" fact-checking CLI tool.
// It is optional — if not configured or if the CLI fails, it degrades gracefully.
type JiaozhenClient struct {
	enabled      bool
	cliPath      string
	commandArgs  []string
	apiKey       string
	timeout      time.Duration
	maxClaims    int
}

// NewJiaozhenClient creates a new JiaozhenClient.
func NewJiaozhenClient(enabled bool, cliPath string, commandArgs []string, apiKey string, timeout time.Duration, maxClaims int) *JiaozhenClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	if maxClaims <= 0 || maxClaims > 5 {
		maxClaims = 2
	}
	if len(commandArgs) == 0 {
		commandArgs = []string{"jiaozhen"}
	}
	return &JiaozhenClient{
		enabled:     enabled,
		cliPath:     cliPath,
		commandArgs: commandArgs,
		apiKey:      apiKey,
		timeout:     timeout,
		maxClaims:   maxClaims,
	}
}

// IsConfigured returns true if the client is enabled and the CLI path is set.
func (c *JiaozhenClient) IsConfigured() bool {
	return c != nil && c.enabled && c.cliPath != ""
}

// JiaozhenResult holds the result of a fact check.
type JiaozhenResult struct {
	Claim   string `json:"claim"`
	Status  string `json:"status"`   // ok | skipped | error
	Source  string `json:"source,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// candidatePatterns match claims that are suitable for fact-checking.
var jiaozhenCandidatePatterns = []*regexp.Regexp{
	regexp.MustCompile(`真假|真伪|真的假的|是真的吗|是否属实|是否真实|是否准确`),
	regexp.MustCompile(`谣言|辟谣|网传|传言|传闻|有人说|据说`),
	regexp.MustCompile(`能否|能不能|可不可以|是否可以|是否能|会不会`),
	regexp.MustCompile(`有毒|致癌|禁用|违法|违规|危险|不能吃|不能用`),
}

// IsJiaozhenCandidate checks if a claim is worth fact-checking.
func IsJiaozhenCandidate(claim string) bool {
	text := compactClaim(claim)
	if len([]rune(text)) < 6 || len([]rune(text)) > 240 {
		return false
	}
	for _, pattern := range jiaozhenCandidatePatterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// compactClaim normalizes a claim string for processing.
func compactClaim(s string) string {
	// Collapse whitespace and trim
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimSpace(s)
	// Limit to 240 runes
	if len([]rune(s)) > 240 {
		s = string([]rune(s)[:240])
	}
	return s
}

// CheckClaim runs the jiaozhen CLI tool to fact-check a single claim.
func (c *JiaozhenClient) CheckClaim(ctx context.Context, claim string) *JiaozhenResult {
	normalizedClaim := compactClaim(claim)

	if !c.enabled {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "disabled"}
	}
	if !IsJiaozhenCandidate(normalizedClaim) {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "not_candidate"}
	}

	// Run CLI tool
	args := append([]string{}, c.commandArgs...)
	args = append(args, normalizedClaim)

	ctxWithTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctxWithTimeout, c.cliPath, args...)

	// Set environment
	if c.apiKey != "" {
		cmd.Env = append(cmd.Env,
			fmt.Sprintf("TENCENT_NEWS_API_KEY=%s", c.apiKey),
			fmt.Sprintf("TENCENT_NEWS_APP_KEY=%s", c.apiKey),
			fmt.Sprintf("TENCENT_NEWS_APPKEY=%s", c.apiKey),
		)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Debug("jiaozhen CLI failed", "claim", normalizedClaim, "error", err)
		return &JiaozhenResult{
			Claim:  claim,
			Status: "error",
			Error:  err.Error(),
		}
	}

	outputStr := strings.TrimSpace(string(output))
	if outputStr == "" {
		return &JiaozhenResult{Claim: claim, Status: "error", Error: "empty_output"}
	}

	// Limit output size
	if len([]rune(outputStr)) > 6000 {
		outputStr = string([]rune(outputStr)[:6000])
	}

	return &JiaozhenResult{
		Claim:   claim,
		Status:  "ok",
		Source:  "较真查证增强",
		Content: outputStr,
	}
}

// CheckClaims runs fact-checking on multiple claims concurrently.
// Returns results for claims that were successfully checked.
func (c *JiaozhenClient) CheckClaims(ctx context.Context, claims []string) []*JiaozhenResult {
	if !c.enabled {
		return []*JiaozhenResult{}
	}

	// Filter to candidates only, limit to maxClaims
	var candidates []string
	for _, claim := range claims {
		if IsJiaozhenCandidate(claim) {
			candidates = append(candidates, claim)
			if len(candidates) >= c.maxClaims {
				break
			}
		}
	}

	if len(candidates) == 0 {
		return []*JiaozhenResult{}
	}

	results := make([]*JiaozhenResult, 0, len(candidates))
	for _, claim := range candidates {
		result := c.CheckClaim(ctx, claim)
		if result.Status == "ok" {
			results = append(results, result)
		}
	}

	slog.Debug("jiaozhen fact check completed", "total_claims", len(claims), "candidates", len(candidates), "results", len(results))
	return results
}
