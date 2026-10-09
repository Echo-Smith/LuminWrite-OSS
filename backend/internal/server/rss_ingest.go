package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	neturl "net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services/rss"
)

// errRSSDuplicateContent marks a content-hash collision in knowledge_base
// (uk_kb_content_hash is a global unique constraint — two users importing
// byte-identical content collide; see docs/30 §limitations). The tick treats
// it as a skip, never a failure.
var errRSSDuplicateContent = errors.New("duplicate content already in knowledge base")

// rssIngestTuning bounds one fetch tick so the 5-minute cron budget and the
// embedding service stay healthy: RSS volume is orders of magnitude larger
// than the old kb_auto_import pipeline, and AddChunk embeds synchronously.
const (
	rssGlobalTickBudget = 20 // max items per tick across all subscriptions
	rssFeedTickBudget   = 3  // max items per subscription per tick (also the schema default)
	rssMinItemRunes     = 50 // shorter items carry no retrievable content
	// rssFullTextBelow: feed bodies under this length are treated as
	// summaries when fetch_full_text is on (full articles are far longer).
	rssFullTextBelow = 400
)

// rssTickResult reports one subscription's tick outcome.
type rssTickResult struct {
	SubscriptionID string `json:"subscription_id"`
	Fetched        int    `json:"fetched"` // items parsed from the feed
	Imported       int    `json:"imported"`
	Skipped        int    `json:"skipped"` // already-imported or too short
	FullText       int    `json:"full_text,omitempty"`
	NotModified    bool   `json:"not_modified"`
	Error          string `json:"error,omitempty"`
}

// rssSubscriptionFetcher is the seam tests replace to avoid live HTTP.
type rssSubscriptionFetcher func(ctx context.Context, rawURL string, opts rss.FetchOptions) (*rss.Feed, *rss.FetchResult, error)

// rssArticleFetcher imports an original article body for a summary-only item
// (the URLImporter pipeline). Tests replace it.
type rssArticleFetcher func(ctx context.Context, userID, url, title string) (docID, docTitle string, err error)

// ingestRSSSubscription fetches one subscription and imports its new items
// as materials (source_type "rss") into the subscription's target folder.
// Never returns a fatal error: per-subscription failures are recorded on the
// row (fail_count backoff) so one bad feed cannot stall the tick.
func (s *Server) ingestRSSSubscription(ctx context.Context, sub *database.RSSSubscription, fetch rssSubscriptionFetcher, budget int) rssTickResult {
	result := rssTickResult{SubscriptionID: sub.ID}
	if s.kbMgr == nil || !s.kbMgr.IsConfigured() {
		result.Error = "knowledge base not configured"
		return result
	}
	if budget < 1 {
		return result
	}

	feed, fetchResult, err := fetch(ctx, sub.FeedURL, rss.FetchOptions{
		ETag:         sub.ETag,
		LastModified: sub.LastModified,
	})
	if err != nil {
		result.Error = err.Error()
		if markErr := s.adminRepo.MarkRSSFetchFailure(ctx, sub.ID, err); markErr != nil {
			slog.Warn("rss: failed to record fetch failure", "sub", sub.ID, "error", markErr)
		}
		return result
	}
	if fetchResult.NotModified {
		result.NotModified = true
		if markErr := s.adminRepo.MarkRSSFetchSuccess(ctx, sub.ID, fetchResult.ETag, fetchResult.LastModified, nil); markErr != nil {
			slog.Warn("rss: failed to record not-modified tick", "sub", sub.ID, "error", markErr)
		}
		return result
	}
	result.Fetched = len(feed.Items)

	// Feed metadata learned on first successful fetch (title/site backfill).
	s.backfillRSSSubscriptionMeta(ctx, sub, feed)

	var lastItemAt *time.Time
	imported := 0
	for _, item := range feed.Items {
		if imported >= budget {
			break
		}
		select {
		case <-ctx.Done():
			result.Error = ctx.Err().Error()
			return result
		default:
		}

		content := rss.StripHTML(item.Content)
		link := resolveItemLink(sub.FeedURL, item.Link)
		if link == "" {
			result.Skipped++
			continue
		}

		// Dedup: feed GUID/link, then the knowledge_base source_url backstop.
		if exists, err := s.adminRepo.HasMaterialSourceURL(ctx, sub.UserID, link); err != nil {
			slog.Warn("rss: dedup lookup failed, skipping item", "sub", sub.ID, "link", link, "error", err)
			result.Skipped++
			continue
		} else if exists {
			result.Skipped++
			continue
		}

		// Summary-only feeds: opt-in full-text fetch of the original article
		// (heavier than the feed body — per-item HTTP + extraction + chunks).
		if sub.FetchFullText && len([]rune(content)) < rssFullTextBelow {
			docID, title, fetchErr := s.fetchRSSArticleBody(ctx, sub, link, item.Title)
			if fetchErr != nil {
				slog.Debug("rss: full-text fetch failed, degrading to summary",
					"sub", sub.ID, "link", link, "error", fetchErr)
			} else {
				if err := s.importRSSFetchedItem(ctx, sub, item, docID, title, link); err != nil {
					if !errors.Is(err, errRSSDuplicateContent) {
						slog.Warn("rss: full-text item import failed", "sub", sub.ID, "link", link, "error", err)
						continue
					}
					result.Skipped++
					continue
				}
				imported++
				result.FullText++
				if item.PublishedAt.After(time.Time{}) {
					t := item.PublishedAt
					lastItemAt = &t
				}
				continue
			}
		}

		if len([]rune(content)) < rssMinItemRunes {
			result.Skipped++
			continue
		}

		if err := s.importRSSItem(ctx, sub, item, content, link); err != nil {
			if errors.Is(err, errRSSDuplicateContent) {
				result.Skipped++
				continue
			}
			slog.Warn("rss: item import failed", "sub", sub.ID, "link", link, "error", err)
			continue
		}
		imported++
		if item.PublishedAt.After(time.Time{}) {
			t := item.PublishedAt
			lastItemAt = &t
		}
	}
	result.Imported = imported

	if markErr := s.adminRepo.MarkRSSFetchSuccess(ctx, sub.ID, fetchResult.ETag, fetchResult.LastModified, lastItemAt); markErr != nil {
		slog.Warn("rss: failed to record fetch success", "sub", sub.ID, "error", markErr)
	}
	return result
}

