package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ─── Export → Markdown Tests ──────────────────────────────

func TestExportToMarkdown_Basic(t *testing.T) {
	memories := []*Memory{
		{
			UserID:      "user-123",
			Tier:        TierHard,
			Category:    "word_count",
			Key:         "requested_word_limit",
			Value:       "1200",
			Confidence:  1.0,
			Occurrences: 5,
			Status:      StatusActive,
		},
		{
			UserID:      "user-123",
			Tier:        TierPattern,
			Category:    "tone",
			Key:         "preferred_tone",
			Value:       "严肃但有温度",
			Confidence:  0.85,
			Occurrences: 8,
			Status:      StatusActive,
		},
		{
			UserID:      "user-123",
			Tier:        TierFeedback,
			Category:    "title",
			Key:         "avoid_pattern",
			Value:       "不要使用伤亡数字做标题",
			Confidence:  0.90,
			Occurrences: 2,
			Status:      StatusActive,
		},
	}

	md := ExportToMarkdown("user-123", memories)

	// Check header
	if !strings.Contains(md, "用户写作偏好记忆") {
		t.Error("expected header in markdown")
	}
	if !strings.Contains(md, "user-123") {
		t.Error("expected user ID in markdown")
	}

	// Check tier sections
	if !strings.Contains(md, "硬偏好 (Tier 1)") {
		t.Error("expected Tier 1 section")
	}
	if !strings.Contains(md, "行为模式 (Tier 2)") {
		t.Error("expected Tier 2 section")
	}
	if !strings.Contains(md, "反馈记忆 (Tier 3)") {
		t.Error("expected Tier 3 section")
	}

	// Check memory entries
	if !strings.Contains(md, "requested_word_limit") {
		t.Error("expected word_count key")
	}
	if !strings.Contains(md, "1200") {
		t.Error("expected word count value")
	}
	if !strings.Contains(md, "严肃但有温度") {
		t.Error("expected tone value")
	}

	// Check metadata
	if !strings.Contains(md, "confidence: 1.00") {
		t.Error("expected confidence in markdown")
	}
}

func TestExportToMarkdown_Empty(t *testing.T) {
	md := ExportToMarkdown("user-empty", []*Memory{})
	if !strings.Contains(md, "MemoryCount: 0") {
		t.Error("expected MemoryCount: 0 for empty list")
	}
}

func TestExportToMarkdown_SkipsArchived(t *testing.T) {
	memories := []*Memory{
		{
			Tier:     TierHard,
			Category: "style",
			Key:      "active",
			Value:    "yinyue",
			Status:   StatusActive,
		},
		{
			Tier:     TierHard,
			Category: "style",
			Key:      "archived",
			Value:    "old_style",
			Status:   StatusArchived,
		},
	}

	md := ExportToMarkdown("user-test", memories)

	if !strings.Contains(md, "active") {
		t.Error("expected active memory in export")
	}
	if strings.Contains(md, "old_style") {
		t.Error("archived memory should not appear in export")
	}
}

// ─── Parse ← Markdown Tests ───────────────────────────────

func TestParseMarkdownMemory_Basic(t *testing.T) {
	md := `# 用户写作偏好记忆
> UserID: user-123
> LastUpdated: 2025-01-15T10:30:00Z
> MemoryCount: 2

## 硬偏好 (Tier 1)
> 用户手动设置的偏好，置信度最高

### word_count
- **requested_word_limit**: 1200
  - confidence: 1.00 | occurrences: 5 | status: active

### style
- **selected_style**: yinyue
  - confidence: 1.00 | occurrences: 3 | status: active

## 行为模式 (Tier 2)
> 自动提取的写作模式

### tone
- **preferred_tone**: 严肃但有温度
  - confidence: 0.85 | occurrences: 8 | status: active
  - source_trace: trace_abc123

---
> 此文件由系统自动生成，可手动编辑后导入。
`

	file, err := ParseMarkdownMemory(md)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if file.UserID != "user-123" {
		t.Errorf("expected UserID 'user-123', got '%s'", file.UserID)
	}

	if len(file.Memories) != 3 {
		t.Fatalf("expected 3 memories, got %d", len(file.Memories))
	}

	// Check first memory
	m0 := file.Memories[0]
	if m0.Tier != TierHard {
		t.Errorf("expected Tier 'hard', got '%s'", m0.Tier)
	}
	if m0.Category != "word_count" {
		t.Errorf("expected category 'word_count', got '%s'", m0.Category)
	}
	if m0.Key != "requested_word_limit" {
		t.Errorf("expected key 'requested_word_limit', got '%s'", m0.Key)
	}
	if m0.Value != "1200" {
		t.Errorf("expected value '1200', got '%s'", m0.Value)
	}
	if m0.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", m0.Confidence)
	}
	if m0.Occurrences != 5 {
		t.Errorf("expected occurrences 5, got %d", m0.Occurrences)
	}

	// Check third memory (with source_trace)
	m2 := file.Memories[2]
	if m2.Tier != TierPattern {
		t.Errorf("expected Tier 'pattern', got '%s'", m2.Tier)
	}
	if m2.SourceTraceID != "trace_abc123" {
		t.Errorf("expected source_trace 'trace_abc123', got '%s'", m2.SourceTraceID)
	}
}

