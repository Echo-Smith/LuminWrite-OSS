// Package rss implements a dependency-free feed parser (RSS 2.0, RDF/RSS 1.0,
// Atom 1.0) plus an SSRF-hardened fetcher for user-subscribed feed URLs.
//
// Design notes:
//   - encoding/xml from the standard library only — no gofeed dependency.
//   - Item bodies prefer the feed's own full text (content:encoded /
//     content). The legacy regex HTML extractor is site-tuned and mangles
//     feed prose, so HTML fetching is a fallback for summary-only feeds.
//   - The fetcher validates scheme, resolves the host, and rejects private,
//     loopback, and link-local targets before connecting.
package rss

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Item is one normalized feed entry.
type Item struct {
	GUID        string // stable id when the feed provides one
	Title       string
	Link        string
	Author      string
	PublishedAt time.Time
	Content     string // feed-provided body (HTML stripped to text by StripHTML)
}

// Feed is a normalized feed document.
type Feed struct {
	Title       string
	Description string
	SiteURL     string
	Items       []Item
}

// ─── Wire formats ─────────────────────────────────────────

type rss2Feed struct {
	Channel struct {
		Title       string    `xml:"title"`
		Description string    `xml:"description"`
		Link        string    `xml:"link"`
		Items       []rss2Item `xml:"item"`
	} `xml:"channel"`
}

type rss2Item struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	GUID        string `xml:"guid"`
	Author      string `xml:"author"`
	Creator     string `xml:"creator"` // dc:creator
	PubDate     string `xml:"pubDate"`
	Description string `xml:"description"`
	Encoded     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
}

type rdfFeed struct {
	Channel struct {
		Title       string    `xml:"title"`
		Description string    `xml:"description"`
		Link        string    `xml:"link"`
	} `xml:"channel"`
	Items []rdfItem `xml:"item"`
}

type rdfItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Author      string `xml:"author"`
	Creator     string `xml:"creator"`
	PubDate     string `xml:"http://purl.org/dc/elements/1.1/ date"`
	Description string `xml:"description"`
	Encoded     string `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
}

type atomFeed struct {
	Title   string     `xml:"title"`
	Entries []atomItem `xml:"entry"`
}

type atomItem struct {
	Title     string     `xml:"title"`
	ID        string     `xml:"id"`
	Link      []atomLink `xml:"link"`
	Author    struct {
		Name string `xml:"name"`
	} `xml:"author"`
	Published string `xml:"published"`
	Updated   string `xml:"updated"`
	Summary   string `xml:"summary"`
	Content   string `xml:"content"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

// Parse decodes RSS 2.0, RDF/RSS 1.0, or Atom 1.0 bytes.
func Parse(data []byte) (*Feed, error) {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil, fmt.Errorf("empty feed body")
	}

	// Root-element probing by substring: XMLName tricks are unreliable across
	// the three wire formats, and the markers below are unambiguous.
	lower := strings.ToLower(trimmed)
	var feed Feed
	switch {
	case strings.Contains(lower, "<feed") && strings.Contains(lower, "2005/atom"):
		var af atomFeed
		if err := xml.Unmarshal(data, &af); err != nil {
			return nil, fmt.Errorf("parse atom feed: %w", err)
		}
		feed.Title = strings.TrimSpace(af.Title)
		for _, e := range af.Entries {
			feed.Items = append(feed.Items, Item{
				GUID:        strings.TrimSpace(e.ID),
				Title:       strings.TrimSpace(e.Title),
				Link:        atomEntryLink(e),
				Author:      strings.TrimSpace(e.Author.Name),
				PublishedAt: parseTime(firstNonEmpty(e.Published, e.Updated)),
				Content:     firstNonEmpty(e.Content, e.Summary),
			})
		}
	case strings.Contains(lower, "rdf-syntax-ns#"):
		var rf rdfFeed
		if err := xml.Unmarshal(data, &rf); err != nil {
			return nil, fmt.Errorf("parse rdf feed: %w", err)
		}
		feed.Title = strings.TrimSpace(rf.Channel.Title)
		feed.Description = strings.TrimSpace(rf.Channel.Description)
		feed.SiteURL = strings.TrimSpace(rf.Channel.Link)
		for _, it := range rf.Items {
			feed.Items = append(feed.Items, Item{
				GUID:        strings.TrimSpace(it.Link),
				Title:       strings.TrimSpace(it.Title),
				Link:        strings.TrimSpace(it.Link),
				Author:      firstNonEmpty(it.Creator, it.Author),
				PublishedAt: parseTime(it.PubDate),
				Content:     firstNonEmpty(it.Encoded, it.Description),
			})
		}
	default:
		var rf rss2Feed
		if err := xml.Unmarshal(data, &rf); err != nil {
			return nil, fmt.Errorf("parse rss feed: %w", err)
		}
		feed.Title = strings.TrimSpace(rf.Channel.Title)
		feed.Description = strings.TrimSpace(rf.Channel.Description)
		feed.SiteURL = strings.TrimSpace(rf.Channel.Link)
		for _, it := range rf.Channel.Items {
			guid := strings.TrimSpace(it.GUID)
			if guid == "" {
				guid = strings.TrimSpace(it.Link)
			}
			feed.Items = append(feed.Items, Item{
				GUID:        guid,
				Title:       strings.TrimSpace(it.Title),
				Link:        strings.TrimSpace(it.Link),
				Author:      firstNonEmpty(it.Creator, it.Author),
				PublishedAt: parseTime(it.PubDate),
				Content:     firstNonEmpty(it.Encoded, it.Description),
			})
		}
	}

	if len(feed.Items) == 0 {
		return nil, fmt.Errorf("feed contains no items")
	}
	return &feed, nil
}

