package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Edition boundary guard (docs/29-wp5-boundary-governance.md).
//
// The OSS edition must not contain paid search provider credential
// variables or commercial payment credential variables in its deployment
// templates. This test scans .env.docker.example and backend/.env.example
// for banned variable names and fails CI when a leak is introduced.
//
// The code-level stub pattern (search_stubs.go returning
// ErrProviderNotInstalled) is enforced by search_capability_test.go.
// This test enforces the *deployment config* boundary.

// bannedEnvVars lists paid search provider and commercial payment
// credential variable prefixes that must never appear in OSS env templates.
// Shared free-tier LLM/embedding vars (DEEPSEEK_*, LLM_*, DASHSCOPE_*) are
// intentionally absent from this list.
var bannedEnvVars = []string{
	"TAVILY_",
	"ANYSEARCH_",
	"ALIPAY_",
	"SERPER_",
	"BRAVE_",
	"BOCHA_",
}

// allowedEnvExceptions lists specific variables that match a banned prefix
// but are documented as edition-neutral. Keep this list minimal.
var allowedEnvExceptions = map[string]bool{}

func TestOSSEnvTemplatesHaveNoPaidProviderVars(t *testing.T) {
	repoRoot := findOSEnvRoot(t)
	templates := []string{
		filepath.Join(repoRoot, ".env.docker.example"),
		filepath.Join(repoRoot, "backend", ".env.example"),
	}

	for _, tmpl := range templates {
		data, err := os.ReadFile(tmpl)
		if err != nil {
			t.Fatalf("read %s: %v", tmpl, err)
		}
		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			for _, banned := range bannedEnvVars {
				if strings.HasPrefix(trimmed, banned) {
					varName := extractVarName(trimmed)
					if allowedEnvExceptions[varName] {
						continue
					}
					t.Errorf("boundary violation in %s:%d: %q — 付费搜索/商业凭证变量不得出现在 OSS 部署模板",
						tmpl, i+1, varName)
				}
			}
		}
	}
}

func TestOSSEnvTemplatesNoTavilyEndpoint(t *testing.T) {
	// Even in comments, OSS must not embed paid provider API endpoints
	// as if they were built-in capabilities.
	repoRoot := findOSEnvRoot(t)
	templates := []string{
		filepath.Join(repoRoot, ".env.docker.example"),
		filepath.Join(repoRoot, "backend", ".env.example"),
	}
	for _, tmpl := range templates {
		data, err := os.ReadFile(tmpl)
		if err != nil {
			t.Fatalf("read %s: %v", tmpl, err)
		}
		content := string(data)
		if strings.Contains(content, "api.tavily.com") {
			t.Errorf("boundary violation in %s: contains api.tavily.com — 付费搜索端点不得出现在 OSS 部署模板", tmpl)
		}
		if strings.Contains(content, "api.anysearch.com") {
			t.Errorf("boundary violation in %s: contains api.anysearch.com — 付费搜索端点不得出现在 OSS 部署模板", tmpl)
		}
	}
}

// extractVarName extracts the variable name from a KEY=value line.
func extractVarName(line string) string {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return line
	}
	return strings.TrimSpace(line[:idx])
}

// findOSEnvRoot locates the repository root containing .env.docker.example.
func findOSEnvRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".env.docker.example")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("locate repository root: .env.docker.example not found")
	return ""
}
