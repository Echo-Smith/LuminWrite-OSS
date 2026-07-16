package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ─── Extra Hot Search Sources (Direct APIs) ──────────────

// ExtraHotClient fetches hot topics from multiple platforms using their direct APIs.
// Supported platforms: baidu, bilibili.
type ExtraHotClient struct {
	timeout time.Duration
	client  *http.Client
}

// NewExtraHotClient creates a new ExtraHotClient.
func NewExtraHotClient(_ string, timeout time.Duration) *ExtraHotClient {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &ExtraHotClient{
		timeout: timeout,
		client:  &http.Client{Timeout: timeout},
	}
}

// FetchHotTopics fetches hot topics from all configured extra platforms concurrently.
func (c *ExtraHotClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	if limit <= 0 {
		limit = 20
	}

	var (
		mu      sync.Mutex
		results []map[string]interface{}
		wg      sync.WaitGroup
	)

	// Baidu
	wg.Add(1)
	go func() {
		defer wg.Done()
		topics, err := c.fetchBaiduHot(ctx, limit)
		if err != nil {
			slog.Debug("baidu hot topics fetch failed", "error", err)
			return
		}
		mu.Lock()
		results = append(results, topics...)
		mu.Unlock()
	}()

	// Bilibili
	wg.Add(1)
	go func() {
		defer wg.Done()
		topics, err := c.fetchBilibiliHot(ctx, limit)
		if err != nil {
			slog.Debug("bilibili hot topics fetch failed", "error", err)
			return
		}
		mu.Lock()
		results = append(results, topics...)
		mu.Unlock()
	}()

	wg.Wait()
	return results, nil
}

// fetchBaiduHot fetches Baidu hot search list from the official Baidu Top API.
func (c *ExtraHotClient) fetchBaiduHot(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	url := "https://top.baidu.com/api/board?platform=wise&tab=realtime"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://top.baidu.com")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("baidu hot API returned status %d", resp.StatusCode)
	}

	return parseBaiduHot(body, limit), nil
}

// parseBaiduHot parses the Baidu Top API response.
// Response structure: {"success": true, "data": {"cards": [{"content": [{"content": [{"word": "...", "url": "...", "hotScore": "..."}]}]}]}}
func parseBaiduHot(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Success bool `json:"success"`
		Data    struct {
			Cards []struct {
				Content []struct {
					Content []struct {
						Word     string `json:"word"`
						URL      string `json:"url"`
						HotScore string `json:"hotScore"`
						Desc     string `json:"desc"`
					} `json:"content"`
				} `json:"content"`
			} `json:"cards"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if !data.Success || len(data.Data.Cards) == 0 {
		return nil
	}

	topics := make([]map[string]interface{}, 0, limit)
	rank := 0
	for _, card := range data.Data.Cards {
		for _, outerContent := range card.Content {
			for _, item := range outerContent.Content {
				if rank >= limit {
					break
				}
				if item.Word == "" {
					continue
				}
				rank++
				topics = append(topics, map[string]interface{}{
					"title":       cleanHTML(item.Word),
					"description": item.HotScore,
					"source":      "baidu",
					"platform":    "baidu",
					"hot_rank":    rank,
					"hot_count":   0,
					"url":         item.URL,
				})
			}
		}
	}
	return topics
}

// fetchBilibiliHot fetches Bilibili hot search list from the official Bilibili API.
func (c *ExtraHotClient) fetchBilibiliHot(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	url := fmt.Sprintf("https://api.bilibili.com/x/web-interface/search/square?limit=%d", limit)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Referer", "https://www.bilibili.com")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bilibili hot API returned status %d", resp.StatusCode)
	}

	return parseBilibiliHot(body, limit), nil
}

// parseBilibiliHot parses the Bilibili hot search API response.
// Response structure: {"code": 0, "data": {"trending": {"list": [{"keyword": "...", "heat_score": 123}]}}}
func parseBilibiliHot(body []byte, limit int) []map[string]interface{} {
	var data struct {
		Code int `json:"code"`
		Data struct {
			Trending struct {
				List []struct {
					Keyword   string `json:"keyword"`
					ShowName  string `json:"show_name"`
					HeatScore int64  `json:"heat_score"`
					URI       string `json:"uri"`
				} `json:"list"`
			} `json:"trending"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	if data.Code != 0 || len(data.Data.Trending.List) == 0 {
		return nil
	}

	topics := make([]map[string]interface{}, 0, len(data.Data.Trending.List))
	for i, item := range data.Data.Trending.List {
		if i >= limit {
			break
		}
		title := item.Keyword
		if title == "" {
			title = item.ShowName
		}
		if title == "" {
			continue
		}
		topics = append(topics, map[string]interface{}{
			"title":       cleanHTML(title),
			"description": fmt.Sprintf("%d", item.HeatScore),
			"source":      "bilibili",
			"platform":    "bilibili",
			"hot_rank":    i + 1,
			"hot_count":   item.HeatScore,
			"url":         item.URI,
		})
	}
	return topics
}

// Suppress unused import warning for strings (used by cleanHTML in other files)
var _ = strings.TrimSpace
