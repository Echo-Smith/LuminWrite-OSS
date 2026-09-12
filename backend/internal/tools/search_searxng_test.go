package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearXNGSearchMapsResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/search") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.URL.Query().Get("format") != "json" {
			t.Error("SearXNG JSON API requires format=json")
		}
		if r.URL.Query().Get("q") == "" {
			t.Error("query must be forwarded")
		}
		w.Write([]byte(`{
			"results": [
				{"title": "结果一", "url": "https://a.example/1", "content": "摘要一", "engine": "bing", "score": 3.2},
				{"title": "", "url": "https://skip.example", "content": "no title"},
				{"title": "结果二", "url": "https://b.example/2", "content": "摘要二", "engine": "duckduckgo", "score": 1.4}
			],
			"answers": ["直接答案"]
		}`))
	}))
	defer server.Close()

	client := NewSearXNGClient(server.URL, 5*time.Second)
	results, err := client.Search(context.Background(), "测试查询", 5)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 mapped results, got %d", len(results))
	}
	if results[0].Title != "结果一" || results[0].URL != "https://a.example/1" {
		t.Fatalf("first result wrong: %+v", results[0])
	}
	if results[0].Source != "searxng:bing" {
		t.Fatalf("source must record the upstream engine: %q", results[0].Source)
	}
	if results[1].Source != "searxng:duckduckgo" {
		t.Fatalf("second source wrong: %q", results[1].Source)
	}
}

func TestSearXNGMaxResultsRespected(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"results": [` +
			`{"title": "一", "url": "https://a.example/1", "content": "x"},` +
			`{"title": "二", "url": "https://a.example/2", "content": "x"},` +
			`{"title": "三", "url": "https://a.example/3", "content": "x"}]}`))
	}))
	defer server.Close()

	client := NewSearXNGClient(server.URL, 5*time.Second)
	results, err := client.Search(context.Background(), "查询", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("maxResults must cap the output, got %d", len(results))
	}
	if results[0].Source != "searxng" {
		t.Fatalf("engine-less results fall back to plain searxng source: %q", results[0].Source)
	}
}

func TestSearXNGForbiddenHintsJSONFormat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`<html>Forbidden</html>`))
	}))
	defer server.Close()

	client := NewSearXNGClient(server.URL, 5*time.Second)
	_, err := client.Search(context.Background(), "查询", 5)
	if err == nil || !strings.Contains(err.Error(), "json format") {
		t.Fatalf("403 must hint at enabling the json format, got %v", err)
	}
}

func TestSearXNGRequiresBaseURL(t *testing.T) {
	client := NewSearXNGClient("", 5*time.Second)
	if _, err := client.Search(context.Background(), "查询", 5); err == nil {
		t.Fatal("empty base URL must be an error, not silence")
	}
}
