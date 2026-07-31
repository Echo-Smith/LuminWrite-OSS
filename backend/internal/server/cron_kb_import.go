package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
)

// ─── KB Auto Import Cron Job ───────────────────────────
// Periodically scrapes a column page for new articles and imports them
// into the knowledge base. Designed for daily auto-import of new content.
//
// Task config:
//   - url:         column page URL (default: https://www.hangzhou.com.cn/pinglun/node_152931.htm)
//   - kb_id:       target knowledge base ID (default: "yinyue")
//   - max_pages:   max pages to scrape (default: 1 — only first page for daily new articles)
//   - source_type: "url" (use URLImporter) or "text" (fetch + import text)

func (s *Server) cronKbAutoImport(ctx context.Context, job *database.CronJob) error {
	slog.Info("cron: kb_auto_import triggered", "job", job.Name)

	if s.kbMgr == nil || !s.kbMgr.IsConfigured() {
		return fmt.Errorf("knowledge base not configured")
	}

	// Parse config
	cfg := job.TaskConfig
	colURL := "https://www.hangzhou.com.cn/pinglun/node_152931.htm"
	kbID := "yinyue"
	maxPages := 1

	if v, ok := cfg["url"].(string); ok && v != "" {
		colURL = v
	}
	if v, ok := cfg["kb_id"].(string); ok && v != "" {
		kbID = v
	}
	if v, ok := cfg["max_pages"].(float64); ok && v > 0 {
		maxPages = int(v)
	}

	// Step 1: Scrape article URLs from column pages
	articles, err := scrapeColumnPage(colURL, maxPages)
	if err != nil {
		return fmt.Errorf("failed to scrape column: %w", err)
	}
	slog.Info("cron: kb_auto_import — articles found", "count", len(articles))

	// Step 2: Filter out already-imported articles
	newArticles, err := s.filterImportedArticles(ctx, articles, kbID)
	if err != nil {
		slog.Warn("cron: kb_auto_import — failed to filter existing, importing all", "error", err)
		newArticles = articles
	}
	slog.Info("cron: kb_auto_import — new articles to import", "count", len(newArticles))

	if len(newArticles) == 0 {
		slog.Info("cron: kb_auto_import — no new articles, done", "job", job.Name)
		return nil
	}

	// Step 3: Import each article
	importer := services.NewURLImporter(s.kbMgr, services.ChunkConfig{
		ChunkSize: 512,
		Overlap:   50,
	})

	imported := 0
	for _, a := range newArticles {
		select {
		case <-ctx.Done():
			slog.Warn("cron: kb_auto_import — context cancelled", "imported", imported)
			return ctx.Err()
		default:
		}

		_, err := importer.ImportURLToKB(ctx, "", kbID, a.URL, a.Title)
		if err != nil {
			slog.Warn("cron: kb_auto_import — import failed",
				"url", a.URL, "title", a.Title, "error", err)
		} else {
			imported++
			slog.Info("cron: kb_auto_import — imported",
				"title", a.Title, "url", a.URL)
		}

		// Rate limit
		time.Sleep(500 * time.Millisecond)
	}

	slog.Info("cron: kb_auto_import completed",
		"job", job.Name, "found", len(articles), "imported", imported)
	return nil
}

// ─── Article extraction ────────────────────────────────

type columnArticle struct {
	Title string
	URL   string
	Date  string
}

// scrapeColumnPage fetches column pages and extracts article URLs.
func scrapeColumnPage(baseURL string, maxPages int) ([]columnArticle, error) {
	var articles []columnArticle
	seen := make(map[string]bool)

	client := &http.Client{Timeout: 30 * time.Second}

	for page := 1; page <= maxPages; page++ {
		var url string
		if page == 1 {
			url = baseURL
		} else {
			// Replace .htm with _N.htm for pagination
			url = strings.Replace(baseURL, ".htm", fmt.Sprintf("_%d.htm", page), 1)
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36")

		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("cron: scrape — fetch failed", "url", url, "error", err)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
		resp.Body.Close()
		if err != nil {
			continue
		}

		html := string(body)
		pageArticles := extractArticleLinks(html)

		for _, a := range pageArticles {
			if !seen[a.URL] {
				seen[a.URL] = true
				articles = append(articles, a)
			}
		}
	}

	return articles, nil
}

// extractArticleLinks extracts article links and titles from HTML.
func extractArticleLinks(html string) []columnArticle {
	var articles []columnArticle

	// Match <a href="...content_XXX.htm" ...>Title</a>
	linkRe := regexp.MustCompile(`<a\s+href="([^"]*content_\d+\.htm)"[^>]*>\s*([^<]+)</a>`)

	links := linkRe.FindAllStringSubmatch(html, -1)

	seen := make(map[string]bool)
	for i, m := range links {
		url := strings.TrimSpace(m[1])
		title := strings.TrimSpace(m[2])
		if seen[url] {
			continue
		}
		seen[url] = true

		// Extract date from surrounding text — look for a date pattern
		// near this link in the original HTML
		date := ""
		// Find the position of this link in the HTML and look for a date nearby
		linkPos := strings.Index(html, m[0])
		if linkPos >= 0 {
			// Search in a 500-char window after the link for a date
			searchArea := html[linkPos:]
			if len(searchArea) > 500 {
				searchArea = searchArea[:500]
			}
			dateRe := regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
			if dm := dateRe.FindStringSubmatch(searchArea); dm != nil {
				date = dm[1]
			}
		}

		articles = append(articles, columnArticle{
			Title: title,
			URL:   url,
			Date:  date,
		})
	}

	return articles
}

// filterImportedArticles checks the KB for documents with matching source_url
// and returns only articles that haven't been imported yet.
func (s *Server) filterImportedArticles(ctx context.Context, articles []columnArticle, kbID string) ([]columnArticle, error) {
	if s.adminRepo == nil || s.adminRepo.DB() == nil {
		return articles, nil
	}

	// Query existing source_urls from knowledge_documents in the specified KB
	rows, err := s.adminRepo.DB().DB.QueryContext(ctx, `
		SELECT metadata->>'source_url' AS url
		FROM knowledge_base
		WHERE metadata->>'source_url' IS NOT NULL
		  AND COALESCE(kb_id, 'default') = $1
	`, kbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	existing := make(map[string]bool)
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err == nil && url != "" {
			existing[url] = true
		}
	}

	var filtered []columnArticle
	for _, a := range articles {
		if !existing[a.URL] {
			filtered = append(filtered, a)
		}
	}

	return filtered, nil
}
