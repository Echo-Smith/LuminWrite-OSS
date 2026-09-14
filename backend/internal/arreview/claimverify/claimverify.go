// Package claimverify implements the host-side, different-source semantic
// second line of defense for AR-012 candidates: an independent verification
// model (a different vendor than the generation model) re-reads the numbered
// candidate manuscript and judges, sentence by sentence, whether each cited
// claim is actually supported by the cited frozen abstracts.
//
// It is REPORT-ONLY: the report is stored as a sixth job artifact
// (claim-check/1) and never gates, rewrites, or blocks anything. The same
// vendor must not be used for both generation and verification — the
// deployment is responsible for pointing AR_REVIEW_VERIFY_* at a different
// provider (documented in gate-inventory.md §5).
package claimverify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Config is the verification model endpoint (OpenAI-compatible).
type Config struct {
	BaseURL   string
	APIKey    string
	Model     string
	TimeoutMS int
}

// SourceRef is one frozen corpus source the verifier can be cited against.
type SourceRef struct {
	Index    int    // 1-based canonical index ([n] in the manuscript)
	Title    string `json:"title"`
	Abstract string `json:"abstract"`
}

// Verdict is one checked sentence.
type Verdict struct {
	Sentence  string   `json:"sentence"`
	Citations []int    `json:"citations"`
	Sources   []string `json:"sources"`
	Verdict   string   `json:"verdict"` // supported | partial | unsupported
	Reason    string   `json:"reason"`
}

// Report is the claim-check/1 artifact content. Deterministic for a given
// (manuscript, corpus, verdicts) triple apart from the verifier block.
type Report struct {
	SchemaVersion    string    `json:"schema_version"`
	GeneratorVersion string    `json:"generator_version"`
	Verifier         string    `json:"verifier"`
	SentencesChecked int       `json:"sentences_checked"`
	Supported        int       `json:"supported"`
	Partial          int       `json:"partial"`
	Unsupported      int       `json:"unsupported"`
	Verdicts         []Verdict `json:"verdicts"`
}

const schemaVersion = "claim-check/1"

var (
	citationPattern = regexp.MustCompile(`\[(\d{1,2})\]`)
	headingPattern  = regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)
)

// SplitClaims cuts the numbered manuscript body into citable sentences:
// the reference list and boundary marker are stripped, headings dropped, and
// only sentences that actually cite [n] are kept (uncited synthesis is not a
// citation-support problem).
func SplitClaims(manuscript string) []string {
	body := manuscript
	if cut := strings.Index(body, "# 参考文献"); cut >= 0 {
		body = body[:cut]
	}
	body = strings.Join(dropLines(body, "> 证据边界"), "\n")
	body = headingPattern.ReplaceAllString(body, "")
	var claims []string
	for _, raw := range strings.FieldsFunc(body, func(r rune) bool {
		return r == '。' || r == '！' || r == '？' || r == '!' || r == '?' || r == '\n'
	}) {
		sentence := strings.TrimSpace(raw)
		if sentence == "" || !citationPattern.MatchString(sentence) || len([]rune(sentence)) < 8 {
			continue
		}
		if !strings.HasSuffix(sentence, "。") && !strings.HasSuffix(sentence, "！") && !strings.HasSuffix(sentence, "？") {
			sentence += "。"
		}
		claims = append(claims, sentence)
	}
	return claims
}

func dropLines(text, prefix string) []string {
	var kept []string
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

// SourceRefsFromIndexes projects corpus sources into verifier refs for the
// given 1-based indexes, silently skipping out-of-range ones.
func SourceRefsFromIndexes(sources []SourceRef, indexes []int) []SourceRef {
	seen := map[int]bool{}
	var refs []SourceRef
	for _, index := range indexes {
		if index < 1 || index > len(sources) || seen[index] {
			continue
		}
		seen[index] = true
		refs = append(refs, sources[index-1])
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Index < refs[j].Index })
	return refs
}

// claimChunkSize bounds how many sentences go into one verification call. The
// verification model is a reasoning model, so a single call over the whole
// manuscript's cited sentences can exceed any reasonable timeout; chunking
// keeps every call bounded and independently retryable.
const claimChunkSize = 10

