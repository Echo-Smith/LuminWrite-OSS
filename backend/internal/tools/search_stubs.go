package tools

import (
	"context"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

// Search source stubs — implement these to add search sources.
// These are stub implementations for the open-source edition.
// The commercial edition replaces these with full implementations.

type TavilyClient struct{}

func NewTavilyClient(apiKey, endpoint string, timeout time.Duration) *TavilyClient {
	return &TavilyClient{}
}

func (c *TavilyClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

type ZhihuClient struct{}

func NewZhihuClient(baseURL, accessSecret string, timeout time.Duration) *ZhihuClient {
	return &ZhihuClient{}
}

func (c *ZhihuClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

type TencentNewsClient struct{}

func NewTencentNewsClient(baseURL string, timeout time.Duration) *TencentNewsClient {
	return &TencentNewsClient{}
}

func (c *TencentNewsClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

func (c *TencentNewsClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}

type TencentNewsCLIClient struct{}

func NewTencentNewsCLIClient(cliPath string, timeout time.Duration) *TencentNewsCLIClient {
	return &TencentNewsCLIClient{}
}

func (c *TencentNewsCLIClient) IsConfigured() bool { return false }

func (c *TencentNewsCLIClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

func (c *TencentNewsCLIClient) FetchHot(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}

type WeiboClient struct{}

// NewWeiboClient — stub signature matches commercial edition (5 params).
func NewWeiboClient(appID, appSecret, tokenEndpoint, baseURL string, timeout time.Duration) *WeiboClient {
	return &WeiboClient{}
}

func (c *WeiboClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

func (c *WeiboClient) HasOpenAPI() bool { return false }

func (c *WeiboClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}

type ExtraHotClient struct{}

func NewExtraHotClient(_ string, timeout time.Duration) *ExtraHotClient {
	return &ExtraHotClient{}
}

func (c *ExtraHotClient) FetchHotTopics(ctx context.Context, limit int) ([]map[string]interface{}, error) {
	return nil, nil
}

type BingClient struct{}

func NewBingClient(baseURL string, timeout time.Duration) *BingClient {
	return &BingClient{}
}

func (c *BingClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

type AnySearchClient struct{}

func NewAnySearchClient(apiKey, endpoint string, timeout time.Duration) *AnySearchClient {
	return &AnySearchClient{}
}

func (c *AnySearchClient) Search(ctx context.Context, query string, maxResults int) ([]engine.SearchResult, error) {
	return nil, nil
}

// Extract — stub returns empty, real implementation in commercial edition.
func (c *AnySearchClient) Extract(ctx context.Context, targetURL string) (title, content string, err error) {
	return "", "", nil
}

// URLFetcher — stub for URL content fetching.
type URLFetcher struct{}

type URLFetchResult struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	URL     string `json:"url"`
}

func NewURLFetcher() *URLFetcher {
	return &URLFetcher{}
}

func (f *URLFetcher) FetchContent(ctx context.Context, url string) (*URLFetchResult, error) {
	return nil, nil
}

// JiaozhenClient — fact-checking client stub.
type JiaozhenClient struct{}

type JiaozhenResult struct {
	Claim   string `json:"claim"`
	Status  string `json:"status"`
	Source  string `json:"source,omitempty"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

func NewJiaozhenClient(enabled bool, cliPath string, _ []string, apiKey string, timeout time.Duration, maxClaims int) *JiaozhenClient {
	return &JiaozhenClient{}
}

func (c *JiaozhenClient) IsConfigured() bool { return false }

func (c *JiaozhenClient) Reconfigure(apiKey string) {}

func (c *JiaozhenClient) CheckClaim(ctx context.Context, claim string) *JiaozhenResult {
	return &JiaozhenResult{Claim: claim, Status: "skipped"}
}

func (c *JiaozhenClient) CheckClaimDirect(ctx context.Context, claim string) *JiaozhenResult {
	return &JiaozhenResult{Claim: claim, Status: "skipped"}
}

func (c *JiaozhenClient) CheckClaims(ctx context.Context, claims []string) []*JiaozhenResult {
	results := make([]*JiaozhenResult, len(claims))
	for i, claim := range claims {
		results[i] = &JiaozhenResult{Claim: claim, Status: "skipped"}
	}
	return results
}
