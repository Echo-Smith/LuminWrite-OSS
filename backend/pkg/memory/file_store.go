package memory

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ─── File-Based Memory Store ──────────────────────────────
//
// FileStore is a filesystem-backed memory layer that stores user
// preferences as Markdown files.  It complements the database-backed
// PgStore — not replaces it.
//
// Design:
//   - Each user has a file: data/memory/{userID}.md
//   - A global file: data/memory/_global.md
//   - Files are human-readable and human-editable
//   - Changes to files are detected and loaded automatically
//   - File memories are merged with DB memories on retrieval
//
// This is the "文件即记忆" (file-as-memory) pattern inspired by OAK's
// CLAUDE.md approach.  It provides:
//   1. Debuggability: see exactly what the system knows about a user
//   2. Editability: manually fix or add preferences without API calls
//   3. Portability: export/import memories as plain text
//   4. Transparency: no hidden database state

// FileStore manages memory files on the filesystem.
type FileStore struct {
	mu       sync.RWMutex
	baseDir  string               // data/memory/
	cache    map[string]*cachedFile // userID → cached parsed file
	cacheTTL time.Duration
}

type cachedFile struct {
	content  *MemoryFile
	modTime  time.Time
	loadedAt time.Time
}

// NewFileStore creates a new file-based memory store.
// baseDir is the directory where memory files are stored (e.g. "data/memory").
func NewFileStore(baseDir string) *FileStore {
	if baseDir == "" {
		baseDir = "data/memory"
	}
	fs := &FileStore{
		baseDir:  baseDir,
		cache:    make(map[string]*cachedFile),
		cacheTTL: 30 * time.Second,
	}

	// Ensure directory exists
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		slog.Warn("memory file store: failed to create directory", "dir", baseDir, "error", err)
	} else {
		slog.Info("memory file store initialized", "dir", baseDir)
	}

	return fs
}

// ─── User Memory Files ────────────────────────────────────

// ExportUserMemories writes user memories to a Markdown file.
// The memories parameter should be the full list from the database.
func (fs *FileStore) ExportUserMemories(userID string, memories []*Memory) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	content := ExportToMarkdown(userID, memories)
	path := fs.userFilePath(userID)

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write memory file: %w", err)
	}

	// Invalidate cache
	delete(fs.cache, userID)

	slog.Info("memory file exported",
		"user_id", userID,
		"path", path,
		"memories", len(memories),
	)

	return nil
}

// LoadUserMemories loads and parses a user's memory file.
// Returns nil if the file doesn't exist (not an error).
func (fs *FileStore) LoadUserMemories(ctx context.Context, userID string) (*MemoryFile, error) {
	fs.mu.RLock()
	cached, ok := fs.cache[userID]
	fs.mu.RUnlock()

	// Check cache freshness
	if ok && time.Since(cached.loadedAt) < fs.cacheTTL {
		// Check if file was modified
		path := fs.userFilePath(userID)
		info, err := os.Stat(path)
		if err == nil && !info.ModTime().After(cached.loadedAt) {
			return cached.content, nil
		}
	}

	// Load from disk
	path := fs.userFilePath(userID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // File doesn't exist — not an error
		}
		return nil, fmt.Errorf("failed to read memory file: %w", err)
	}

	file, err := ParseMarkdownMemory(string(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse memory file: %w", err)
	}

	// Update cache
	info, _ := os.Stat(path)
	fs.mu.Lock()
	fs.cache[userID] = &cachedFile{
		content:  file,
		loadedAt: time.Now(),
	}
	if info != nil {
		fs.cache[userID].modTime = info.ModTime()
	}
	fs.mu.Unlock()

	return file, nil
}

// GetUserMemoriesAsEntries loads file memories as MemoryEntry list
// (for injection into the writing prompt alongside DB memories).
func (fs *FileStore) GetUserMemoriesAsEntries(ctx context.Context, userID string) ([]MemoryEntry, error) {
	file, err := fs.LoadUserMemories(ctx, userID)
	if err != nil || file == nil {
		return nil, err
	}

	entries := make([]MemoryEntry, 0, len(file.Memories))
	for _, m := range file.Memories {
		if m.Status != string(StatusActive) && m.Status != "" {
			continue
		}
		entries = append(entries, MemoryEntry{
			Tier:       m.Tier,
			Category:   m.Category,
			Value:      fmt.Sprintf("%s: %s", m.Key, m.Value),
			Confidence: m.Confidence,
		})
	}

	return entries, nil
}

// GetUserMemoryMarkdown returns the raw Markdown content of a user's memory file.
func (fs *FileStore) GetUserMemoryMarkdown(userID string) (string, error) {
	path := fs.userFilePath(userID)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// ImportUserMemory imports a Markdown memory file, replacing the existing file.
// The parsed entries can then be synced back to the database.
func (fs *FileStore) ImportUserMemory(userID, content string) (*MemoryFile, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	path := fs.userFilePath(userID)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write memory file: %w", err)
	}

	// Parse and cache
	file, err := ParseMarkdownMemory(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse memory file: %w", err)
	}

	// Ensure UserID is set
	if file.UserID == "" {
		file.UserID = userID
	}

	delete(fs.cache, userID)

	slog.Info("memory file imported",
		"user_id", userID,
		"entries", len(file.Memories),
	)

	return file, nil
}

