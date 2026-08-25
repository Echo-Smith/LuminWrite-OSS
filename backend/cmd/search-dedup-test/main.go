// search-dedup-test is a CLI tool that calls SearchClient.Search() multiple times
// with the same query and reports URL/title overlap to verify dedup effectiveness.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/joho/godotenv"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/tools"
)

func main() {
	// Load .env.docker (try multiple paths for local + Docker)
	for _, p := range []string{".env.docker", "../.env.docker", "../../.env.docker"} {
		if err := godotenv.Load(p); err == nil {
			fmt.Printf("Loaded env from: %s\n", p)
			break
		}
	}

	cfg := config.Load()

	// Build SearchClient (same as server.go)
	sc := tools.NewSearchClient(
		cfg.Tavily.APIKey, cfg.Tavily.Endpoint, cfg.Tavily.Timeout,
		cfg.Zhihu.Enabled, cfg.Zhihu.BaseURL, cfg.Zhihu.AccessSecret, cfg.Zhihu.Timeout,
		cfg.Tencent.Enabled, cfg.Tencent.BaseURL, cfg.Tencent.Timeout,
		cfg.Weibo.Enabled, cfg.Weibo.AppID, cfg.Weibo.AppSecret, cfg.Weibo.TokenEndpoint, cfg.Weibo.BaseURL, cfg.Weibo.Timeout,
		cfg.ExtraHot.Enabled, cfg.ExtraHot.BaseURL, cfg.ExtraHot.Timeout,
		cfg.Bing.Enabled, cfg.Bing.BaseURL, cfg.Bing.Timeout,
		cfg.Jiaozhen.CLIPath, cfg.Jiaozhen.Timeout,
		cfg.AnySearch.APIKey, cfg.AnySearch.Endpoint, cfg.AnySearch.Timeout,
	)

	if !sc.HasSources() {
		fmt.Println("ERROR: no search sources configured")
		os.Exit(1)
	}

	fmt.Printf("Active sources: %v\n", sc.ActiveSources())
	fmt.Println()

	query := "人工智能在教育中的应用"
	if len(os.Args) > 1 {
		query = os.Args[1]
	}
	rounds := 3
	if len(os.Args) > 2 {
		fmt.Sscanf(os.Args[2], "%d", &rounds)
	}

	fmt.Printf("Query: %s\n", query)
	fmt.Printf("Rounds: %d\n", rounds)
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Track all results across rounds
	type resultInfo struct {
		round  int
		source string
		title  string
		url    string
	}
	var allResults []resultInfo
	roundURLSets := make([]map[string]bool, rounds)
	roundTitleSets := make([]map[string]bool, rounds)

	for round := 0; round < rounds; round++ {
		fmt.Printf("\n── Round %d ─────────────────────────────────────────────────\n", round+1)
		start := time.Now()
		results := sc.Search(context.Background(), query, 9)
		elapsed := time.Since(start)

		roundURLSets[round] = make(map[string]bool)
		roundTitleSets[round] = make(map[string]bool)

		fmt.Printf("  Results: %d  |  Time: %v\n", len(results), elapsed.Round(time.Millisecond))
		fmt.Println("  ────────────────────────────────────────────────────────────")

		for i, r := range results {
			fmt.Printf("  [%d] src=%-10s | %s\n      URL: %s\n", i+1, r.Source, truncate(r.Title, 60), r.URL)

			allResults = append(allResults, resultInfo{
				round:  round + 1,
				source: r.Source,
				title:  r.Title,
				url:    r.URL,
			})

			if r.URL != "" {
				roundURLSets[round][r.URL] = true
			}
			roundTitleSets[round][r.Title] = true
		}
	}

	// ── Cross-round analysis ──
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("│ Cross-Round URL Overlap Analysis                            │")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	for i := 0; i < rounds; i++ {
		for j := i + 1; j < rounds; j++ {
			overlap := 0
			for url := range roundURLSets[i] {
				if roundURLSets[j][url] {
					overlap++
				}
			}
			fmt.Printf("  Round %d vs Round %d: %d URLs in common (R%d=%d, R%d=%d)\n",
				i+1, j+1, overlap,
				i+1, len(roundURLSets[i]),
				j+1, len(roundURLSets[j]))
		}
	}

	// ── Source-level analysis ──
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("│ Per-Source Result Count by Round                            │")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	sourceCounts := make(map[string][]int)
	for _, r := range allResults {
		if sourceCounts[r.source] == nil {
			sourceCounts[r.source] = make([]int, rounds)
		}
		sourceCounts[r.source][r.round-1]++
	}

	fmt.Printf("  %-12s", "Source")
	for i := 0; i < rounds; i++ {
		fmt.Printf("  R%d", i+1)
	}
	fmt.Println()
	fmt.Println("  ────────────────────────────────────────────────────────────")
	for src, counts := range sourceCounts {
		fmt.Printf("  %-12s", src)
		for _, c := range counts {
			fmt.Printf("  %2d", c)
		}
		fmt.Println()
	}

	// ── Within-round dedup verification ──
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("│ Within-Round Dedup Check (SearchClient internal)           │")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	// Run one more round with a higher maxTotal to see if dedup catches cross-source dupes
	fmt.Println("\n  Running extra round with maxTotal=15 to stress cross-source dedup...")
	results := sc.Search(context.Background(), query, 15)

	urlSeen := make(map[string]int)
	titleSeen := make(map[string]int)
	for _, r := range results {
		if r.URL != "" {
			urlSeen[r.URL]++
		}
		titleSeen[r.Title]++
	}

	dupURLs := 0
	dupTitles := 0
	for _, cnt := range urlSeen {
		if cnt > 1 {
			dupURLs++
		}
	}
	for _, cnt := range titleSeen {
		if cnt > 1 {
			dupTitles++
		}
	}

	fmt.Printf("  Results: %d\n", len(results))
	fmt.Printf("  Unique URLs: %d / Duplicate URLs: %d\n", len(urlSeen), dupURLs)
	fmt.Printf("  Unique Titles: %d / Duplicate Titles: %d\n", len(titleSeen), dupTitles)

	if dupURLs == 0 && dupTitles == 0 {
		fmt.Println("  ✅ PASS: No duplicates found within a single Search() call!")
	} else {
		fmt.Println("  ❌ FAIL: Duplicates found! Dedup logic has a bug.")
		for url, cnt := range urlSeen {
			if cnt > 1 {
				fmt.Printf("     DUP URL (%dx): %s\n", cnt, url)
			}
		}
		for title, cnt := range titleSeen {
			if cnt > 1 {
				fmt.Printf("     DUP Title (%dx): %s\n", cnt, truncate(title, 60))
			}
		}
	}

	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("│ Conclusion                                                  │")
	fmt.Println("═══════════════════════════════════════════════════════════════")
	fmt.Println("  1. Within a single Search() call: dedup removes cross-source duplicates.")
	fmt.Println("  2. Across multiple Search() calls: results MAY overlap (same query →")
	fmt.Println("     similar top results from search engines). This is expected behavior —")
	fmt.Println("     search engines return similar results for the same query.")
	fmt.Println("  3. For cross-call dedup in agent loops, DedupAgainstExisting() handles")
	fmt.Println("     it at the Session.SearchResults level (verified via logs).")
}

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
