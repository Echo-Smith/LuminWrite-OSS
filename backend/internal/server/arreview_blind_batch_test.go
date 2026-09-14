package server

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
)

// AR-012 twelve-case blind evaluation generator (design.md §8: at least 12
// cases = 3 subject-vocabulary groups × 2 material sufficiencies × 2 corpus
// lengths; blind on 论点组织/引用支持/校准表达/可读性).
//
// For each matrix cell it drives the same offline T06 research chain (scripted
// scholar worker, deterministic draft writer, two human gates) to a completed
// run, then the real AR-012 sidecar pipeline against a real model. It exports
// the primary draft and the candidate as anonymized A.md / B.md (coin-flip
// order per case), a brief carrying the shared research question, the approved
// outline and the numbered corpus, and writes an answer key + cost sheet the
// evaluator must not open before scoring.
//
// Methodological limit recorded honestly: the primary draft here is the T06
// deterministic stub (the first-version research path is not deployed), so
// pair-level length asymmetry is expected; the batch evidences pipeline
// reproducibility and produces the twelve real materials, not a fair
// writer-vs-writer quality contest.
//
// Output layout is a STATIC relative directory under the process working
// directory (run the compiled test binary from the mounted pack dir; the
// environment variable below only gates the test, it never joins a path).
//
// Environment gate (skips otherwise):
//
//	AR012_BLIND_BATCH=1      opt in to the twelve-case batch
//	AR012_LIVE_SIDECAR_URL   + AR012_LIVE_EXCHANGE_DIR  as in the live acceptance
//	TEST_DATABASE_URL        throwaway PostgreSQL (dbtest per case)

const ar012BlindPackDir = "ar012-blind-pack" // static relative; no operator input

func ar012BlindOnlyCases(t *testing.T) map[int]bool {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("AR012_BLIND_ONLY"))
	if raw == "" {
		return nil
	}
	set := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 1 || n > 12 {
			t.Fatalf("AR012_BLIND_ONLY must be comma-separated case numbers 1..12, got %q", raw)
		}
		set[n] = true
	}
	return set
}

func ar012BlindSelected(set map[int]bool, number int) bool { return set[number] }

// ar012BlindLatestByKey folds the append-only answer key down to one row per
// case (the last write wins), so targeted re-runs refresh cells in place.
func ar012BlindLatestByKey(t *testing.T) []ar012BlindCaseResult {
	t.Helper()
	file, err := os.Open(ar012BlindPath(t, "answer-key.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	latest := map[int]ar012BlindCaseResult{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var probe map[string]any
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			continue
		}
		if _, isEvent := probe["event"]; isEvent {
			continue
		}
		var row ar012BlindCaseResult
		if err := json.Unmarshal([]byte(line), &row); err != nil || row.Case < 1 || row.Case > 12 {
			continue
		}
		latest[row.Case] = row
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	out := make([]ar012BlindCaseResult, 0, 12)
	for n := 1; n <= 12; n++ {
		if row, ok := latest[n]; ok {
			out = append(out, row)
			continue
		}
		out = append(out, ar012BlindCaseResult{Case: n, Subject: "—", Length: "—", Error: "案例未生成（从未运行）"})
	}
	return out
}

type ar012BlindSubject struct {
	slug     string
	label    string
	question string
	terms    []string
}

// Subjects map to real OpenAlex pools (ar012RealCorpus) and carry the frozen
// contract's research question plus the Chinese coverage vocabulary the
// candidate's spec gate will demand.
var ar012BlindSubjects = []ar012BlindSubject{
	{slug: "governance", label: "生成式AI与学术写作",
		question: "生成式人工智能应如何融入高校学术写作与教育评价，才能兼顾效率提升与学术诚信？",
		terms:    []string{"生成式人工智能", "学术诚信", "写作"}},
	{slug: "policy", label: "公共项目评估",
		question: "学校和社区层面的公共干预项目对学生发展有哪些真实效果，评估方法应如何选择？",
		terms:    []string{"随机对照试验", "政策评估", "因果效应"}},
	{slug: "learning", label: "家长参与与学业成就",
		question: "家长参与如何影响子女的学业成就，哪些参与形式最为有效？",
		terms:    []string{"家长参与", "学业成就", "元分析"}},
}

type ar012BlindLength struct {
	slug      string
	quote     int
	sentences int
}

var ar012BlindLengths = []ar012BlindLength{
	{slug: "短摘要", quote: 600, sentences: 8},
	{slug: "长摘要", quote: 1200, sentences: 18},
}

func ar012BlindPath(t *testing.T, elems ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{ar012BlindPackDir}, elems...)...)
	abs, err := filepath.Abs(path)
	if err != nil || !filepath.IsAbs(abs) {
		t.Fatalf("blind pack path unresolved: %v", err)
	}
	return path
}

