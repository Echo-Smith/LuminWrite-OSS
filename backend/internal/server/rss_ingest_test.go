package server

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services/rss"
)

// fakeFeedFetcher serves scripted feeds without live HTTP.
type fakeFeedFetcher struct {
	feed   *rss.Feed
	result *rss.FetchResult
	err    error
	calls  int
}

func (f *fakeFeedFetcher) fetch(_ context.Context, _ string, _ rss.FetchOptions) (*rss.Feed, *rss.FetchResult, error) {
	f.calls++
	if f.err != nil {
		return nil, nil, f.err
	}
	return f.feed, f.result, nil
}

func longItem(link string) rss.Item {
	return rss.Item{
		GUID:        "guid-" + link,
		Title:       "条目 " + link,
		Link:        link,
		Content:     "<p>这是 " + link + " 的测试正文内容，用于通过最小长度检查并写入知识库成为可检索的素材条目，其中包含多个句号与细节描述以确保长度超过阈值。</p>",
		PublishedAt: time.Now(),
	}
}

// TestIngestRSSSubscription covers dedup, per-tick budget, folder targeting
// and fetch bookkeeping against the real KB pipeline.
func TestIngestRSSSubscription(t *testing.T) {
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 4, 2)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()

	var userID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (uid, name, role) VALUES ('rss-ingest', 'rss-ingest', 'user') RETURNING id::text`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	var folderID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO material_folders (user_id, name) VALUES ($1::uuid, '订阅文件夹') RETURNING id::text`,
		userID).Scan(&folderID); err != nil {
		t.Fatal(err)
	}

	encKey := []byte("0123456789abcdef0123456789abcdef")
	adminRepo := database.NewAdminRepo(db).WithEncryptionKey(encKey)
	// nil embedding client: AddChunk degrades to BM25-only chunks (the
	// documented degradation path), keeping the test hermetic.
	kbMgr := services.NewKbManager(db.DB, nil)

	s := &Server{db: db, adminRepo: adminRepo, kbMgr: kbMgr}

	sub, err := adminRepo.CreateRSSSubscription(ctx, &database.RSSSubscription{
		UserID: userID, FeedURL: "https://example.com/feed", TargetFolderID: folderID,
		MaxItemsPerTick: 2, IsActive: true,
	})
	if err != nil {
		t.Fatalf("create subscription: %v", err)
	}

	// Tick 1: 5 items (one byte-identical to another), budget 2 → the first
	// two imported; the duplicate article is never reached under this budget.
	dup := longItem("/a")
	dup.Link, dup.GUID = "/dup", "guid-/dup"
	fetcher := &fakeFeedFetcher{
		feed:   &rss.Feed{Title: "测试源", SiteURL: "https://example.com", Items: []rss.Item{longItem("/a"), dup, longItem("/c"), longItem("/d")}},
		result: &rss.FetchResult{ETag: `"v1"`},
	}
	result := s.ingestRSSSubscription(ctx, sub, fetcher.fetch, 2)
	if result.Error != "" {
		t.Fatalf("tick 1: %v", result.Error)
	}
	if result.Imported != 2 || result.Fetched != 4 {
		t.Fatalf("tick 1: imported=%d fetched=%d, want 2/4", result.Imported, result.Fetched)
	}
	// budget=2 时 /dup（与 /a 内容相同）还没轮到；它由 tick 3 的撞键路径覆盖

	// Materials exist, filed into the target folder, source_type=rss.
	var count, rssCount int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*), COUNT(*) FILTER (WHERE source_type='rss') FROM user_materials WHERE user_id = $1 AND folder_id = $2`,
		userID, folderID).Scan(&count, &rssCount); err != nil {
		t.Fatal(err)
	}
	if count != 2 || rssCount != 2 {
		t.Fatalf("materials in folder: total=%d rss=%d, want 2/2", count, rssCount)
	}

	// Tick 2: /a and /c were already imported in tick 1 → deduped by
	// source_url; /b and /d are new → imported under budget 3.
	fetcher2 := &fakeFeedFetcher{
		feed:   &rss.Feed{Title: "测试源", Items: []rss.Item{longItem("/a"), longItem("/b"), longItem("/c"), longItem("/d")}},
		result: &rss.FetchResult{ETag: `"v2"`},
	}
	result2 := s.ingestRSSSubscription(ctx, sub, fetcher2.fetch, 3)
	if result2.Error != "" {
		t.Fatalf("tick 2: %v", result2.Error)
	}
	if result2.Imported != 2 || result2.Skipped != 2 {
		t.Fatalf("tick 2: imported=%d skipped=%d, want 2/2", result2.Imported, result2.Skipped)
	}

	// Tick 3: one new item beyond the dedup set → imported, budget allows 3.
	fetcher3 := &fakeFeedFetcher{
		feed:   &rss.Feed{Title: "测试源", Items: []rss.Item{longItem("/a"), longItem("/e")}},
		result: &rss.FetchResult{ETag: `"v3"`},
	}
	result3 := s.ingestRSSSubscription(ctx, sub, fetcher3.fetch, 3)
	if result3.Imported != 1 {
		t.Fatalf("tick 3: imported=%d, want 1", result3.Imported)
	}

	// Bookkeeping: success recorded (fail_count reset, etag kept).
	updated, err := adminRepo.GetRSSSubscription(ctx, userID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.FailCount != 0 || updated.ETag != `"v3"` || updated.LastFetchedAt == nil {
		t.Fatalf("bookkeeping not recorded: %+v", updated)
	}

	// A failing fetch records the error and bumps fail_count.
	failing := &fakeFeedFetcher{err: context.DeadlineExceeded}
	failResult := s.ingestRSSSubscription(ctx, sub, failing.fetch, 3)
	if failResult.Error == "" {
		t.Fatal("expected fetch error to be surfaced")
	}
	afterFail, err := adminRepo.GetRSSSubscription(ctx, userID, sub.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterFail.FailCount != 1 || afterFail.LastError == "" {
		t.Fatalf("failure not recorded: %+v", afterFail)
	}
}
