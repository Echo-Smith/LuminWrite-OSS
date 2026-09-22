package memory

import (
	"fmt"
	"strings"
	"time"
)

// ─── Markdown Memory File Format ──────────────────────────
//
// The memory file is a human-readable, human-editable Markdown file
// that summarizes a user's writing preferences.  It is similar to
// CLAUDE.md — a project-level memory file that gets injected into
// prompts as context.
//
// Format:
//
//   # 用户写作偏好记忆
//   > UserID: 550e8400-e29b-41d4-a716-446655440000
//   > LastUpdated: 2025-01-15T10:30:00Z
//   > MemoryCount: 12
//
//   ## 硬偏好 (Tier 1)
//   > 用户手动设置的偏好，置信度最高
//
//   ### word_count
//   - **requested_word_limit**: 1200
//     - confidence: 1.00 | occurrences: 5 | status: active
//
//   ### style
//   - **selected_style**: yinyue
//     - confidence: 1.00 | occurrences: 3 | status: active
//
//   ## 行为模式 (Tier 2)
//   > 自动提取的写作模式
//
//   ### tone
//   - **preferred_tone**: 严肃但有温度
//     - confidence: 0.85 | occurrences: 8 | status: active
//     - source_trace: trace_abc123
//
//   ## 反馈记忆 (Tier 3)
//   > 基于用户反馈的改进信号
//
//   ### title
//   - **avoid_pattern**: 不要使用伤亡数字做标题
//     - confidence: 0.90 | occurrences: 2 | status: active
//
//   ---
//   > 此文件由系统自动生成，可手动编辑后导入。
//   > 编辑后请通过 API 导入以更新数据库中的记忆。

// MemoryFile represents the parsed content of a Markdown memory file.
type MemoryFile struct {
	UserID      string    `json:"user_id"`
	LastUpdated time.Time `json:"last_updated"`
	Memories    []MemoryFileEntry `json:"memories"`
}

// MemoryFileEntry is a single memory entry parsed from / serialized to Markdown.
type MemoryFileEntry struct {
	Tier          Tier    `json:"tier"`
	Category      string  `json:"category"`
	Key           string  `json:"key"`
	Value         string  `json:"value"`
	Confidence    float64 `json:"confidence"`
	Occurrences   int     `json:"occurrences"`
	Status        string  `json:"status"`
	SourceTraceID string  `json:"source_trace_id,omitempty"`
}

// ─── Export: Memories → Markdown ──────────────────────────

// ExportToMarkdown converts a list of memories to a Markdown memory file.
// The result is human-readable and can be edited manually.
func ExportToMarkdown(userID string, memories []*Memory) string {
	var sb strings.Builder

	// Header
	sb.WriteString("# 用户写作偏好记忆\n")
	sb.WriteString(fmt.Sprintf("> UserID: %s\n", userID))
	sb.WriteString(fmt.Sprintf("> LastUpdated: %s\n", time.Now().Format(time.RFC3339)))
	sb.WriteString(fmt.Sprintf("> MemoryCount: %d\n\n", len(memories)))

	// Group by tier
	tiers := []struct {
		tier Tier
		name string
		desc string
	}{
		{TierHard, "硬偏好 (Tier 1)", "用户手动设置的偏好，置信度最高"},
		{TierPattern, "行为模式 (Tier 2)", "自动提取的写作模式"},
		{TierFeedback, "反馈记忆 (Tier 3)", "基于用户反馈的改进信号"},
	}

	for _, t := range tiers {
		tierMems := filterByTier(memories, t.tier)
		if len(tierMems) == 0 {
			continue
		}

		sb.WriteString(fmt.Sprintf("## %s\n", t.name))
		sb.WriteString(fmt.Sprintf("> %s\n\n", t.desc))

		// Group by category within each tier
		categories := groupByCategory(tierMems)
		for _, cat := range sortedCategories(categories) {
			sb.WriteString(fmt.Sprintf("### %s\n", cat))
			for _, m := range categories[cat] {
				if m.Status == StatusSuperseded || m.Status == StatusArchived {
					continue
				}
				writeMemoryEntry(&sb, m)
			}
			sb.WriteString("\n")
		}
	}

	// Footer
	sb.WriteString("---\n")
	sb.WriteString("> 此文件由系统自动生成，可手动编辑后导入。\n")
	sb.WriteString("> 编辑后请通过 API 导入以更新数据库中的记忆。\n")

	return sb.String()
}

