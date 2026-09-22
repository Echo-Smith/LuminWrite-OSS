package scholar

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Constrained full-text downloader (design.md §9 SSRF constraints).
// Security model, carried over from the original worker:
//
//   - Only http/https URLs are accepted; URLs must not carry credentials.
//   - Every hop's target is resolved (all A/AAAA records) and each IP is
//     checked against a blocklist: loopback, private (RFC1918/ULA),
//     link-local, the cloud-metadata addresses explicitly, unspecified,
//     multicast, reserved. IP-literal hosts are checked the same way without
//     DNS. Fail-closed: if ANY resolved address is blocked, the hop is
//     rejected (the attacker controls the DNS answer, so every record must
//     be clean).
//   - Connection pinning: validation and connection use the SAME resolution.
//     Each hop resolves once, screens every address, and dials one of the
//     validated IPs directly; Host header and TLS SNI keep the original
//     hostname so certificate verification still runs against the real name.
//     The transport never performs a second DNS lookup, so a DNS-rebinding
//     flip between resolve and connect cannot reroute the request.
//   - Redirects are followed manually, hop-by-hop, with full re-validation
//     and re-pinning per hop (max 5 hops).
//   - Size is bounded twice: Content-Length pre-check plus a streamed byte
//     counter with an abort at sizeLimit.
//   - Content-Type must be in the whitelist; application/octet-stream (and a
//     missing header) is admitted only after a magic-byte sniff.
//   - Content hash: SHA-256 of the downloaded bytes ("sha256:<hex>").
const (
	MaxRedirectHops   = 5
	DefaultSizeLimit  = 25 * 1024 * 1024
	downloadChunkSize = 64 * 1024
	sniffSampleSize   = 4096
)

var allowedContentTypes = map[string]bool{
	"application/pdf": true,
	"text/plain":      true,
	"text/markdown":   true,
}

const pdfMagic = "%PDF-"

// DownloadError is a typed download failure surfaced as an acquisition
// error_code.
type DownloadError struct {
	Code   string
	Detail string
}

func (e *DownloadError) Error() string { return e.Code + ": " + e.Detail }

// ipPolicy decides whether a resolved IP may be contacted. The default
// policy blocks loopback/private/link-local/metadata/reserved ranges. Tests
// may supply a custom policy (the only injection point; there is no env,
// config, or payload knob that weakens the production policy).
type ipPolicy interface {
	checkIP(ip net.IP) error
}

type defaultIPPolicy struct{}

func (defaultIPPolicy) checkIP(ip net.IP) error {
	bad := func(reason string) error {
		return &DownloadError{Code: "forbidden_target_ip",
			Detail: fmt.Sprintf("refusing to contact blocked address %s (%s)", ip, reason)}
	}
	// Unmap IPv4-mapped IPv6 so range checks see the true family.
	if v4 := ip.To4(); v4 != nil && !ip.Equal(net.IPv6loopback) && isV4Mapped(ip) {
		ip = v4
	}
	if ip.Equal(net.ParseIP("169.254.169.254")) ||
		ip.Equal(net.ParseIP("fd00:ec2::254")) || ip.Equal(net.ParseIP("fd00:ec2::253")) {
		return bad("cloud metadata endpoint")
	}
	if ip.IsUnspecified() {
		return bad("unspecified address")
	}
	if ip.IsLoopback() {
		return bad("loopback")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return bad("link-local")
	}
	if ip.IsMulticast() || ip.IsUnspecified() {
		return bad("multicast")
	}
	if isPrivateOrReserved(ip) {
		return bad("private/reserved")
	}
	return nil
}

// isV4Mapped reports whether the 16-byte IP is an IPv4-mapped IPv6 address
// (::ffff:a.b.c.d). Pure IPv6 addresses (including loopback) are not mapped.
func isV4Mapped(ip net.IP) bool {
	if len(ip) != net.IPv6len {
		return false
	}
	for i := 0; i < 12; i++ {
		if ip[i] != 0 {
			return false
		}
	}
	return ip[12] == 0xff && ip[13] == 0xff
}