func TestParseMarkdownMemory_Empty(t *testing.T) {
	file, err := ParseMarkdownMemory("")
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(file.Memories) != 0 {
		t.Errorf("expected 0 memories, got %d", len(file.Memories))
	}
}

// ─── Round-Trip Test ──────────────────────────────────────

func TestExportParseRoundTrip(t *testing.T) {
	original := []*Memory{
		{
			UserID:      "user-rt",
			Tier:        TierHard,
			Category:    "style",
			Key:         "selected_style",
			Value:       "yinyue",
			Confidence:  1.0,
			Occurrences: 3,
			Status:      StatusActive,
		},
		{
			UserID:      "user-rt",
			Tier:        TierPattern,
			Category:    "tone",
			Key:         "preferred_tone",
			Value:       "温暖",
			Confidence:  0.75,
			Occurrences: 4,
			Status:      StatusActive,
		},
	}

	// Export to markdown
	md := ExportToMarkdown("user-rt", original)

	// Parse back
	file, err := ParseMarkdownMemory(md)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	if len(file.Memories) != 2 {
		t.Fatalf("expected 2 memories after round-trip, got %d", len(file.Memories))
	}

	// Find and check each memory
	findMem := func(tier Tier, category, key string) *MemoryFileEntry {
		for _, m := range file.Memories {
			if m.Tier == tier && m.Category == category && m.Key == key {
				return &m
			}
		}
		return nil
	}

	m1 := findMem(TierHard, "style", "selected_style")
	if m1 == nil {
		t.Fatal("expected to find style memory after round-trip")
	}
	if m1.Value != "yinyue" {
		t.Errorf("expected value 'yinyue', got '%s'", m1.Value)
	}
	if m1.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", m1.Confidence)
	}

	m2 := findMem(TierPattern, "tone", "preferred_tone")
	if m2 == nil {
		t.Fatal("expected to find tone memory after round-trip")
	}
	if m2.Value != "温暖" {
		t.Errorf("expected value '温暖', got '%s'", m2.Value)
	}
}

// ─── Global Memory File Tests ─────────────────────────────

func TestGlobalMemoryFile_RoundTrip(t *testing.T) {
	entries := []MemoryFileEntry{
		{Category: "editorial", Key: "tone", Value: "理性客观"},
		{Category: "editorial", Key: "structure", Value: "三段论"},
		{Category: "safety", Key: "forbidden", Value: "禁止造谣"},
	}

	md := FormatGlobalMemoryFile(entries)
	parsed := ParseGlobalMemoryFile(md)

	if len(parsed) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(parsed))
	}

	// Check first entry
	if parsed[0].Category != "editorial" {
		t.Errorf("expected category 'editorial', got '%s'", parsed[0].Category)
	}
	if parsed[0].Key != "tone" {
		t.Errorf("expected key 'tone', got '%s'", parsed[0].Key)
	}
	if parsed[0].Value != "理性客观" {
		t.Errorf("expected value '理性客观', got '%s'", parsed[0].Value)
	}
}

// ─── FormatMemoryFileForPrompt Test ───────────────────────

func TestFormatMemoryFileForPrompt(t *testing.T) {
	entries := []MemoryFileEntry{
		{Category: "style", Key: "tone", Value: "温暖"},
		{Category: "style", Key: "length", Value: "1000字"},
		{Category: "topic", Key: "preferred", Value: "科技"},
	}

	result := FormatMemoryFileForPrompt(entries)

	if !strings.Contains(result, "用户偏好记忆文件") {
		t.Error("expected header in prompt")
	}
	if !strings.Contains(result, "style") {
		t.Error("expected 'style' category in prompt")
	}
	if !strings.Contains(result, "温暖") {
		t.Error("expected '温暖' value in prompt")
	}
}

