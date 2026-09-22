package scholar

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Three academic discovery providers (contracts.md §4): OpenAlex, Crossref,
// Semantic Scholar. Each adapter is robust to malformed fields (missing
// title/authors/year become nil/[] rather than an error; non-object entries
// are skipped) and never raises — failures surface as a ProviderResult with
// status "error" so a single dead source never sinks the fan-out.

const (
	ProviderOpenAlex         = "openalex"
	ProviderCrossref         = "crossref"
	ProviderSemanticScholar  = "semantic_scholar"
	providerUnknownErrorCode = "provider_unknown"

	providerTimeout  = 15 * time.Second
	providerUAHeader = "lumin-scholar-go/1.0 (research discovery)"

	openAlexBaseURL        = "https://api.openalex.org/works"
	crossrefBaseURL        = "https://api.crossref.org/works"
	semanticScholarBaseURL = "https://api.semanticscholar.org/graph/v1/paper/search"
)

// KnownProviders is the fixed provider allowlist (the security boundary:
// only these are callable, so a caller cannot smuggle an arbitrary fetch
// target through a payload).
var KnownProviders = map[string]bool{
	ProviderOpenAlex: true, ProviderCrossref: true, ProviderSemanticScholar: true,
}

// providerHTTPClient is the shared egress client for discovery searches.
// Proxy environment variables are ignored: egress goes direct (the scholar
// downloader keeps the same rule for its SSRF-pinned path).
var providerHTTPClient = &http.Client{Timeout: providerTimeout}

var _ = regexp.MustCompile // keep regexp import if unused by future edits

func checkAllowlist(allowlist []string) error {
	var unknown []string
	for _, p := range allowlist {
		if !KnownProviders[p] {
			unknown = append(unknown, p)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf("unknown provider(s): %s", strings.Join(unknown, ", "))
	}
	return nil
}

func fetchProviderJSON(ctx context.Context, base string, params url.Values) (map[string]any, *string) {
	endpoint := base + "?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, strPtr("provider_bad_request")
	}
	req.Header.Set("User-Agent", providerUAHeader)
	req.Header.Set("Accept", "application/json")
	resp, err := providerHTTPClient.Do(req)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, strPtr("provider_deadline")
		}
		return nil, strPtr("provider_unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, strPtr("provider_rate_limited")
	}
	if resp.StatusCode >= 400 {
		return nil, strPtr("provider_http_error")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, strPtr("provider_read_error")
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, strPtr("provider_bad_json")
	}
	return decoded, nil
}

func strPtr(s string) *string { return &s }

func jsonStr(v any) *string {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return nil
	}
	trimmed := strings.TrimSpace(s)
	return &trimmed
}

func jsonInt(v any) *int64 {
	switch n := v.(type) {
	case float64:
		if n == float64(int64(n)) {
			out := int64(n)
			return &out
		}
	case int64:
		return &n
	case int:
		out := int64(n)
		return &out
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return &i
		}
	}
	return nil
}

// --- OpenAlex -----------------------------------------------------------

func openAlexAuthorNames(item map[string]any) []string {
	authorships, ok := item["authorships"].([]any)
	if !ok {
		return nil
	}
	names := []string{}
	for _, entry := range authorships {
		e, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		author, _ := e["author"].(map[string]any)
		if author == nil {
			continue
		}
		if name := jsonStr(author["display_name"]); name != nil {
			names = append(names, *name)
		}
	}
	return names
}

func openAlexAbstract(item map[string]any) *string {
	// Rebuild the abstract from OpenAlex's inverted index, if present.
	index, ok := item["abstract_inverted_index"].(map[string]any)
	if !ok || len(index) == 0 {
		return jsonStr(item["abstract"])
	}
	type pos struct{ i int; word string }
	positions := make([]pos, 0, len(index))
	for word, spotsAny := range index {
		spots, ok := spotsAny.([]any)
		if !ok {
			continue
		}
		for _, spotAny := range spots {
			if i, ok := spotAny.(float64); ok && i >= 0 {
				positions = append(positions, pos{int(i), word})
			}
		}
	}
	if len(positions) == 0 {
		return nil
	}
	// Sort by position; ties keep map order — deterministic rebuild requires
	// a stable tiebreak, so equal positions sort by word.
	for i := 1; i < len(positions); i++ {
		for j := i; j > 0; j-- {
			if positions[j].i < positions[j-1].i ||
				(positions[j].i == positions[j-1].i && positions[j].word < positions[j-1].word) {
				positions[j], positions[j-1] = positions[j-1], positions[j]
				continue
			}
			break
		}
	}
	words := make([]string, len(positions))
	for i, p := range positions {
		words[i] = p.word
	}
	joined := strings.Join(words, " ")
	return &joined
}