func isPrivateOrReserved(ip net.IP) bool {
	privateRanges := []string{
		"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", // RFC1918
		"100.64.0.0/10",                   // carrier-grade NAT
		"198.18.0.0/15",                   // benchmarking
		"169.254.0.0/16",                  // link-local (v4 catch)
		"0.0.0.0/8",                       // this-network
		"192.0.0.0/24", "192.0.2.0/24",    // IETF protocol / TEST-NET-1
		"198.51.100.0/24", "203.0.113.0/24", // TEST-NET-2/3
		"224.0.0.0/4", "240.0.0.0/4", "255.255.255.255/32", // multicast/reserved/broadcast
		"fc00::/7",   // ULA
		"fe80::/10",  // link-local v6
		"2001:db8::/32", // documentation
		"::1/128", "::/128",
	}
	for _, cidr := range privateRanges {
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			continue
		}
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// resolveHostIPs resolves all A/AAAA records for a hostname. This is the DNS
// seam: production always uses the real resolver; tests may override the
// resolver on the dialer. Every returned address still goes through the IP
// policy.
func resolveHostIPs(ctx context.Context, hostname string) ([]net.IP, error) {
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, hostname)
	if err != nil {
		return nil, err
	}
	out := make([]net.IP, 0, len(ips))
	for _, addr := range ips {
		out = append(out, addr.IP)
	}
	return out, nil
}

// validatedTarget is one validated connection candidate for a single hop.
// addr dials directly; hostHeader and serverName keep the original hostname.
type validatedTarget struct {
	addr       string // "ip:port"
	hostHeader string // original hostname[:port]
	serverName string // TLS SNI / certificate name (original hostname)
}

// resolveAndValidate does URL validation + DNS resolution + IP screening for
// one hop. Fail-closed over ALL records. Returns the parsed URL, the
// validated de-duplicated address list (resolver order), and whether the
// host was an IP literal.
func resolveAndValidate(ctx context.Context, rawURL string, policy ipPolicy) (*url.URL, []net.IP, bool, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, false, &DownloadError{Code: "invalid_oa_url", Detail: "unparsable URL: " + err.Error()}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, nil, false, &DownloadError{Code: "invalid_oa_url",
			Detail: "only http/https URLs are allowed, got scheme " + strconv.Quote(parsed.Scheme)}
	}
	if parsed.Hostname() == "" {
		return nil, nil, false, &DownloadError{Code: "invalid_oa_url", Detail: "URL has no host"}
	}
	if parsed.User != nil {
		return nil, nil, false, &DownloadError{Code: "invalid_oa_url", Detail: "URL must not carry credentials"}
	}
	hostname := parsed.Hostname()
	var ips []net.IP
	if literal := net.ParseIP(hostname); literal != nil {
		ips = []net.IP{literal}
	} else {
		ips, err = resolveHostIPs(ctx, hostname)
		if err != nil {
			return nil, nil, false, &DownloadError{Code: "unresolvable_host",
				Detail: fmt.Sprintf("cannot resolve %q: %v", hostname, err)}
		}
	}
	if len(ips) == 0 {
		return nil, nil, false, &DownloadError{Code: "unresolvable_host",
			Detail: fmt.Sprintf("no addresses for host %q", hostname)}
	}
	// De-duplicate preserving resolver order.
	unique := ips[:0]
	seen := map[string]bool{}
	for _, ip := range ips {
		key := ip.String()
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, ip)
	}
	for _, ip := range unique {
		if err := policy.checkIP(ip); err != nil {
			return nil, nil, false, err
		}
	}
	return parsed, unique, net.ParseIP(hostname) != nil, nil
}

// pinTargets builds the per-address dial candidates for one hop (connection
// pinning: the transport dials the validated IP; Host/SNI keep the hostname).
func pinTargets(ctx context.Context, rawURL string, policy ipPolicy) ([]validatedTarget, error) {
	parsed, ips, literal, err := resolveAndValidate(ctx, rawURL, policy)
	if err != nil {
		return nil, err
	}
	hostname := parsed.Hostname()
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if literal {
		return []validatedTarget{{
			addr:       net.JoinHostPort(hostname, port),
			hostHeader: parsed.Host,
			serverName: hostname,
		}}, nil
	}
	targets := make([]validatedTarget, 0, len(ips))
	for _, ip := range ips {
		targets = append(targets, validatedTarget{
			addr:       net.JoinHostPort(ip.String(), port),
			hostHeader: parsed.Host,
			serverName: hostname,
		})
	}
	return targets, nil
}