func TestFormatMemoryFileForPrompt_Empty(t *testing.T) {
	result := FormatMemoryFileForPrompt(nil)
	if result != "" {
		t.Error("expected empty string for nil entries")
	}
}

// ─── FileStore Tests ──────────────────────────────────────

func TestFileStore_ExportAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	memories := []*Memory{
		{
			UserID:      "user-fs-test",
			Tier:        TierHard,
			Category:    "style",
			Key:         "selected_style",
			Value:       "yinyue",
			Confidence:  1.0,
			Occurrences: 2,
			Status:      StatusActive,
		},
	}

	// Export
	if err := fs.ExportUserMemories("user-fs-test", memories); err != nil {
		t.Fatalf("export failed: %v", err)
	}

	// Verify file exists
	path := filepath.Join(tmpDir, "user-fs-test.md")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("expected memory file to exist")
	}

	// Load
	file, err := fs.LoadUserMemories(context.Background(), "user-fs-test")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if file == nil {
		t.Fatal("expected non-nil file")
	}
	if len(file.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(file.Memories))
	}
	if file.Memories[0].Value != "yinyue" {
		t.Errorf("expected value 'yinyue', got '%s'", file.Memories[0].Value)
	}
}

func TestFileStore_LoadNonExistent(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	file, err := fs.LoadUserMemories(context.Background(), "nonexistent-user")
	if err != nil {
		t.Fatalf("expected nil error for non-existent file, got: %v", err)
	}
	if file != nil {
		t.Fatal("expected nil file for non-existent user")
	}
}

func TestFileStore_ImportAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	content := `# 用户写作偏好记忆
> UserID: user-import-test
> MemoryCount: 1

## 硬偏好 (Tier 1)
> 用户手动设置的偏好

### style
- **selected_style**: shenlun
  - confidence: 1.00 | occurrences: 1 | status: active
`

	// Import
	file, err := fs.ImportUserMemory("user-import-test", content)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(file.Memories) != 1 {
		t.Fatalf("expected 1 memory, got %d", len(file.Memories))
	}

	// Load from disk
	loaded, err := fs.LoadUserMemories(context.Background(), "user-import-test")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil loaded file")
	}
	if loaded.Memories[0].Value != "shenlun" {
		t.Errorf("expected value 'shenlun', got '%s'", loaded.Memories[0].Value)
	}
}

func TestFileStore_GetUserMemoryMarkdown(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	// Non-existent file returns empty string, no error
	md, err := fs.GetUserMemoryMarkdown("nonexistent")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md != "" {
		t.Errorf("expected empty string, got '%s'", md)
	}

	// After export
	memories := []*Memory{
		{Tier: TierHard, Category: "cat", Key: "key", Value: "val", Status: StatusActive, Confidence: 0.5},
	}
	fs.ExportUserMemories("user-md", memories)

	md, err = fs.GetUserMemoryMarkdown("user-md")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(md, "user-md") {
		t.Error("expected user ID in markdown")
	}
}

func TestFileStore_GetUserMemoriesAsEntries(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	memories := []*Memory{
		{Tier: TierHard, Category: "style", Key: "tone", Value: "温暖", Confidence: 0.9, Status: StatusActive},
		{Tier: TierHard, Category: "style", Key: "archived", Value: "old", Status: StatusArchived, Confidence: 0.5},
	}
	fs.ExportUserMemories("user-entries", memories)

	entries, err := fs.GetUserMemoriesAsEntries(context.Background(), "user-entries")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Archived should be filtered out
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (archived filtered), got %d", len(entries))
	}
}

func TestFileStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	memories := []*Memory{
		{Tier: TierHard, Category: "cat", Key: "key", Value: "val", Status: StatusActive},
	}
	fs.ExportUserMemories("user-del", memories)

	// Delete
	if err := fs.DeleteUserMemory("user-del"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	// Verify file is gone
	file, err := fs.LoadUserMemories(context.Background(), "user-del")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if file != nil {
		t.Fatal("expected nil file after deletion")
	}
}

