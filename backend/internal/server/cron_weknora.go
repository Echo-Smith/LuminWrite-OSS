package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
)

// ─── Knowledge Base Cron Sync ───────────────────────────
// Previously: synced WeKnora knowledge base entries into local pgvector.
// Now: WeKnora is fully merged — no external sync needed.
// This cron job now generates missing embeddings for local KB entries
// and can trigger GraphRAG extraction for documents that don't have entities yet.

// cronKbSync generates missing embeddings for local KB entries.
// This replaces the old cronWeKnoraSync which pulled from an external API.
func (s *Server) cronKbSync(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: kb_sync triggered", "job", job.Name)

	if s.kbRepo == nil {
		return fmt.Errorf("knowledge base repo not available")
	}

	// Generate embeddings for entries that don't have one yet
	count, err := s.kbRepo.GenerateMissingEmbeddings(ctx, 25)
	if err != nil {
		slog.Warn("cron: kb_sync — embedding generation failed", "error", err)
	} else if count > 0 {
		slog.Info("cron: kb_sync — missing embeddings generated", "count", count)
	}

	slog.Info("cron: kb_sync completed", "job", job.Name)
	return nil
}

// cronWeKnoraSync is kept for backward compatibility with existing cron job entries.
// It delegates to cronKbSync.
func (s *Server) cronWeKnoraSync(ctx context.Context, job *database.CronJob) error {
	return s.cronKbSync(ctx, job)
}