// sniffMediaType maps the reported Content-Type to an admitted media type,
// sniffing when the server only says application/octet-stream (or nothing).
// ok=false rejects.
func sniffMediaType(contentType string, head []byte) (string, bool) {
	normalized := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	normalized = strings.ToLower(normalized)
	if allowedContentTypes[normalized] {
		return normalized, true
	}
	if normalized == "application/octet-stream" || normalized == "" {
		if strings.HasPrefix(string(head), pdfMagic) {
			return "application/pdf", true
		}
		if looksTextual(head) {
			return "text/plain", true
		}
		return "", false
	}
	// Anything else the server claims is rejected outright: HTML landing
	// pages, ZIPs, executables etc. are not admitted by magic-byte guessing.
	return "", false
}

// looksTextual is a cheap binary-vs-text heuristic on the first bytes.
func looksTextual(head []byte) bool {
	if len(head) == 0 {
		return false
	}
	sample := head
	if len(sample) > sniffSampleSize {
		sample = sample[:sniffSampleSize]
	}
	for _, b := range sample {
		if b == 0 {
			return false
		}
	}
	printable := 0
	for _, b := range sample {
		if (b >= 0x20 && b < 0x7F) || b == 9 || b == 10 || b == 13 {
			printable++
		}
	}
	return printable*10 >= 9*len(sample)
}

// probePDF reports (looksLikePDF, likelyScanned). A PDF with no /Font
// resources anywhere in the head/tail windows is a scan candidate.
// Deliberately conservative; page-level verification happens at parse time.
func probePDF(content []byte) (bool, *bool) {
	if !strings.HasPrefix(string(content), pdfMagic) {
		return false, nil
	}
	head := content
	if len(head) > 65536 {
		head = head[:65536]
	}
	tail := content
	if len(tail) > 65536 {
		tail = tail[len(tail)-65536:]
	}
	hasFonts := strings.Contains(string(head), "/Font") || strings.Contains(string(tail), "/Font")
	if !hasFonts {
		scanned := true
		return true, &scanned
	}
	return true, nil
}

// DownloadResult is the constrained download outcome (fetch_full_text core).
type DownloadResult struct {
	Content             []byte
	ContentHash         string
	SizeBytes           int64
	MediaType           string
	ContentTypeReported string
	FinalURL            string
	RedirectHops        int
	LooksLikePDF        bool
	LikelyScanned       *bool
}

// downloadHTTPClient is the production egress client: redirects are NEVER
// followed automatically (the manual loop re-validates and re-pins every
// hop) and proxy environment variables are ignored (HTTP_PROXY etc. would
// relay the connection through an uncontrolled hop that re-resolves DNS
// outside the validated-IP pinning). Connection reuse is disabled so a
// pooled connection opened for one hostname can never be reused for another
// sharing the same IP, silently skipping TLS verification.
var downloadHTTPClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DisableKeepAlives: true,
		Proxy:             nil,
		DialContext:       nil, // overridden per-request via pinned URL host
		TLSClientConfig:   &tls.Config{},
	},
}