// Check verifies the numbered manuscript in bounded chunks and merges the
// verdicts. Report-only: it never mutates the manuscript. A chunk that fails
// after retries is skipped (its sentences are simply absent from the report)
// rather than failing the whole pass, so one flaky relay call cannot lose the
// rest of the verification.
func (v *Verifier) Check(ctx context.Context, manuscript, generatorVersion string, sources []SourceRef) (*Report, error) {
	claims := SplitClaims(manuscript)
	if len(claims) == 0 {
		return nil, fmt.Errorf("claimverify: manuscript has no citable sentences")
	}
	catalog := strings.Builder{}
	for _, src := range sources {
		fmt.Fprintf(&catalog, "[%d] %s：%s\n", src.Index, src.Title, src.Abstract)
	}
	system := "你是独立的事实核查员，与正文作者无关。你只做支撑性判定，不改写正文。" +
		"判定口径：supported=句子内容被所引来源摘要明确陈述；partial=方向一致但强度/范围超出摘要（如把相关说成因果）；" +
		"unsupported=摘要中没有该陈述，或与摘要矛盾。不确定时判 partial。"
	report := &Report{SchemaVersion: schemaVersion, GeneratorVersion: generatorVersion,
		Verifier: v.cfg.Model, Verdicts: []Verdict{}}
	valid := map[string]bool{"supported": true, "partial": true, "unsupported": true}

	var lastErr error
	for start := 0; start < len(claims); start += claimChunkSize {
		end := min(start+claimChunkSize, len(claims))
		chunk := claims[start:end]
		listed := strings.Builder{}
		for i, claim := range chunk {
			cites := citationPattern.FindAllStringSubmatch(claim, -1)
			indexes := make([]string, 0, len(cites))
			for _, c := range cites {
				indexes = append(indexes, c[1])
			}
			fmt.Fprintf(&listed, "%d. （引用 [%s]）%s\n", i, strings.Join(indexes, ","), claim)
		}
		user := fmt.Sprintf(
			"对下面每一条编号句子：找到它引用的来源摘要，判定句子内容是否被该摘要支撑。"+
				"只返回 JSON：{\"verdicts\":[{\"index\":0,\"verdict\":\"supported|partial|unsupported\",\"reason\":\"10字内的依据\"}]}，"+
				"index 对应句子编号，必须逐条给出。\n\n唯一来源目录：\n%s\n编号句子：\n%s",
			catalog.String(), listed.String())
		raw, err := v.client.completeJSON(ctx, system, user)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			lastErr = err
			continue
		}
		var parsed struct {
			Verdicts []struct {
				Index   int    `json:"index"`
				Verdict string `json:"verdict"`
				Reason  string `json:"reason"`
			} `json:"verdicts"`
		}
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			lastErr = fmt.Errorf("claimverify: verifier response is not JSON: %w", err)
			continue
		}
		for _, entry := range parsed.Verdicts {
			if entry.Index < 0 || entry.Index >= len(chunk) || !valid[entry.Verdict] {
				continue
			}
			claim := chunk[entry.Index]
			indexes := citationIndexes(claim)
			var titles []string
			for _, ref := range SourceRefsFromIndexes(sources, indexes) {
				titles = append(titles, fmt.Sprintf("[%d] %s", ref.Index, ref.Title))
			}
			report.Verdicts = append(report.Verdicts, Verdict{
				Sentence: claim, Citations: indexes, Sources: titles,
				Verdict: entry.Verdict, Reason: entry.Reason,
			})
			switch entry.Verdict {
			case "supported":
				report.Supported++
			case "partial":
				report.Partial++
			default:
				report.Unsupported++
			}
		}
	}
	report.SentencesChecked = len(report.Verdicts)
	if report.SentencesChecked == 0 && lastErr != nil {
		return nil, lastErr
	}
	return report, nil
}

func citationIndexes(sentence string) []int {
	var out []int
	for _, m := range citationPattern.FindAllStringSubmatch(sentence, -1) {
		var n int
		if _, err := fmt.Sscanf(m[1], "%d", &n); err == nil {
			out = append(out, n)
		}
	}
	return out
}

// Verifier bundles the config with its HTTP client.
type Verifier struct {
	cfg    Config
	client *llmClient
}

// New validates required fields and returns a verifier. The caller (the
// production assembly in server) is responsible for ValidateBaseURL — tests
// legitimately use loopback httptest servers.
func New(cfg Config) (*Verifier, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" || strings.TrimSpace(cfg.APIKey) == "" || strings.TrimSpace(cfg.Model) == "" {
		return nil, fmt.Errorf("claimverify: base_url, api_key and model are all required")
	}
	if cfg.TimeoutMS <= 0 {
		cfg.TimeoutMS = 120000
	}
	return &Verifier{cfg: cfg, client: &llmClient{
		baseURL: strings.TrimRight(cfg.BaseURL, "/"), apiKey: cfg.APIKey,
		model: cfg.Model, timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond,
		maxAttempts: 5, backoff: 30 * time.Second,
		client: &http.Client{Timeout: time.Duration(cfg.TimeoutMS) * time.Millisecond},
	}}, nil
}