// resolveItemLink makes item links absolute against the feed URL — feeds in
// the wild routinely emit relative paths.
func resolveItemLink(feedURL, link string) string {
	link = strings.TrimSpace(link)
	if link == "" {
		return ""
	}
	base, err := neturl.Parse(strings.TrimSpace(feedURL))
	if err != nil {
		return link
	}
	ref, err := neturl.Parse(link)
	if err != nil {
		return link
	}
	return base.ResolveReference(ref).String()
}

// fetchRSSArticleBody fetches the original article for a summary-only feed
// item through the URLImporter pipeline (fetch → extract → chunk → embed).
func (s *Server) fetchRSSArticleBody(ctx context.Context, sub *database.RSSSubscription, link, itemTitle string) (docID, docTitle string, err error) {
	if s.articleFetcher != nil {
		return s.articleFetcher(ctx, sub.UserID, link, itemTitle)
	}
	importer := services.NewURLImporter(s.kbMgr, services.DefaultChunkConfig())
	docID, err = importer.ImportURLToKB(ctx, sub.UserID, "", link, itemTitle)
	if err != nil {
		return "", "", err
	}
	doc, err := s.kbMgr.GetDocument(ctx, sub.UserID, docID)
	if err != nil {
		return docID, itemTitle, nil // document exists; title fallback is fine
	}
	return docID, doc.Title, nil
}

// importRSSFetchedItem files an article that was fetched by the URLImporter
// (full-text path) as an RSS material in the subscription's folder.
func (s *Server) importRSSFetchedItem(ctx context.Context, sub *database.RSSSubscription, item rss.Item, docID, docTitle, link string) error {
	title := strings.TrimSpace(docTitle)
	if title == "" {
		title = strings.TrimSpace(item.Title)
	}
	if title == "" {
		title = link
	}

	doc, err := s.kbMgr.GetDocument(ctx, sub.UserID, docID)
	if err != nil {
		return fmt.Errorf("read fetched document: %w", err)
	}
	preview := doc.ContentPreview
	if preview == "" && len(doc.Content) > 0 {
		preview = doc.Content
	}
	if len([]rune(preview)) > 500 {
		preview = string([]rune(preview)[:500])
	}
	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         sub.UserID,
		Title:          title,
		ContentPreview: preview,
		SourceType:     "rss",
		SourceURL:      link,
		DocID:          docID,
		ChunkCount:     doc.ChunkCount,
		FolderID:       sub.TargetFolderID,
		Status:         "active",
	}
	if err := s.kbMgr.SaveMaterial(ctx, mat); err != nil {
		if strings.Contains(err.Error(), "duplicate key") || strings.Contains(err.Error(), "unique") {
			return errRSSDuplicateContent
		}
		return fmt.Errorf("save material: %w", err)
	}
	return nil
}

// backfillRSSSubscriptionMeta fills title/site on the first fetch.
func (s *Server) backfillRSSSubscriptionMeta(ctx context.Context, sub *database.RSSSubscription, feed *rss.Feed) {
	title := strings.TrimSpace(feed.Title)
	site := strings.TrimSpace(feed.SiteURL)
	if (title == "" || title == sub.Title) && (site == "" || site == sub.SiteURL) {
		return
	}
	next := *sub
	if title != "" {
		next.Title = title
	}
	if site != "" {
		next.SiteURL = site
	}
	if _, err := s.adminRepo.UpdateRSSSubscription(ctx, sub.UserID, sub.ID, &next); err != nil {
		slog.Debug("rss: meta backfill skipped", "sub", sub.ID, "error", err)
	}
}