// DeleteUserMemory removes a user's memory file.
func (fs *FileStore) DeleteUserMemory(userID string) error {
	path := fs.userFilePath(userID)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}

	fs.mu.Lock()
	delete(fs.cache, userID)
	fs.mu.Unlock()

	return nil
}

// ─── Global Memory File ───────────────────────────────────

// GetGlobalMemory loads the global memory file (_global.md).
// This is the CLAUDE.md-style system-level preferences.
func (fs *FileStore) GetGlobalMemory(ctx context.Context) ([]MemoryFileEntry, error) {
	path := fs.globalFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return ParseGlobalMemoryFile(string(data)), nil
}

// GetGlobalMemoryMarkdown returns the raw content of the global memory file.
func (fs *FileStore) GetGlobalMemoryMarkdown() (string, error) {
	path := fs.globalFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// SaveGlobalMemory writes the global memory file.
func (fs *FileStore) SaveGlobalMemory(content string) error {
	path := fs.globalFilePath()
	return os.WriteFile(path, []byte(content), 0644)
}

// ─── File System Helpers ──────────────────────────────────

// userFilePath returns the file path for a user's memory file.
func (fs *FileStore) userFilePath(userID string) string {
	// Sanitize userID for filesystem safety
	safe := sanitizeFilename(userID)
	return filepath.Join(fs.baseDir, safe+".md")
}

// globalFilePath returns the path for the global memory file.
func (fs *FileStore) globalFilePath() string {
	return filepath.Join(fs.baseDir, "_global.md")
}

// sanitizeFilename removes characters that are unsafe for filenames.
func sanitizeFilename(s string) string {
	// Replace common dangerous characters
	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"..", "_",
		"\x00", "",
	)
	return replacer.Replace(s)
}

// ─── File Watching (optional, for hot-reload) ─────────────

// Watch starts a goroutine that periodically checks for file changes
// and invalidates the cache.  Call this once at startup.
func (fs *FileStore) Watch(ctx context.Context, interval time.Duration) {
	if interval == 0 {
		interval = 1 * time.Minute
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fs.invalidateStaleCache()
			}
		}
	}()
}

// invalidateStaleCache checks cached files against disk modification times
// and removes stale entries.
func (fs *FileStore) invalidateStaleCache() {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for userID, cached := range fs.cache {
		path := fs.userFilePath(userID)
		info, err := os.Stat(path)
		if err != nil {
			// File was deleted
			delete(fs.cache, userID)
			continue
		}
		if info.ModTime().After(cached.modTime) {
			delete(fs.cache, userID)
		}
	}
}

// ─── File Store ↔ DB Sync ─────────────────────────────────

// FileMemorySyncer synchronizes file-based memories back to the database.
// This allows manual edits to memory files to be persisted to the DB.
type FileMemorySyncer struct {
	fileStore *FileStore
	store     Store // DB-backed store
}

// NewFileMemorySyncer creates a syncer between file and DB stores.
func NewFileMemorySyncer(fileStore *FileStore, store Store) *FileMemorySyncer {
	return &FileMemorySyncer{
		fileStore: fileStore,
		store:     store,
	}
}

// SyncToDB reads the user's memory file and upserts entries to the DB.
// This is called when a user imports a memory file.
func (s *FileMemorySyncer) SyncToDB(ctx context.Context, userID string) (int, error) {
	file, err := s.fileStore.LoadUserMemories(ctx, userID)
	if err != nil {
		return 0, err
	}
	if file == nil {
		return 0, nil
	}

	synced := 0
	for _, entry := range file.Memories {
		mem := &Memory{
			UserID:        userID,
			Tier:          entry.Tier,
			Category:      entry.Category,
			Key:           entry.Key,
			Value:         entry.Value,
			Confidence:    entry.Confidence,
			Occurrences:   entry.Occurrences,
			SourceTraceID: entry.SourceTraceID,
			Status:        MemoryStatus(entry.Status),
			FirstSeen:     time.Now(),
			LastSeen:      time.Now(),
		}

		if err := s.store.Save(ctx, mem); err != nil {
			slog.Warn("file memory sync: failed to save",
				"category", entry.Category,
				"key", entry.Key,
				"error", err,
			)
			continue
		}
		synced++
	}

	slog.Info("file memory sync completed",
		"user_id", userID,
		"synced", synced,
		"total", len(file.Memories),
	)

	return synced, nil
}

// SyncFromDB exports DB memories to a file.
// This is called to generate the initial memory file or refresh it.
func (s *FileMemorySyncer) SyncFromDB(ctx context.Context, userID string) error {
	memories, err := s.store.List(ctx, userID, ListOptions{Limit: 100})
	if err != nil {
		return fmt.Errorf("failed to list DB memories: %w", err)
	}

	return s.fileStore.ExportUserMemories(userID, memories)
}
