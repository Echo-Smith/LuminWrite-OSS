package server

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

// ─── WeKnora Cron Sync ──────────────────────────────────
// Periodically syncs WeKnora knowledge base entries into the local pgvector store.
// This provides a local cache of WeKnora content for fast semantic search and
// ensures the local knowledge_base table stays up-to-date with WeKnora changes.

// cronWeKnoraSync syncs WeKnora knowledge base entries into the local pgvector store.
// It fetches all knowledge entries from the WeKnora knowledge base API and upserts them
// into the local knowledge_base table, then generates embeddings for any new entries.
func (s *Server) cronWeKnoraSync(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: weknora_sync triggered", "job", job.Name)

	wkClient := s.getWeKnoraClient()
	if wkClient == nil {
		slog.Warn("cron: weknora_sync — WeKnora client not configured, skipping")
		return nil
	}
	if s.kbRepo == nil {
		return fmt.Errorf("knowledge base repo not available")
	}

	// Step 1: Fetch + upsert
	syncWeKnoraToDB(ctx, wkClient, s.kbRepo)

	// Step 2: Generate embeddings for entries that don't have one yet
	count, err := s.kbRepo.GenerateMissingEmbeddings(ctx, 25)
	if err != nil {
		slog.Warn("cron: weknora_sync — embedding generation failed", "error", err)
	} else if count > 0 {
		slog.Info("cron: weknora_sync — missing embeddings generated", "count", count)
	}

	slog.Info("cron: weknora_sync completed", "job", job.Name)
	return nil
}

// syncWeKnoraToDB fetches all knowledge from WeKnora and upserts into the local KB repo.
func syncWeKnoraToDB(ctx context.Context, wk *tools.WeKnoraClient, repo *database.KnowledgeBaseRepo) {
	docs, err := wk.FetchAllKnowledge(ctx, 50)
	if err != nil {
		slog.Warn("cron: weknora_sync — failed to fetch knowledge from WeKnora", "error", err)
		return
	}
	slog.Info("cron: weknora_sync — fetched knowledge entries from WeKnora", "count", len(docs))

	newCount, skipCount := 0, 0
	for _, doc := range docs {
		metadata := map[string]any{
			"source":      "weknora",
			"doc_id":      doc.ID,
			"url":         doc.Source,
			"status":      doc.Status,
			"synced_from": "weknora",
		}
		entry, err := repo.AddEntry(ctx, "weknora", doc.ID, doc.Title, doc.Content, metadata)
		if err != nil {
			slog.Debug("cron: weknora_sync — failed to upsert doc", "doc_id", doc.ID, "error", err)
			skipCount++
			continue
		}
		if entry != nil {
			newCount++
		}
	}

	slog.Info("cron: weknora_sync — upsert completed",
		"new_or_updated", newCount, "skipped", skipCount, "total_fetched", len(docs))
}