func TestFileStore_CacheInvalidation(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)
	fs.cacheTTL = 0 // Force cache miss

	memories := []*Memory{
		{Tier: TierHard, Category: "cat", Key: "key", Value: "v1", Status: StatusActive, Confidence: 0.5},
	}

	// Export v1
	fs.ExportUserMemories("user-cache", memories)

	// Load (populates cache)
	file, _ := fs.LoadUserMemories(context.Background(), "user-cache")
	if file.Memories[0].Value != "v1" {
		t.Fatalf("expected v1, got '%s'", file.Memories[0].Value)
	}

	// Export v2 (invalidates cache)
	memories[0].Value = "v2"
	fs.ExportUserMemories("user-cache", memories)

	// Load again (should get v2)
	file, _ = fs.LoadUserMemories(context.Background(), "user-cache")
	if file.Memories[0].Value != "v2" {
		t.Errorf("expected v2 after cache invalidation, got '%s'", file.Memories[0].Value)
	}
}

func TestFileStore_GlobalMemory(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)

	// Non-existent global file
	entries, err := fs.GetGlobalMemory(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatal("expected nil entries for non-existent global file")
	}

	// Save global memory
	content := "# 全局写作偏好\n\n## editorial\n- **tone**: 理性客观\n\n## safety\n- **forbidden**: 禁止造谣\n"
	if err := fs.SaveGlobalMemory(content); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	// Load
	entries, err = fs.GetGlobalMemory(context.Background())
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestFileStore_SanitizeFilename(t *testing.T) {
	// Test that dangerous characters are sanitized
	unsafe := "../../../etc/passwd"
	safe := sanitizeFilename(unsafe)

	if strings.Contains(safe, "..") {
		t.Errorf("expected '..' to be sanitized, got '%s'", safe)
	}
	if strings.Contains(safe, "/") {
		t.Errorf("expected '/' to be sanitized, got '%s'", safe)
	}
}

// ─── FileMemorySyncer Tests (with mock store) ─────────────

func TestFileMemorySyncer_SyncFromDB(t *testing.T) {
	tmpDir := t.TempDir()
	fs := NewFileStore(tmpDir)
	mockStore := &mockFileSyncStore{}
	syncer := NewFileMemorySyncer(fs, mockStore)

	if err := syncer.SyncFromDB(context.Background(), "user-sync"); err != nil {
		t.Fatalf("SyncFromDB failed: %v", err)
	}

	// Verify file was created
	md, err := fs.GetUserMemoryMarkdown("user-sync")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if md == "" {
		t.Error("expected non-empty markdown after sync")
	}
}

// mockFileSyncStore implements Store for testing file sync.
type mockFileSyncStore struct{}

func (m *mockFileSyncStore) Save(ctx context.Context, mem *Memory) error { return nil }
func (m *mockFileSyncStore) Get(ctx context.Context, id string) (*Memory, error) { return nil, nil }
func (m *mockFileSyncStore) List(ctx context.Context, userID string, opts ListOptions) ([]*Memory, error) {
	return []*Memory{
		{
			UserID:      userID,
			Tier:        TierHard,
			Category:    "style",
			Key:         "test",
			Value:       "test_value",
			Confidence:  0.8,
			Occurrences: 1,
			Status:      StatusActive,
			FirstSeen:   time.Now(),
			LastSeen:    time.Now(),
		},
	}, nil
}
func (m *mockFileSyncStore) Search(ctx context.Context, userID string, queryVector []float32, limit int) ([]*Memory, error) {
	return nil, nil
}
func (m *mockFileSyncStore) FindByCategoryKey(ctx context.Context, userID, category, key string) ([]*Memory, error) {
	return nil, nil
}
func (m *mockFileSyncStore) UpdateStatus(ctx context.Context, id string, status MemoryStatus) error { return nil }
func (m *mockFileSyncStore) IncrementOccurrence(ctx context.Context, id string) error { return nil }
func (m *mockFileSyncStore) Supersede(ctx context.Context, oldID, newID string) error { return nil }
func (m *mockFileSyncStore) Delete(ctx context.Context, id string) error { return nil }
func (m *mockFileSyncStore) DismissForSession(ctx context.Context, memoryID, sessionID string) error { return nil }
func (m *mockFileSyncStore) GetDismissals(ctx context.Context, sessionID string) ([]string, error) {
	return nil, nil
}