func parseOpenAlexItem(item any) *RawProviderRecord {
	m, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	title := jsonStr(m["title"])
	if title == nil {
		title = jsonStr(m["display_name"])
	}
	venue := (*string)(nil)
	if primary, ok := m["primary_location"].(map[string]any); ok {
		if source, ok := primary["source"].(map[string]any); ok {
			venue = jsonStr(source["display_name"])
		}
	}
	var doiRaw string
	if s := jsonStr(m["doi"]); s != nil {
		doiRaw = *s
	}
	var oaURL *string
	if oa, ok := m["open_access"].(map[string]any); ok {
		oaURL = jsonStr(oa["oa_url"])
	}
	return &RawProviderRecord{
		Title:        title,
		Authors:      openAlexAuthorNames(m),
		Year:         jsonInt(m["publication_year"]),
		DOI:          doiRaw,
		Abstract:     openAlexAbstract(m),
		Venue:        venue,
		CanonicalURL: jsonStr(m["id"]),
		OAURL:        oaURL,
		Provider:     ProviderOpenAlex,
		ProviderID:   derefOr(jsonStr(m["id"])),
	}
}

func derefOr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func searchOpenAlex(ctx context.Context, query string, limit int) ([]RawProviderRecord, ProviderResult) {
	params := url.Values{}
	params.Set("search", query)
	params.Set("per-page", strconv.Itoa(limit))
	params.Set("select", "id,title,display_name,doi,publication_year,authorships,primary_location,open_access,abstract_inverted_index")
	body, errCode := fetchProviderJSON(ctx, openAlexBaseURL, params)
	if errCode != nil {
		return nil, ProviderResult{Provider: ProviderOpenAlex, Status: "error", ErrorCode: errCode}
	}
	results, ok := body["results"].([]any)
	if !ok {
		return nil, ProviderResult{Provider: ProviderOpenAlex, Status: "error", ErrorCode: strPtr("provider_bad_json")}
	}
	records := []RawProviderRecord{}
	for _, item := range results {
		if r := parseOpenAlexItem(item); r != nil {
			records = append(records, *r)
		}
	}
	return records, ProviderResult{Provider: ProviderOpenAlex, Status: "ok", Returned: len(records)}
}

// --- Crossref -----------------------------------------------------------

func crossrefAuthorNames(item map[string]any) []string {
	raw, ok := item["author"].([]any)
	if !ok {
		return nil
	}
	authors := []string{}
	for _, entry := range raw {
		e, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if name := jsonStr(e["name"]); name != nil {
			authors = append(authors, *name)
			continue
		}
		given := derefOr(jsonStr(e["given"]))
		family := derefOr(jsonStr(e["family"]))
		switch {
		case given != "" && family != "":
			authors = append(authors, family+", "+given)
		case family != "" || given != "":
			if family != "" {
				authors = append(authors, family)
			} else {
				authors = append(authors, given)
			}
		}
	}
	return authors
}

func crossrefYear(item map[string]any) *int64 {
	for _, field := range []string{"published-print", "published-online", "issued"} {
		dates, ok := item[field].(map[string]any)
		if !ok {
			continue
		}
		parts, ok := dates["date-parts"].([]any)
		if !ok || len(parts) == 0 {
			continue
		}
		first, ok := parts[0].([]any)
		if !ok || len(first) == 0 {
			continue
		}
		if year := jsonInt(first[0]); year != nil {
			return year
		}
	}
	return nil
}

func parseCrossrefItem(item any) *RawProviderRecord {
	m, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	var title *string
	if rawTitles, ok := m["title"].([]any); ok && len(rawTitles) > 0 {
		title = jsonStr(rawTitles[0])
	}
	var doiRaw string
	if s := jsonStr(m["DOI"]); s != nil {
		doiRaw = *s
	}
	venue := (*string)(nil)
	if containers, ok := m["container-title"].([]any); ok && len(containers) > 0 {
		venue = jsonStr(containers[0])
	}
	link := (*string)(nil)
	if links, ok := m["link"].([]any); ok && len(links) > 0 {
		if l, ok := links[0].(map[string]any); ok {
			link = jsonStr(l["URL"])
		}
	}
	if link == nil {
		if resource, ok := m["resource"].(map[string]any); ok {
			if primary, ok := resource["primary"].(map[string]any); ok {
				link = jsonStr(primary["URL"])
			}
		}
	}
	abstract := jsonStr(m["abstract"])
	if abstract != nil && strings.Contains(*abstract, "<") {
		// Crossref abstracts often carry JATS tags; strip the two common wraps.
		for _, tag := range []string{"jats:p", "p"} {
			prefix, suffix := "<"+tag+">", "</"+tag+">"
			if strings.HasPrefix(*abstract, prefix) && strings.HasSuffix(*abstract, suffix) {
				trimmed := strings.TrimSpace((*abstract)[len(prefix) : len(*abstract)-len(suffix)])
				abstract = &trimmed
				break
			}
		}
	}
	return &RawProviderRecord{
		Title:        title,
		Authors:      crossrefAuthorNames(m),
		Year:         crossrefYear(m),
		DOI:          doiRaw,
		Abstract:     abstract,
		Venue:        venue,
		CanonicalURL: link,
		Provider:     ProviderCrossref,
		ProviderID:   derefOr(jsonStr(m["DOI"])),
	}
}

