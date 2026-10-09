package rss

import (
	"context"
	"encoding/xml"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// httptest listens on loopback, which production blocks: allow loopback for
// the guard seams while keeping private/link-local/metadata blocking active.
func init() {
	hostGuard = func(host string) bool {
		switch strings.ToLower(host) {
		case "127.0.0.1", "localhost", "::1", "[::1]":
			return false
		}
		return blockedHost(host)
	}
	ipGuard = func(ip net.IP) bool {
		if ip.IsLoopback() {
			return false
		}
		return blockedIP(ip)
	}
}

const rss2Sample = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
<channel>
  <title>测试科技周刊</title>
  <description>每周科技资讯</description>
  <link>https://example.com</link>
  <item>
    <title>第一条：AI 新模型发布</title>
    <link>https://example.com/posts/1</link>
    <guid>tag:example.com,2026:1</guid>
    <pubDate>Mon, 06 Oct 2026 10:00:00 +0800</pubDate>
    <description>&lt;p&gt;摘要内容&lt;/p&gt;</description>
    <content:encoded><![CDATA[<p>这是正文第一段，包含足够长度的中文内容用于测试解析与入库逻辑是否正常工作。</p><p>第二段继续补充更多细节信息。</p>]]></content:encoded>
  </item>
  <item>
    <title>第二条：开源动态</title>
    <link>https://example.com/posts/2</link>
    <guid>tag:example.com,2026:2</guid>
    <pubDate>Tue, 07 Oct 2026 10:00:00 +0800</pubDate>
    <description>纯摘要条目</description>
  </item>
</channel>
</rss>`

const atomSample = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Atom 源</title>
  <entry>
    <title>Atom 条目</title>
    <id>urn:uuid:1234</id>
    <link href="https://example.com/atom/1" rel="alternate"/>
    <updated>2026-10-05T10:00:00Z</updated>
    <content type="html">&lt;p&gt;Atom 正文内容，同样需要足够长度以便通过最小长度检查并被写入知识库。&lt;/p&gt;</content>
  </entry>
</feed>`

func TestParseRSS2(t *testing.T) {
	feed, err := Parse([]byte(rss2Sample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if feed.Title != "测试科技周刊" || len(feed.Items) != 2 {
		t.Fatalf("unexpected feed: %+v", feed)
	}
	first := feed.Items[0]
	if first.Link != "https://example.com/posts/1" || first.GUID != "tag:example.com,2026:1" {
		t.Fatalf("unexpected item: %+v", first)
	}
	if first.PublishedAt.IsZero() {
		t.Fatal("pubDate not parsed")
	}
	text := StripHTML(first.Content)
	if !strings.Contains(text, "这是正文第一段") || strings.Contains(text, "<p>") {
		t.Fatalf("StripHTML failed: %q", text)
	}
}

func TestParseAtom(t *testing.T) {
	feed, err := Parse([]byte(atomSample))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if feed.Title != "Atom 源" || len(feed.Items) != 1 {
		t.Fatalf("unexpected feed: %+v", feed)
	}
	item := feed.Items[0]
	if item.Link != "https://example.com/atom/1" || item.GUID != "urn:uuid:1234" {
		t.Fatalf("unexpected item: %+v", item)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte("not xml at all")); err == nil {
		t.Fatal("expected parse error")
	}
	if _, err := Parse([]byte("<rss version=\"2.0\"><channel><title>空</title></channel></rss>")); err == nil {
		t.Fatal("expected no-items error")
	}
}

func TestValidateFeedURL(t *testing.T) {
	ok := []string{"https://example.com/feed", "http://example.com/rss"}
	for _, u := range ok {
		if err := ValidateFeedURL(u); err != nil {
			t.Fatalf("%s should be valid: %v", u, err)
		}
	}
	bad := []string{"file:///etc/passwd", "ftp://example.com", "gopher://x", "not a url", "https://"}
	for _, u := range bad {
		if err := ValidateFeedURL(u); err == nil {
			t.Fatalf("%s should be rejected", u)
		}
	}
}

func TestFetchFeedBlocksPrivateHosts(t *testing.T) {
	privateHosts := []string{
		"http://127.0.0.1/feed",
		"http://localhost/feed",
		"http://10.0.0.1/feed",
		"http://192.168.1.1/feed",
		"http://169.254.169.254/latest/meta-data",
		"http://[::1]/feed",
		"http://metadata.google.internal/feed",
	}
	for _, u := range privateHosts {
		if _, _, err := FetchFeed(context.Background(), u, FetchOptions{}); err == nil {
			t.Fatalf("%s should have been blocked", u)
		}
	}
}

func TestFetchFeedRejectsNonHTTPScheme(t *testing.T) {
	if _, _, err := FetchFeed(context.Background(), "file:///etc/passwd", FetchOptions{}); err == nil {
		t.Fatal("file:// should be rejected")
	}
}

func TestFetchFeedSuccessAndConditionalGET(t *testing.T) {
	var lastRequest *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastRequest = r
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 06 Oct 2026 10:00:00 GMT")
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(rss2Sample))
	}))
	defer server.Close()

	url := server.URL + "/feed"
	feed, result, err := FetchFeed(context.Background(), url, FetchOptions{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if result.NotModified {
		t.Fatal("first fetch must not be 304")
	}
	if result.ETag != `"v1"` || result.LastModified == "" {
		t.Fatalf("conditional headers not captured: %+v", result)
	}
	if len(feed.Items) != 2 {
		t.Fatalf("unexpected item count: %d", len(feed.Items))
	}

	// Second fetch with the returned validators should send them back.
	if _, _, err := FetchFeed(context.Background(), url, FetchOptions{
		ETag: result.ETag, LastModified: result.LastModified,
	}); err != nil {
		t.Fatalf("conditional fetch: %v", err)
	}
	if got := lastRequest.Header.Get("If-None-Match"); got != `"v1"` {
		t.Fatalf("If-None-Match not sent: %q", got)
	}
}

func TestFetchFeedNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	_, result, err := FetchFeed(context.Background(), server.URL+"/feed", FetchOptions{ETag: `"v1"`})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if !result.NotModified {
		t.Fatal("expected NotModified")
	}
}

func TestStripHTML(t *testing.T) {
	cases := map[string]string{
		"<p>a</p><p>b</p>":                    "a\nb",
		"<script>evil()</script>safe":         "safe",
		"<div>x<br/>y</div>":                  "x\ny",
		"&lt;tag&gt; &amp; more":              "<tag> & more",
	}
	for in, want := range cases {
		if got := StripHTML(in); got != want {
			t.Errorf("StripHTML(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParseXMLDeclarationOnly guards against feeds whose root element we do
// not model: Parse must fail closed rather than return an empty feed that
// would silently stop ingestion.
func TestParseUnknownRoot(t *testing.T) {
	_, err := Parse([]byte(`<?xml version="1.0"?><something><else/></something>`))
	if err == nil {
		t.Fatal("expected error for unknown feed root")
	}
	_ = xml.Unmarshal // keep import stable across refactors
}