// constrainedDownload downloads rawURL under the full constraint set,
// following redirects manually with per-hop re-validation and IP pinning.
// Every hop: validate scheme/credentials, resolve DNS once, screen every
// resolved IP, then connect to one of the validated addresses directly while
// Host/SNI keep the original hostname. The final response streams through
// the byte cap with SHA-256 computed on the fly, so an oversized body is
// aborted mid-stream instead of being swallowed whole.
func constrainedDownload(ctx context.Context, rawURL string, sizeLimit int64, policy ipPolicy, hop int) (*DownloadResult, error) {
	if policy == nil {
		policy = defaultIPPolicy{}
	}
	if hop > MaxRedirectHops {
		return nil, &DownloadError{Code: "too_many_redirects",
			Detail: fmt.Sprintf("exceeded %d redirect hops", MaxRedirectHops)}
	}
	if sizeLimit <= 0 {
		sizeLimit = DefaultSizeLimit
	}

	targets, err := pinTargets(ctx, rawURL, policy)
	if err != nil {
		return nil, err
	}

	var connectFailure error
	for _, target := range targets {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, &DownloadError{Code: "invalid_oa_url", Detail: err.Error()}
		}
		req.Header.Set("User-Agent", providerUAHeader)
		// Pin the connection: replace the URL host with the validated IP and
		// restore the original hostname via Host header + TLS ServerName. A
		// fresh transport per request (no lock copying, no keep-alive) keeps
		// each hop's TLS check bound to the original hostname.
		parsed, _ := url.Parse(rawURL)
		pinned := *parsed
		pinned.Host = target.addr
		req.URL = &pinned
		req.Host = target.hostHeader
		client := &http.Client{
			CheckRedirect: downloadHTTPClient.CheckRedirect,
			Transport: &http.Transport{
				DisableKeepAlives: true,
				Proxy:             nil,
				TLSClientConfig:   &tls.Config{ServerName: target.serverName},
			},
		}

		resp, err := client.Do(req)
		if err != nil {
			// Connection-phase failure: try the next validated address,
			// mirroring multi-record connect fallback.
			connectFailure = err
			if ctx.Err() != nil {
				return nil, &DownloadError{Code: "download_timeout", Detail: ctx.Err().Error()}
			}
			continue
		}

		if isRedirect(resp.StatusCode) {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			if location == "" {
				return nil, &DownloadError{Code: "redirect_without_location",
					Detail: rawURL + ": redirect lacks Location"}
			}
			next, err := parsed.Parse(location)
			if err != nil {
				return nil, &DownloadError{Code: "invalid_oa_url",
					Detail: "redirect Location is not a URL: " + err.Error()}
			}
			return constrainedDownload(ctx, next.String(), sizeLimit, policy, hop+1)
		}

		if resp.StatusCode >= 400 {
			resp.Body.Close()
			return nil, &DownloadError{Code: "download_http_error",
				Detail: fmt.Sprintf("%s: HTTP %d", rawURL, resp.StatusCode)}
		}

		contentLength := resp.Header.Get("Content-Length")
		if n, err := strconv.ParseInt(contentLength, 10, 64); err == nil && n > sizeLimit {
			resp.Body.Close()
			return nil, &DownloadError{Code: "content_too_large",
				Detail: fmt.Sprintf("Content-Length %d exceeds limit %d", n, sizeLimit)}
		}

		head := make([]byte, 0, sniffSampleSize)
		hasher := sha256.New()
		var chunks [][]byte
		var total int64
		buf := make([]byte, downloadChunkSize)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				total += int64(n)
				if len(head) < sniffSampleSize {
					take := n
					if remaining := sniffSampleSize - len(head); take > remaining {
						take = remaining
					}
					head = append(head, buf[:take]...)
				}
				if total > sizeLimit {
					resp.Body.Close()
					return nil, &DownloadError{Code: "content_too_large",
						Detail: fmt.Sprintf("streamed body exceeded limit %d; aborted after %d bytes", sizeLimit, total)}
				}
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				chunks = append(chunks, chunk)
				hasher.Write(chunk)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				resp.Body.Close()
				return nil, &DownloadError{Code: "download_read_error", Detail: readErr.Error()}
			}
		}
		resp.Body.Close()

		content := make([]byte, 0, total)
		for _, chunk := range chunks {
			content = append(content, chunk...)
		}
		mediaType, ok := sniffMediaType(resp.Header.Get("Content-Type"), head)
		if !ok {
			return nil, &DownloadError{Code: "content_type_rejected",
				Detail: fmt.Sprintf("Content-Type %q is not allowed", resp.Header.Get("Content-Type"))}
		}
		looksPDF, likelyScanned := probePDF(content)
		// FinalURL reports the caller-facing URL of this hop — the hostname
		// form, never the internal pinned-IP URL.
		return &DownloadResult{
			Content:             content,
			ContentHash:         hashBytes(content),
			SizeBytes:           int64(len(content)),
			MediaType:           mediaType,
			ContentTypeReported: resp.Header.Get("Content-Type"),
			FinalURL:            rawURL,
			RedirectHops:        hop,
			LooksLikePDF:        looksPDF,
			LikelyScanned:       likelyScanned,
		}, nil
	}
	if connectFailure != nil {
		return nil, &DownloadError{Code: "download_unreachable",
			Detail: rawURL + ": " + connectFailure.Error()}
	}
	return nil, &DownloadError{Code: "download_unreachable", Detail: rawURL + ": no validated target"}
}

func isRedirect(status int) bool {
	return status == http.StatusMovedPermanently || status == http.StatusFound ||
		status == http.StatusSeeOther || status == http.StatusTemporaryRedirect ||
		status == http.StatusPermanentRedirect
}

// base64Std encodes content for the inline blob transfer.
func base64Std(content []byte) string {
	return base64.StdEncoding.EncodeToString(content)
}
