package tools

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// JiaozhenClient wraps the Tencent News "较真" fact-checking CLI tool.
// It uses the `tencent-news-cli jiaozhen --query=<claim>` command.
// It is optional — if not configured or if the CLI fails, it degrades gracefully.
type JiaozhenClient struct {
	enabled   bool
	cliPath   string
	apiKey    string
	timeout   time.Duration
	maxClaims int
}

// NewJiaozhenClient creates a new JiaozhenClient.
// cliPath can be empty — the client will auto-detect the CLI on PATH or in ~/.tencent-news-cli/bin/.
func NewJiaozhenClient(enabled bool, cliPath string, _ []string, apiKey string, timeout time.Duration, maxClaims int) *JiaozhenClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if maxClaims <= 0 || maxClaims > 5 {
		maxClaims = 2
	}

	// Auto-detect CLI path if not specified
	if cliPath == "" {
		cliPath = detectTencentNewsCLI()
	}

	c := &JiaozhenClient{
		enabled:   enabled,
		cliPath:   cliPath,
		apiKey:    apiKey,
		timeout:   timeout,
		maxClaims: maxClaims,
	}

	if enabled && cliPath != "" {
		slog.Info("jiaozhen client initialized", "cli_path", cliPath, "timeout", timeout)
	} else if enabled {
		slog.Warn("jiaozhen enabled but tencent-news-cli not found", "hint", "install via: curl -fsSL https://mat1.gtimg.com/qqcdn/qqnews/cli/hub/tencent-news/setup.sh | sh")
	}

	return c
}

// detectTencentNewsCLI tries to find the tencent-news-cli binary.
// Order: PATH → ~/.tencent-news-cli/bin/tencent-news-cli
func detectTencentNewsCLI() string {
	// Try PATH
	if path, err := exec.LookPath("tencent-news-cli"); err == nil {
		// Verify it works
		if err := exec.Command(path, "version").Run(); err == nil {
			return path
		}
	}

	// Try default install location
	home := getHomeDir()
	if home != "" {
		defaultPath := home + "/.tencent-news-cli/bin/tencent-news-cli"
		if exec.Command(defaultPath, "version").Run() == nil {
			return defaultPath
		}
	}

	return ""
}

// IsConfigured returns true if the client is enabled and the CLI path is set.
func (c *JiaozhenClient) IsConfigured() bool {
	return c != nil && c.enabled && c.cliPath != ""
}

// JiaozhenResult holds the result of a fact check.
type JiaozhenResult struct {
	Claim   string `json:"claim"`
	Status  string `json:"status"` // ok | skipped | error
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
// Uses: tencent-news-cli jiaozhen --query=<claim> --caller=jiaozhen-factcheck
// Only checks claims that match candidate patterns (rumors, health claims, etc.).
func (c *JiaozhenClient) CheckClaim(ctx context.Context, claim string) *JiaozhenResult {
	normalizedClaim := compactClaim(claim)

	if !c.enabled {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "disabled"}
	}
	if c.cliPath == "" {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "cli_not_found"}
	}
	if !IsJiaozhenCandidate(normalizedClaim) {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "not_candidate"}
	}

	return c.checkClaimDirect(ctx, claim, normalizedClaim)
}

// CheckClaimDirect runs the jiaozhen CLI tool to fact-check a claim without
// the candidate filter. This is used for article fact-checking where claims
// are factual statements (e.g., "教育部发布AI教育行动计划") rather than
// rumor-style questions.
func (c *JiaozhenClient) CheckClaimDirect(ctx context.Context, claim string) *JiaozhenResult {
	normalizedClaim := compactClaim(claim)

	if !c.enabled {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "disabled"}
	}
	if c.cliPath == "" {
		return &JiaozhenResult{Claim: claim, Status: "skipped", Error: "cli_not_found"}
	}

	return c.checkClaimDirect(ctx, claim, normalizedClaim)
}

// checkClaimDirect is the internal implementation that runs the CLI command.
func (c *JiaozhenClient) checkClaimDirect(ctx context.Context, claim, normalizedClaim string) *JiaozhenResult {

	// Build command: tencent-news-cli jiaozhen --query=<claim> --caller=jiaozhen-factcheck
	args := []string{
		"jiaozhen",
		"--query=" + normalizedClaim,
		"--caller=jiaozhen-factcheck",
	}

	ctxWithTimeout, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctxWithTimeout, c.cliPath, args...)

	// Inherit parent environment so the CLI can find its config file
	cmd.Env = os.Environ()
	if c.apiKey != "" {
		cmd.Env = append(cmd.Env,
			fmt.Sprintf("TENCENT_NEWS_API_KEY=%s", c.apiKey),
			fmt.Sprintf("TENCENT_NEWS_APP_KEY=%s", c.apiKey),
			fmt.Sprintf("TENCENT_NEWS_APPKEY=%s", c.apiKey),
		)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		slog.Warn("jiaozhen CLI failed",
			"claim", normalizedClaim,
			"error", err,
			"output", string(output),
			"cli_path", c.cliPath)
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

// getHomeDir returns the user's home directory.
func getHomeDir() string {
	if h := os.Getenv("HOME"); h != "" {
		return h
	}
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}