func TestAr012BlindEvalBatch(t *testing.T) {
	if os.Getenv("AR012_BLIND_BATCH") != "1" {
		t.Skip("set AR012_BLIND_BATCH=1 to generate the twelve-case blind pack")
	}
	sidecarURL := strings.TrimSpace(os.Getenv("AR012_LIVE_SIDECAR_URL"))
	exchangeDir := strings.TrimSpace(os.Getenv("AR012_LIVE_EXCHANGE_DIR"))
	if sidecarURL == "" || exchangeDir == "" {
		t.Skip("AR012_LIVE_SIDECAR_URL and AR012_LIVE_EXCHANGE_DIR are required")
	}
	if err := os.MkdirAll(ar012BlindPackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A single-case re-run (AR012_BLIND_ONLY=2) appends to the existing
	// answer key and leaves the full pack/sheets untouched; a full batch
	// starts the key fresh. The deliverable assembly takes, per case, the
	// last completed line in the key.
	only := ar012BlindOnlyCases(t)
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	if len(only) > 0 {
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}
	keyFile, err := os.OpenFile(ar012BlindPath(t, "answer-key.jsonl"), flags, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer keyFile.Close()
	enc := json.NewEncoder(keyFile)

	cases := 0
	results := make([]ar012BlindCaseResult, 0, 12)
	for _, subject := range ar012BlindSubjects {
		poolLen := len(ar012RealCorpus[subject.slug])
		for _, papers := range []int{6, 8} {
			for _, length := range ar012BlindLengths {
				cases++
				if len(only) > 0 && !ar012BlindSelected(only, cases) {
					continue
				}
				results = append(results, ar012RunBlindCase(t, enc, cases, subject, min(papers, poolLen), length, sidecarURL, exchangeDir))
			}
		}
	}
	// Sheets rebuild from the whole answer key, so a targeted re-run (e.g.
	// after a gate failure) refreshes a cell without regenerating the pack.
	final := ar012BlindLatestByKey(t)
	ar012WriteBlindSheets(t, final)
	ok := 0
	for _, r := range final {
		if r.Completed {
			ok++
		}
	}
	t.Logf("ar012 blind batch done: %d/12 cases completed (answer key: %s)", ok, ar012BlindPath(t, "answer-key.jsonl"))
	if ok == 0 {
		t.Fatalf("blind batch produced zero completed cases")
	}
}

type ar012BlindCaseResult struct {
	Case      int            `json:"case"`
	Subject   string         `json:"subject"`
	Papers    int            `json:"papers"`
	Length    string         `json:"length"`
	Candidate string         `json:"candidate_side"`
	Completed bool           `json:"completed"`
	Error     string         `json:"error,omitempty"`
	JobID     string         `json:"job_id,omitempty"`
	Seconds   int64          `json:"sidecar_seconds,omitempty"`
	Usage     map[string]any `json:"usage,omitempty"`
	Metrics   map[string]any `json:"metrics,omitempty"`
}

func ar012RunBlindCase(t *testing.T, enc *json.Encoder, number int,
	subject ar012BlindSubject, papers int, length ar012BlindLength,
	sidecarURL, exchangeDir string) ar012BlindCaseResult {
	t.Helper()
	result := ar012BlindCaseResult{Case: number, Subject: subject.label, Papers: papers, Length: length.slug}
	caseDir := ar012BlindPath(t, fmt.Sprintf("case-%02d", number))
	if err := os.MkdirAll(caseDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Up to three attempts; each retry prefixes the fetched body with a CJK
	// salt — a genuinely different pack, because the sidecar keys runs by
	// content hash and a failed run must never be "replayed". Gate failures
	// on length are model sampling variance, so the salt is a fresh draw.
	salts := []string{"", "（再）", "（再再）"}
	for index, salt := range salts {
		attempt := index + 1
		job, inputs, service, router, token, runID, elapsed, userID :=
			ar012DriveBlindCase(t, subject, papers, length, salt, sidecarURL, exchangeDir)
		status, _ := job["status"].(string)
		if status != "completed" {
			result.Error = fmt.Sprintf("job %v ended %s: %v", job["job_id"], status, job["error_message"])
			t.Logf("blind case %02d attempt %d: %v", number, attempt, result.Error)
			_ = enc.Encode(map[string]any{"event": "attempt_failed", "case": number, "attempt": attempt, "detail": result.Error})
			continue
		}
		manuscript, err := arReviewLiveFetchArtifact(router, runID, "manuscript", token)
		if err != nil {
			result.Error = err.Error()
			continue
		}
		metricsJSON, err := arReviewLiveFetchArtifact(router, runID, "metrics", token)
		if err != nil {
			result.Error = err.Error()
			continue
		}
		_ = json.Unmarshal([]byte(metricsJSON), &result.Metrics)
		for _, kind := range []string{"citation_map", "receipt", "quality"} {
			body, err := arReviewLiveFetchArtifact(router, runID, kind, token)
			if err != nil {
				result.Error = err.Error()
				break
			}
			if err := os.WriteFile(filepath.Join(caseDir, "sidecar-"+kind+".json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if result.Error != "" {
			continue
		}
		primary, _, err := service.latestArtifact(context.Background(), runID, "full_draft")
		if err != nil {
			result.Error = err.Error()
			continue
		}
		n, err := rand.Int(rand.Reader, big.NewInt(2))
		if err != nil {
			t.Fatal(err)
		}
		candidateIsA := n.Int64() == 0
		result.Candidate = "B"
		sideA, sideB := string(primary), manuscript
		if candidateIsA {
			result.Candidate = "A"
			sideA, sideB = manuscript, string(primary)
		}
		if err := os.WriteFile(filepath.Join(caseDir, "A.md"), []byte(sideA), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(caseDir, "B.md"), []byte(sideB), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = userID
		ar012WriteCaseBrief(t, caseDir, subject, papers, length, inputs)
		result.Completed = true
		result.JobID, _ = job["job_id"].(string)
		result.Usage, _ = job["usage"].(map[string]any)
		result.Seconds = int64(elapsed.Round(time.Second).Seconds())
		if err := enc.Encode(result); err != nil {
			t.Fatal(err)
		}
		t.Logf("blind case %02d (%s/%d篇/%s) done: candidate=%s job=%s %ds",
			number, subject.slug, papers, length.slug, result.Candidate, result.JobID, result.Seconds)
		return result
	}
	_ = enc.Encode(result)
	return result
}

// ar012BlindAdvanceToGate is advanceToGate with a diagnostic dump: when the
// research run settles without ever parking at the requested gate, print the
// run projection and the research view so the batch failure is debuggable
// after the throwaway database is gone.
func ar012BlindAdvanceToGate(t *testing.T, h *t06Harness, runID, kind string) string {
	t.Helper()
	deadline := time.Now().Add(120 * time.Second)
	var lastResume time.Time
	for time.Now().Before(deadline) {
		code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/runs/"+runID+"/research", "", nil)
		if code == http.StatusOK {
			if active, _ := dataOf(t, payload)["active_gate"].(map[string]any); active != nil {
				if gateKind, _ := active["gate_kind"].(string); gateKind == kind {
					gateID, _ := active["gate_id"].(string)
					if gateID != "" {
						return gateID
					}
				}
			}
		}
		if status := h.httpStatus(t, runID); status == "paused" && time.Since(lastResume) > time.Second {
			lastResume = time.Now()
			_, _ = t02APIRequest(t, h.router, h.token, http.MethodPost, "/api/v2/runs/"+runID+"/resume",
				t02APIIdempotencyKey("advance-"+fmt.Sprint(time.Now().UnixNano())), map[string]any{})
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Logf("blind diagnostic: run %s settled at %q without a pending %q gate", runID, h.httpStatus(t, runID), kind)
	if code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/runs/"+runID, "", nil); code == http.StatusOK {
		t.Logf("blind diagnostic: run view: %s", payload)
	}
	if code, payload := t02APIRequest(t, h.router, h.token, http.MethodGet, "/api/v2/runs/"+runID+"/research", "", nil); code == http.StatusOK {
		t.Logf("blind diagnostic: research view: %s", payload)
	}
	t.Fatalf("run %s never reached a pending %q gate", runID, kind)
	return ""
}

// ar012DriveBlindCase builds a fresh harness + completed research run for one
// matrix cell (optionally salted), mounts the AR-012 service, drives the
// candidate job to a terminal state and returns the final view plus the frozen
// inputs needed for the brief.
func ar012DriveBlindCase(t *testing.T, subject ar012BlindSubject, papers int, length ar012BlindLength,
	salt, sidecarURL, exchangeDir string) (map[string]any, frozenInputs, *arReviewService, http.Handler, string, string, time.Duration, string) {
	t.Helper()
	h := newT06E2EHarness(t, papers, 0)
	h.worker.withFullText = true
	h.worker.corpusEligible = true
	h.worker.subjectTerms = subject.terms
	h.worker.quoteCodepoints = length.quote
	h.worker.fullTextSentences = length.sentences
	h.worker.caseSalt = salt
	// Genuine evidence: the frozen pack is built from real OpenAlex works
	// (first N of the subject pool), so the blind evaluator can check every
	// claim against the actual abstracts.
	pool := ar012RealCorpus[subject.slug]
	if len(pool) > papers {
		pool = pool[:papers]
	}
	h.worker.realPapers = pool
	// fixtureMutate seals the v1.1 research-review contract with the
	// subject's research question, so the frozen task matches the corpus.
	fixture := h.fixtureMutate(t, func(c *writingkernel.WritingContract) {
		c.Content.Topic = subject.question
		c.Content.CentralQuestion = subject.question
	})
	envelope := h.buildResearchEnvelope(t, fixture)
	for _, n := range envelope.ExecutablePlan.Nodes {
		t.Logf("blind plan node: %s kind=%s cap=%s", n.NodeID, n.Kind, n.Capability)
	}
	runID := h.createResearchRun(t, fixture, envelope)
	evidenceGate := ar012BlindAdvanceToGate(t, h, runID, "evidence")
	h.decideGate(t, runID, evidenceGate, envelope)
	outlineGate := h.advanceToGate(t, runID, "outline")
	h.decideGate(t, runID, outlineGate, envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("research run ended as %q, want completed", status)
	}
	h.server.arReview = newArReviewService(h.store, config.ArReviewConfig{
		Enabled: true, SidecarURL: sidecarURL,
		// The evaluation tool drives the full matrix including thin-corpus
		// cells, so the deployment's corpus-budget floors are explicitly off.
		TimeoutMS: 1500000, ExchangeDir: exchangeDir,
		MinSources: 0, MinAbstractRunes: 0,
		// Optional different-source verifier, enabled via env for the batch.
		Verify: config.ArReviewVerifyConfig{
			BaseURL:   os.Getenv("AR_REVIEW_VERIFY_BASE_URL"),
			APIKey:    os.Getenv("AR_REVIEW_VERIFY_API_KEY"),
			Model:     os.Getenv("AR_REVIEW_VERIFY_MODEL"),
			TimeoutMS: 360000,
		},
	})
	if h.server.arReview == nil {
		t.Fatal("AR-012 service did not mount")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go h.server.arReview.Serve(ctx)

	rec := arReviewLiveRequest(h.router, http.MethodPost, "/api/v2/runs/"+runID+"/research/ar012-candidate", h.token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST candidate: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	job := arReviewLiveData(t, rec)
	started := time.Now()
	deadline := started.Add(30 * time.Minute)
	for {
		time.Sleep(15 * time.Second)
		rec := arReviewLiveRequest(h.router, http.MethodGet, "/api/v2/runs/"+runID+"/research/ar012-candidate", h.token)
		job = arReviewLiveData(t, rec)
		status, _ := job["status"].(string)
		terminal := status == "completed" || status == "failed" || status == "cancelled" || status == "outcome_unknown"
		if terminal || time.Now().After(deadline) {
			var inputs frozenInputs
			if status == "completed" {
				loaded, err := h.server.arReview.loadFrozenInputs(context.Background(), h.userID, runID)
				if err != nil {
					t.Fatalf("completed job lost its frozen inputs: %v", err)
				}
				inputs = loaded
			}
			return job, inputs, h.server.arReview, h.router, h.token, runID, time.Since(started), h.userID
		}
	}
}

func ar012WriteCaseBrief(t *testing.T, caseDir string, subject ar012BlindSubject, papers int, length ar012BlindLength, inputs frozenInputs) {
	t.Helper()
	var out strings.Builder
	out.WriteString("# 盲评简报 · 案例背景（A/B 共用）\n\n")
	fmt.Fprintf(&out, "- 研究问题：%s\n\n", inputs.centralQuestion)
	fmt.Fprintf(&out, "- 词表组：%s ｜ 语料充分度：%d 篇 ｜ 语料长度：%s\n\n", subject.label, papers, length.slug)
	out.WriteString("## 批准提纲（两稿的共同要求）\n\n")
	for i, section := range inputs.outline.Sections {
		fmt.Fprintf(&out, "%d. **%s** —— %s\n", i+1, section.Title, section.CentralPoint)
	}
	out.WriteString("\n## 冻结证据语料（稿件中的 [编号] 即对应下列条目）\n\n")
	byPaper := map[string]writingkernel.Evidence{}
	for _, ev := range inputs.pack.Evidence {
		if len([]rune(ev.Quote)) > len([]rune(byPaper[ev.PaperID].Quote)) {
			byPaper[ev.PaperID] = ev
		}
	}
	sorted := append([]writingkernel.PaperEvidence(nil), inputs.pack.Papers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].PaperID < sorted[j].PaperID })
	num := 0
	for _, p := range sorted {
		ev, ok := byPaper[p.PaperID]
		if !ok {
			continue
		}
		num++
		fmt.Fprintf(&out, "**[%d] %s**（%s，%s%s）\n\n> %s（证据范围：%s）\n\n",
			num, p.Bibliography.Title, blindVenue(p), blindYear(p), blindDOI(p), ev.Quote, ev.EvidenceScope)
	}
	out.WriteString("\n---\n\n说明：两稿针对同一研究问题、同一批准提纲、同一证据语料写成。评分不参考引用格式差异，只判断论点组织、引用支持、校准表达与可读性。\n")
	if err := os.WriteFile(filepath.Join(caseDir, "brief.md"), []byte(out.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func blindVenue(p writingkernel.PaperEvidence) string {
	if p.Bibliography.Venue != nil {
		return *p.Bibliography.Venue
	}
	return "venue 未记录"
}

func blindYear(p writingkernel.PaperEvidence) string {
	if p.Bibliography.Year != nil {
		return fmt.Sprint(*p.Bibliography.Year)
	}
	return "年份未记录"
}

func blindDOI(p writingkernel.PaperEvidence) string {
	if p.Bibliography.DOI != nil {
		return "，DOI " + *p.Bibliography.DOI
	}
	return ""
}

func ar012WriteBlindSheets(t *testing.T, results []ar012BlindCaseResult) {
	t.Helper()
	var sheet strings.Builder
	sheet.WriteString("# AR-012 候选 12 案例人工盲评记录表\n\n")
	sheet.WriteString("日期：2026-09-12　评审人：____\n\n")
	sheet.WriteString("协议（design.md §8）：12 案例 = 3 词表组 × 2 材料充分度 × 2 语料长度；每案例先读 `case-NN/brief.md`（共同问题、批准提纲、证据语料），再对照 `A.md` 与 `B.md` 打分。\n\n")
	sheet.WriteString("评分方式：每维 1–5 分（5 最好）。另记两稿中**关键性无证据支持主张**的条数（编造的事实/数字/结论）。引用完整性另有机械核验，不必人工统计。\n\n")
	sheet.WriteString("盲评纪律：case-NN 目录里 A/B 顺序已随机化；`answer-key.jsonl` 与 `cost-sheet.md` 是答案单/成本单，全部案例打分完成前请勿打开。\n\n")
	for _, r := range results {
		fmt.Fprintf(&sheet, "## case-%02d ｜ %s ｜ %d 篇 ｜ %s\n\n", r.Case, r.Subject, r.Papers, r.Length)
		if !r.Completed {
			fmt.Fprintf(&sheet, "（本案例候选生成失败：%s —— 标注 SKIP，不计入盲评）\n\n---\n\n", r.Error)
			continue
		}
		sheet.WriteString("论点组织：A __ /5　B __ /5\n\n")
		sheet.WriteString("引用支持：A __ /5　B __ /5\n\n")
		sheet.WriteString("校准表达：A __ /5　B __ /5\n\n")
		sheet.WriteString("可读性：　A __ /5　B __ /5\n\n")
		sheet.WriteString("无支持主张条数：A __ 处　B __ 处\n\n")
		sheet.WriteString("总体更可信：☐ A　☐ B　☐ 平手\n\n")
		sheet.WriteString("一句话理由：＿＿＿＿＿＿＿＿＿＿＿＿\n\n---\n\n")
	}
	if err := os.WriteFile(ar012BlindPath(t, "盲评记录表.md"), []byte(sheet.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	var cost strings.Builder
	cost.WriteString("# 成本与耗时记录（自动生成，非盲评材料）\n\n")
	cost.WriteString("| case | 词表组 | 篇数 | 长度 | 候选侧 | 用时(s) | input tokens | output tokens | job |\n|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		in, outTok := "—", "—"
		if r.Usage != nil {
			in = fmt.Sprint(r.Usage["input_tokens"])
			outTok = fmt.Sprint(r.Usage["output_tokens"])
		}
		side := r.Candidate
		if !r.Completed {
			side = "—"
		}
		fmt.Fprintf(&cost, "| %02d | %s | %d | %s | %s | %d | %s | %s | %s |\n",
			r.Case, r.Subject, r.Papers, r.Length, side, r.Seconds, in, outTok, r.JobID)
	}
	if err := os.WriteFile(ar012BlindPath(t, "cost-sheet.md"), []byte(cost.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