func atomEntryLink(e atomItem) string {
	for _, l := range e.Link {
		if l.Rel == "" || l.Rel == "alternate" {
			if l.Href != "" {
				return l.Href
			}
		}
	}
	if len(e.Link) > 0 {
		return e.Link[0].Href
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func parseTime(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC1123Z, time.RFC1123, time.RFC3339, "2006-01-02T15:04:05Z07:00", "2006-01-02", time.RFC822} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ─── SSRF-hardened fetching ──────────────────────────────

const (
	maxFeedBytes = 8 << 20 // 8 MiB
	fetchTimeout = 20 * time.Second
	userAgent    = "LuminBuddy-RSS/1.0 (+https://github.com/Echo-Smith/LuminWrite-OSS)"
)

// FetchOptions carries conditional-GET state from the subscription row.
type FetchOptions struct {
	ETag         string
	LastModified string
}

// FetchResult carries the payload plus updated conditional-GET headers.
type FetchResult struct {
	NotModified  bool
	ETag         string
	LastModified string
}

// ValidateFeedURL rejects anything that is not a plain http(s) URL with a
// host literal. DNS-level checks happen at dial time (see safeDialer).
func ValidateFeedURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("invalid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http and https feeds are supported")
	}
	if u.Host == "" {
		return fmt.Errorf("feed URL needs a host")
	}
	return nil
}

// hostGuard and ipGuard are the SSRF decision points; tests override them to
// permit loopback listeners (httptest).
var (
	hostGuard = blockedHost
	ipGuard   = blockedIP
)

// blockedHost reports whether the host (literal or resolved) is private,
// loopback, link-local, or otherwise not a public internet address.
func blockedHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return blockedIP(ip)
	}
	return false
}

func blockedIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast()
}

// safeDialer resolves the target and refuses connections to private or
// loopback addresses, defeating DNS-rebinding to internal names at dial time.
func safeDialer(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var allowed []net.IP
	for _, ip := range ips {
		if !ipGuard(ip.IP) {
			allowed = append(allowed, ip.IP)
		}
	}
	if len(allowed) == 0 {
		return nil, fmt.Errorf("host resolves to a non-public address")
	}
	var lastErr error
	for _, ip := range allowed {
		conn, err := (&net.Dialer{Timeout: fetchTimeout}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

// FetchFeed downloads and parses a feed with SSRF protection and
// conditional-GET support.
func FetchFeed(ctx context.Context, rawURL string, opts FetchOptions) (*Feed, *FetchResult, error) {
	if err := ValidateFeedURL(rawURL); err != nil {
		return nil, nil, err
	}
	u, _ := url.Parse(strings.TrimSpace(rawURL))
	if hostGuard(u.Hostname()) {
		return nil, nil, fmt.Errorf("feed host is not a public address")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5")
	if opts.ETag != "" {
		req.Header.Set("If-None-Match", opts.ETag)
	}
	if opts.LastModified != "" {
		req.Header.Set("If-Modified-Since", opts.LastModified)
	}

	client := &http.Client{
		Timeout: fetchTimeout,
		Transport: &http.Transport{
			DialContext:         safeDialer,
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   true,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			if hostGuard(req.URL.Hostname()) {
				return fmt.Errorf("redirect to a non-public address")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetch feed: %w", err)
	}
	defer resp.Body.Close()

	result := &FetchResult{
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
	}
	if resp.StatusCode == http.StatusNotModified {
		result.NotModified = true
		return nil, result, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("feed returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read feed body: %w", err)
	}
	feed, err := Parse(body)
	if err != nil {
		return nil, nil, err
	}
	return feed, result, nil
}

// StripHTML converts feed HTML bodies to plain text: drops script/style
// blocks, turns block-level tags into newlines, and decodes entities.
func StripHTML(s string) string {
	var b strings.Builder
	i := 0
	lower := strings.ToLower(s)
	for i < len(s) {
		if s[i] == '<' {
			end := strings.IndexByte(s[i:], '>')
			if end < 0 {
				break
			}
			tag := lower[i+1 : i+end]
			switch {
			case strings.HasPrefix(tag, "script"), strings.HasPrefix(tag, "style"):
				closeTag := "</" + strings.SplitN(tag, " ", 2)[0]
				if idx := strings.Index(lower[i:], closeTag); idx >= 0 {
					// +1 skips the closing tag's own '>'
					i += idx + len(closeTag) + 1
					continue
				}
			case strings.HasPrefix(tag, "br"), strings.HasPrefix(tag, "/p"),
				strings.HasPrefix(tag, "/div"), strings.HasPrefix(tag, "/li"),
				strings.HasPrefix(tag, "/h"):
				b.WriteString("\n")
			}
			i += end + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	text := decodeEntities(b.String())
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			kept = append(kept, strings.TrimSpace(l))
		}
	}
	return strings.Join(kept, "\n")
}

func decodeEntities(s string) string {
	replacer := strings.NewReplacer(
		"&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">",
		"&quot;", "\"", "&#39;", "'", "&apos;", "'",
	)
	return replacer.Replace(s)
}