func searchCrossref(ctx context.Context, query string, limit int) ([]RawProviderRecord, ProviderResult) {
	params := url.Values{}
	params.Set("query.bibliographic", query)
	params.Set("rows", strconv.Itoa(limit))
	params.Set("select", "DOI,title,author,issued,published-print,published-online,container-title,link,resource,abstract")
	body, errCode := fetchProviderJSON(ctx, crossrefBaseURL, params)
	if errCode != nil {
		return nil, ProviderResult{Provider: ProviderCrossref, Status: "error", ErrorCode: errCode}
	}
	message, _ := body["message"].(map[string]any)
	items, ok := message["items"].([]any)
	if !ok {
		return nil, ProviderResult{Provider: ProviderCrossref, Status: "error", ErrorCode: strPtr("provider_bad_json")}
	}
	records := []RawProviderRecord{}
	for _, item := range items {
		if r := parseCrossrefItem(item); r != nil {
			records = append(records, *r)
		}
	}
	return records, ProviderResult{Provider: ProviderCrossref, Status: "ok", Returned: len(records)}
}

// --- Semantic Scholar ----------------------------------------------------

const s2Fields = "title,externalIds,authors,year,venue,abstract,url,openAccessPdf"

func s2PaperID(item map[string]any) string {
	if external, ok := item["externalIds"].(map[string]any); ok {
		for _, key := range []string{"DOI", "CorpusId", "ArXiv"} {
			if v, ok := external[key]; ok {
				switch value := v.(type) {
				case string:
					if strings.TrimSpace(value) != "" {
						return key + ":" + value
					}
				case float64:
					return key + ":" + strconv.FormatInt(int64(value), 10)
				}
			}
		}
	}
	return derefOr(jsonStr(item["paperId"]))
}

func parseSemanticScholarItem(item any) *RawProviderRecord {
	m, ok := item.(map[string]any)
	if !ok {
		return nil
	}
	authors := []string{}
	if raw, ok := m["authors"].([]any); ok {
		for _, entry := range raw {
			if e, ok := entry.(map[string]any); ok {
				if name := jsonStr(e["name"]); name != nil {
					authors = append(authors, *name)
				}
			}
		}
	}
	var doi string
	if external, ok := m["externalIds"].(map[string]any); ok {
		if s := jsonStr(external["DOI"]); s != nil {
			doi = *s
		}
	}
	var oaURL *string
	if oa, ok := m["openAccessPdf"].(map[string]any); ok {
		oaURL = jsonStr(oa["url"])
	}
	return &RawProviderRecord{
		Title:        jsonStr(m["title"]),
		Authors:      authors,
		Year:         jsonInt(m["year"]),
		DOI:          doi,
		Abstract:     jsonStr(m["abstract"]),
		Venue:        jsonStr(m["venue"]),
		CanonicalURL: jsonStr(m["url"]),
		OAURL:        oaURL,
		Provider:     ProviderSemanticScholar,
		ProviderID:   s2PaperID(m),
	}
}

func searchSemanticScholar(ctx context.Context, query string, limit int) ([]RawProviderRecord, ProviderResult) {
	params := url.Values{}
	params.Set("query", query)
	params.Set("limit", strconv.Itoa(limit))
	params.Set("fields", s2Fields)
	body, errCode := fetchProviderJSON(ctx, semanticScholarBaseURL, params)
	if errCode != nil {
		return nil, ProviderResult{Provider: ProviderSemanticScholar, Status: "error", ErrorCode: errCode}
	}
	data, ok := body["data"].([]any)
	if !ok {
		return nil, ProviderResult{Provider: ProviderSemanticScholar, Status: "error", ErrorCode: strPtr("provider_bad_json")}
	}
	records := []RawProviderRecord{}
	for _, item := range data {
		if r := parseSemanticScholarItem(item); r != nil {
			records = append(records, *r)
		}
	}
	return records, ProviderResult{Provider: ProviderSemanticScholar, Status: "ok", Returned: len(records)}
}

func searchProvider(ctx context.Context, provider, query string, limit int) ([]RawProviderRecord, ProviderResult) {
	switch provider {
	case ProviderOpenAlex:
		return searchOpenAlex(ctx, query, limit)
	case ProviderCrossref:
		return searchCrossref(ctx, query, limit)
	case ProviderSemanticScholar:
		return searchSemanticScholar(ctx, query, limit)
	default:
		return nil, ProviderResult{Provider: provider, Status: "error", ErrorCode: strPtr(providerUnknownErrorCode)}
	}
}