// writeMemoryEntry writes a single memory as Markdown bullet points.
func writeMemoryEntry(sb *strings.Builder, m *Memory) {
	sb.WriteString(fmt.Sprintf("- **%s**: %s\n", m.Key, m.Value))
	sb.WriteString(fmt.Sprintf("  - confidence: %.2f | occurrences: %d | status: %s\n",
		m.Confidence, m.Occurrences, string(m.Status)))
	if m.SourceTraceID != "" {
		sb.WriteString(fmt.Sprintf("  - source_trace: %s\n", m.SourceTraceID))
	}
	if m.QualitySource != "" {
		sb.WriteString(fmt.Sprintf("  - quality_source: %s (weight: %.2f)\n", m.QualitySource, m.QualityWeight))
	}
}

// ─── Import: Markdown → Memories ──────────────────────────

// ParseMarkdownMemory parses a Markdown memory file and returns
// the extracted memory entries.  This is the reverse of ExportToMarkdown.
//
// The parser is lenient: it skips lines it doesn't understand
// and doesn't require strict formatting.
func ParseMarkdownMemory(content string) (*MemoryFile, error) {
	file := &MemoryFile{
		LastUpdated: time.Now(),
	}

	lines := strings.Split(content, "\n")

	var currentTier Tier
	var currentCategory string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse header metadata
		if strings.HasPrefix(line, "> UserID:") {
			file.UserID = strings.TrimSpace(strings.TrimPrefix(line, "> UserID:"))
			continue
		}
		if strings.HasPrefix(line, "> LastUpdated:") {
			tsStr := strings.TrimSpace(strings.TrimPrefix(line, "> LastUpdated:"))
			if ts, err := time.Parse(time.RFC3339, tsStr); err == nil {
				file.LastUpdated = ts
			}
			continue
		}

		// Parse tier headers
		if strings.HasPrefix(line, "## 硬偏好") {
			currentTier = TierHard
			continue
		}
		if strings.HasPrefix(line, "## 行为模式") {
			currentTier = TierPattern
			continue
		}
		if strings.HasPrefix(line, "## 反馈记忆") {
			currentTier = TierFeedback
			continue
		}

		// Parse category headers
		if strings.HasPrefix(line, "### ") {
			currentCategory = strings.TrimSpace(strings.TrimPrefix(line, "### "))
			continue
		}

		// Parse memory entries: "- **key**: value"
		if strings.HasPrefix(line, "- **") {
			entry := parseMemoryEntry(line, lines, &i)
			if entry.Category == "" {
				entry.Category = currentCategory
			}
			if entry.Tier == "" {
				entry.Tier = currentTier
			}
			if entry.Status == "" {
				entry.Status = string(StatusActive)
			}
			if entry.Confidence == 0 {
				entry.Confidence = 0.5
			}
			file.Memories = append(file.Memories, entry)
		}
	}

	return file, nil
}

// parseMemoryEntry parses a bullet point line and the following metadata lines.
func parseMemoryEntry(line string, lines []string, i *int) MemoryFileEntry {
	entry := MemoryFileEntry{}

	// Parse "- **key**: value"
	// Remove leading "- **"
	rest := strings.TrimPrefix(line, "- **")
	// Find closing "**"
	idx := strings.Index(rest, "**")
	if idx < 0 {
		return entry
	}
	entry.Key = rest[:idx]
	// Rest after "**: "
	rest = strings.TrimPrefix(rest[idx+2:], ": ")
	entry.Value = strings.TrimSpace(rest)

	// Parse following indented lines (start with "  - ")
	for *i+1 < len(lines) {
		next := strings.TrimSpace(lines[*i+1])
		if !strings.HasPrefix(next, "- ") {
			break
		}
		// Check if it's a metadata line (indented)
		if !strings.HasPrefix(lines[*i+1], "  -") {
			break
		}

		*i++

		meta := strings.TrimSpace(strings.TrimPrefix(next, "- "))
		// Parse "confidence: 0.85 | occurrences: 8 | status: active"
		parts := strings.Split(meta, "|")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "confidence:") {
				var conf float64
				fmt.Sscanf(strings.TrimPrefix(p, "confidence:"), "%f", &conf)
				entry.Confidence = conf
			} else if strings.HasPrefix(p, "occurrences:") {
				var occ int
				fmt.Sscanf(strings.TrimPrefix(p, "occurrences:"), "%d", &occ)
				entry.Occurrences = occ
			} else if strings.HasPrefix(p, "status:") {
				entry.Status = strings.TrimSpace(strings.TrimPrefix(p, "status:"))
			} else if strings.HasPrefix(p, "source_trace:") {
				entry.SourceTraceID = strings.TrimSpace(strings.TrimPrefix(p, "source_trace:"))
			}
		}
	}

	return entry
}

