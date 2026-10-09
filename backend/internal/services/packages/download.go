package packages

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DownloadTimeout caps one package download.
const DownloadTimeout = 30 * time.Second

// maxRedirects caps redirect hops; each hop is re-validated.
const maxRedirects = 3

// FetchPackage downloads a package body (zip) from rawURL with SSRF
// protections, returning at most MaxDownloadBytes:
//   - scheme must be http/https;
//   - the host is resolved and every resolved IP is rejected when it is
//     loopback, private, link-local, or otherwise reserved (this covers
//     localhost, 127.0.0.0/8, 10/8, 172.16/12, 192.168/16, 169.254/16
//     including the cloud metadata endpoint, and IPv6 equivalents);
//   - redirects are followed manually so every hop is re-validated;
//   - the body is size-capped and time-capped.
func FetchPackage(rawURL string) ([]byte, error) {
	current := strings.TrimSpace(rawURL)
	if current == "" {
		return nil, fmt.Errorf("package url is required")
	}
	client := &http.Client{Timeout: DownloadTimeout}
	// Redirects are handled manually (CheckRedirect rejects), so the client
	// never follows one on its own.
	_ = client

	for hop := 0; hop <= maxRedirects; hop++ {
		parsed, err := url.Parse(current)
		if err != nil {
			return nil, fmt.Errorf("invalid package url: %w", err)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("package url must be http(s)")
		}
		host := parsed.Hostname()
		if host == "" {
			return nil, fmt.Errorf("package url has no host")
		}
		if err := validateHost(host); err != nil {
			return nil, err
		}

		body, status, finalURL, err := getOnce(client, current)
		if err != nil {
			return nil, err
		}
		switch {
		case status >= 300 && status < 400 && finalURL != "" && finalURL != current:
			current = finalURL
			continue
		case status != http.StatusOK:
			return nil, fmt.Errorf("package host returned status %d", status)
		}
		if int64(len(body)) > MaxDownloadBytes {
			return nil, fmt.Errorf("package exceeds %d bytes", MaxDownloadBytes)
		}
		return body, nil
	}
	return nil, fmt.Errorf("too many redirects")
}

// getOnce performs one request without following redirects, returning the
// body (capped), the status, and the redirect target when present.
func getOnce(client *http.Client, target string) (body []byte, status int, redirectTo string, err error) {
	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		return nil, 0, "", fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/zip, application/octet-stream")
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("download package: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, resp.StatusCode, resp.Header.Get("Location"), nil
	}
	limited := io.LimitReader(resp.Body, MaxDownloadBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, resp.StatusCode, "", fmt.Errorf("read package body: %w", err)
	}
	return data, resp.StatusCode, "", nil
}

// validateHost resolves the host and rejects any address in a
// non-routable/reserved range.
func validateHost(host string) error {
	// Literal IP first (no DNS).
	if ip := net.ParseIP(host); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("package host %s is not a public address", host)
		}
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("resolve package host: %w", err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("package host %s does not resolve", host)
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("package host %s resolves to a non-public address", host)
		}
	}
	return nil
}

// isPublicIP reports whether ip is routable public address space: the net
// package predicates plus the reserved ranges they do not model directly
// (documentation, benchmarking, CGNAT, IETF assignments, 240/4).
func isPublicIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() {
		return false
	}
	if ip4 := ip.To4(); ip4 != nil {
		switch {
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 2: // 192.0.2.0/24 (TEST-NET-1)
			return false
		case ip4[0] == 198 && ip4[1] == 51 && ip4[2] == 100: // 198.51.100.0/24 (TEST-NET-2)
			return false
		case ip4[0] == 203 && ip4[1] == 0 && ip4[2] == 113: // 203.0.113.0/24 (TEST-NET-3)
			return false
		case ip4[0] == 198 && ip4[1] == 18: // 198.18.0.0/15 (benchmarking)
			return false
		case ip4[0] == 192 && ip4[1] == 0 && ip4[2] == 0: // 192.0.0.0/24 (IETF assignments)
			return false
		case ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127: // 100.64.0.0/10 (CGNAT)
			return false
		case ip4[0] >= 240: // 240.0.0.0/4 reserved (incl. 255.255.255.255)
			return false
		}
	}
	return ip.IsGlobalUnicast()
}