// importRSSItem writes one feed item through the standard material pipeline:
// knowledge_base document → chunks (with synchronous embedding) →
// user_materials metadata row filed into the subscription's folder.
func (s *Server) importRSSItem(ctx context.Context, sub *database.RSSSubscription, item rss.Item, content, link string) error {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = link
	}

	doc, err := s.kbMgr.AddDocument(ctx, sub.UserID, title, content, "rss", map[string]interface{}{
		"source":      "rss",
		"source_url":  link,
		"feed_url":    sub.FeedURL,
		"item_guid":   item.GUID,
		"imported_at": time.Now().Format(time.RFC3339),
	})
	if err != nil {
		if strings.Contains(err.Error(), "uk_kb_content_hash") || strings.Contains(err.Error(), "duplicate key") {
			return errRSSDuplicateContent
		}
		return fmt.Errorf("add document: %w", err)
	}

	chunks := services.ChunkText(content, services.DefaultChunkConfig())
	for _, chunk := range chunks {
		if _, chunkErr := s.kbMgr.AddChunk(ctx, doc.ID, sub.UserID, chunk.Index, chunk.Title, chunk.Content, map[string]interface{}{
			"source_url": link,
		}); chunkErr != nil {
			slog.Warn("rss: chunk add failed", "doc", doc.ID, "index", chunk.Index, "error", chunkErr)
		}
	}
	if err := s.kbMgr.UpdateChunkCount(ctx, doc.ID, len(chunks)); err != nil {
		slog.Warn("rss: chunk count update failed", "doc", doc.ID, "error", err)
	}

	preview := content
	if len([]rune(preview)) > 500 {
		preview = string([]rune(preview)[:500])
	}
	mat := &services.UserMaterial{
		ID:             uuid.NewString(),
		UserID:         sub.UserID,
		Title:          title,
		ContentPreview: preview,
		SourceType:     "rss",
		SourceURL:      link,
		DocID:          doc.ID,
		ChunkCount:     len(chunks),
		FolderID:       sub.TargetFolderID,
		Status:         "active",
	}
	if err := s.kbMgr.SaveMaterial(ctx, mat); err != nil {
		return fmt.Errorf("save material: %w", err)
	}
	return nil
}

// cronRSSFetch is the global rss_fetch cron task: it walks due
// subscriptions under a global item budget. Per-subscription limits come
// from each row (max_items_per_tick).
func (s *Server) cronRSSFetch(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: rss_fetch triggered", "job", job.Name)
	if s.adminRepo == nil {
		return fmt.Errorf("database not available")
	}

	// How many subscriptions to consider this tick. Due-order is
	// last_fetched_at NULLS FIRST, so starved feeds are served first.
	maxSubs := rssGlobalTickBudget
	if v, ok := job.TaskConfig["max_subscriptions"].(float64); ok && v > 0 {
		maxSubs = int(v)
	}
	subs, err := s.adminRepo.ListDueRSSSubscriptions(ctx, maxSubs)
	if err != nil {
		return fmt.Errorf("list due subscriptions: %w", err)
	}
	if len(subs) == 0 {
		slog.Info("cron: rss_fetch — no due subscriptions")
		return nil
	}

	imported, failed := 0, 0
	for _, sub := range subs {
		if imported >= rssGlobalTickBudget {
			slog.Info("cron: rss_fetch — global budget reached, deferring the rest", "imported", imported)
			break
		}
		budget := sub.MaxItemsPerTick
		if budget < 1 || budget > rssFeedTickBudget {
			budget = rssFeedTickBudget
		}
		if remaining := rssGlobalTickBudget - imported; budget > remaining {
			budget = remaining
		}
		result := s.ingestRSSSubscription(ctx, sub, rss.FetchFeed, budget)
		imported += result.Imported
		if result.Error != "" {
			failed++
			slog.Warn("cron: rss_fetch — subscription failed", "sub", sub.ID, "error", result.Error)
		}
	}
	slog.Info("cron: rss_fetch completed", "subscriptions", len(subs), "imported", imported, "failed", failed)
	return nil
}

// EnsureRSSFetchCronJob idempotently creates the global rss_fetch job so
// scheduled ingestion works out of the box (admins can still edit/disable it
// from the cron page).
func (s *Server) EnsureRSSFetchCronJob(ctx context.Context) error {
	if s.adminRepo == nil {
		return nil
	}
	jobs, err := s.adminRepo.ListCronJobs(ctx)
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.TaskType == "rss_fetch" {
			return nil
		}
	}
	schedule := "*/15 * * * *"
	_, err = s.adminRepo.CreateCronJob(ctx, &database.CronJob{
		Name:        "RSS 订阅抓取",
		Description: "周期性拉取用户订阅的 RSS/Atom 源，新条目自动入库为素材（限量：全局 20 条/次，每源默认 3 条）",
		Schedule:    schedule,
		TaskType:    "rss_fetch",
		TaskConfig:  map[string]interface{}{"max_subscriptions": rssGlobalTickBudget},
		IsActive:    true,
	})
	if err != nil {
		return fmt.Errorf("seed rss_fetch cron job: %w", err)
	}
	slog.Info("rss: seeded rss_fetch cron job", "schedule", schedule)
	return nil
}