// ─── Helpers ──────────────────────────────────────────────

func filterByTier(memories []*Memory, tier Tier) []*Memory {
	var result []*Memory
	for _, m := range memories {
		if m.Tier == tier {
			result = append(result, m)
		}
	}
	return result
}

func groupByCategory(memories []*Memory) map[string][]*Memory {
	result := make(map[string][]*Memory)
	for _, m := range memories {
		result[m.Category] = append(result[m.Category], m)
	}
	return result
}

func sortedCategories(cats map[string][]*Memory) []string {
	keys := make([]string, 0, len(cats))
	for k := range cats {
		keys = append(keys, k)
	}
	// Simple sort (no need for sort.Slice)
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// ─── Global Memory File (CLAUDE.md style) ─────────────────

// GlobalMemoryFile is a system-level memory file that applies to all users.
// It's similar to CLAUDE.md — project-level instructions injected into
// every writing prompt.
//
// The global file is stored at data/memory/_global.md and is loaded
// at startup.  It contains style guidelines, tone preferences, and
// editorial standards that apply universally.

// FormatGlobalMemoryFile generates the global memory file content.
func FormatGlobalMemoryFile(entries []MemoryFileEntry) string {
	var sb strings.Builder

	sb.WriteString("# 全局写作偏好\n")
	sb.WriteString("> 此文件定义所有用户共享的写作偏好和编辑标准。\n")
	sb.WriteString("> 类似于 CLAUDE.md，内容会注入到每次写作的 prompt 中。\n")
	sb.WriteString(fmt.Sprintf("> LastUpdated: %s\n\n", time.Now().Format(time.RFC3339)))

	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("## %s\n", e.Category))
		sb.WriteString(fmt.Sprintf("- **%s**: %s\n\n", e.Key, e.Value))
	}

	sb.WriteString("---\n")
	sb.WriteString("> 编辑此文件后重启服务或调用刷新 API 以应用更改。\n")

	return sb.String()
}

// ParseGlobalMemoryFile parses a global memory file.
func ParseGlobalMemoryFile(content string) []MemoryFileEntry {
	var entries []MemoryFileEntry

	lines := strings.Split(content, "\n")
	var currentCategory string

	for i := 0; i < len(lines); i++ {
		line := strings.TrimSpace(lines[i])

		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ">") {
			if strings.HasPrefix(line, "## ") {
				currentCategory = strings.TrimSpace(strings.TrimPrefix(line, "## "))
			}
			continue
		}

		if strings.HasPrefix(line, "- **") {
			entry := MemoryFileEntry{
				Category:   currentCategory,
				Tier:       TierHard,
				Confidence: 1.0,
				Status:     string(StatusActive),
			}

			rest := strings.TrimPrefix(line, "- **")
			idx := strings.Index(rest, "**")
			if idx < 0 {
				continue
			}
			entry.Key = rest[:idx]
			entry.Value = strings.TrimSpace(strings.TrimPrefix(rest[idx+2:], ": "))

			entries = append(entries, entry)
		}
	}

	return entries
}

// ─── Prompt Injection ─────────────────────────────────────

// FormatMemoryFileForPrompt formats memory file entries as a prompt section.
// This is injected into the writing system prompt alongside other context.
func FormatMemoryFileForPrompt(entries []MemoryFileEntry) string {
	if len(entries) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n--- 用户偏好记忆文件 ---\n")

	// Group by category
	categories := make(map[string][]MemoryFileEntry)
	for _, e := range entries {
		categories[e.Category] = append(categories[e.Category], e)
	}

	for cat, ents := range categories {
		sb.WriteString(fmt.Sprintf("[%s]\n", cat))
		for _, e := range ents {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", e.Key, e.Value))
		}
	}

	return sb.String()
}
