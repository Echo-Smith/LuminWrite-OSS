package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/arreview"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
)

// AR-012 live sidecar acceptance (T10, docs/releases/2026-09-12-ar012-candidate-acceptance.md):
// the previously SKIPPED item "真实模型端到端" — one real POST → 202 → the
// worker drives the synchronous sidecar pipeline against a real
// OpenAI-compatible model → poll → artifacts export → idempotent replay.
//
// The input is *not* a hand-built fixture: the run is produced by the same
// offline research-review chain the workbench ships (T06 E2E harness —
// scripted scholar worker, deterministic writer, two human gates decided via
// HTTP), so the frozen evidence pack and approved outline are exactly what a
// completed research run carries. Only the sidecar's generation model is
// live; the repository, this test, and every report stay credential-free.
//
// Environment gate (skips otherwise):
//
//	AR012_LIVE_SIDECAR_URL   e.g. http://ar012-live-sidecar:8020 (private network)
//	AR012_LIVE_EXCHANGE_DIR  the host directory mounted as the sidecar's project root
//
// see scripts/run-ar012-live-acceptance.sh for the deployment wiring.

const arReviewLivePapers = 6 // ≥ MinCorpusSources after BuildCorpus exclusions

func TestArReviewLiveSidecarEndToEnd(t *testing.T) {
	sidecarURL := strings.TrimSpace(os.Getenv("AR012_LIVE_SIDECAR_URL"))
	exchangeDir := strings.TrimSpace(os.Getenv("AR012_LIVE_EXCHANGE_DIR"))
	if sidecarURL == "" || exchangeDir == "" {
		t.Skip("set AR012_LIVE_SIDECAR_URL and AR012_LIVE_EXCHANGE_DIR for the AR-012 live sidecar acceptance")
	}
	if info, err := os.Stat(exchangeDir); err != nil || !info.IsDir() {
		t.Fatalf("AR012_LIVE_EXCHANGE_DIR must be an existing directory: %v", err)
	}

	// 1. A real completed research run through the two confirmed gates.
	h := newT06E2EHarness(t, arReviewLivePapers, 0)
	// The fake worker upgrades to corpus-eligible output (DOI + year +
	// verified full-text quotes above the sidecar's 120-char floor) so the
	// frozen pack survives BuildCorpus honest exclusion with ≥5 sources.
	h.worker.withFullText = true
	h.worker.corpusEligible = true
	fixture := h.fixture(t)
	envelope := h.buildResearchEnvelope(t, fixture)
	runID := h.createResearchRun(t, fixture, envelope)
	evidenceGate := h.advanceToGate(t, runID, "evidence")
	h.decideGate(t, runID, evidenceGate, envelope)
	outlineGate := h.advanceToGate(t, runID, "outline")
	h.decideGate(t, runID, outlineGate, envelope)
	if status := h.driveToTerminal(t, runID, 3); status != "completed" {
		t.Fatalf("research run ended as %q, want completed", status)
	}
	artifactsBefore, err := h.store.ListRunArtifacts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}

	// 2. Mount the AR-012 evaluation service onto the same server and start
	// the single-worker loop (production shape: one claimer, five-second scan).
	h.server.arReview = newArReviewService(h.store, config.ArReviewConfig{
		Enabled: true, SidecarURL: sidecarURL,
		// The live acceptance seeds a fixed corpus, so deployment corpus
		// floors are explicitly off here.
		TimeoutMS: 1500000, ExchangeDir: exchangeDir,
		MinSources: 0, MinAbstractRunes: 0,
	})
	if h.server.arReview == nil {
		t.Fatal("AR-012 service did not mount")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go h.server.arReview.Serve(ctx)

	// 3. POST → 202 (fresh job), never a synchronous wait.
	rec := arReviewLiveRequest(h.router, http.MethodPost, "/api/v2/runs/"+runID+"/research/ar012-candidate", h.token)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST candidate: want 202, got %d: %s", rec.Code, rec.Body.String())
	}
	created := arReviewLiveData(t, rec)
	jobID, _ := created["job_id"].(string)
	if jobID == "" {
		t.Fatalf("202 view missing job_id: %v", created)
	}
	t.Logf("ar012 live job=%s status=%v project=%v warnings=%v",
		jobID, created["status"], created["surrogate_project_id"], created["corpus_warnings"])

	// 4. Poll until terminal: the sidecar runs five-plus synchronous model
	// calls end to end; the client timeout bounds one dispatch at 25m.
	deadline := time.Now().Add(45 * time.Minute)
	var job map[string]any
	for {
		rec := arReviewLiveRequest(h.router, http.MethodGet, "/api/v2/runs/"+runID+"/research/ar012-candidate", h.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET candidate: want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		job = arReviewLiveData(t, rec)
		status, _ := job["status"].(string)
		if status == "completed" {
			break
		}
		switch status {
		case "failed", "cancelled", "outcome_unknown":
			t.Fatalf("sidecar job ended as %s: code=%v message=%v remote=%v",
				status, job["error_code"], job["error_message"], job["remote_run_id"])
		}
		if time.Now().After(deadline) {
			t.Fatalf("sidecar job %s still %v after 45m (remote_run_id=%v)", jobID, status, job["remote_run_id"])
		}
		time.Sleep(20 * time.Second)
	}
	t.Logf("ar012 live job completed: remote=%v artifacts=%v usage=%v",
		job["remote_run_id"], job["artifact_refs"], job["usage"])

	// 5. All five kinds export through the owner surface, hash-verifiable.
	refs := map[string]map[string]any{}
	for _, entry := range job["artifact_refs"].([]any) {
		ref := entry.(map[string]any)
		refs[fmt.Sprint(ref["kind"])] = ref
	}
	for _, kind := range []string{"manuscript", "citation_map", "receipt", "quality", "metrics"} {
		if _, ok := refs[kind]; !ok {
			t.Fatalf("job missing artifact kind %q", kind)
		}
		rec := arReviewLiveRequest(h.router, http.MethodGet,
			"/api/v2/runs/"+runID+"/research/ar012-candidate/artifacts/"+kind, h.token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET artifact %s: want 200, got %d: %s", kind, rec.Code, rec.Body.String())
		}
		body := rec.Body.Bytes()
		if len(body) == 0 {
			t.Fatalf("artifact %s empty", kind)
		}
		if got, want := arreview.HashContent(body), fmt.Sprint(refs[kind]["content_hash"]); got != want {
			t.Fatalf("artifact %s rehash mismatch: %s vs %s", kind, got, want)
		}
		if header := rec.Header().Get("X-Content-Hash"); header != fmt.Sprint(refs[kind]["content_hash"]) {
			t.Fatalf("artifact %s hash header mismatch: %s", kind, header)
		}
		t.Logf("ar012 live artifact %s: %d bytes media=%v", kind, len(body), refs[kind]["media_type"])
	}

	// 6. The live manuscript is a real generation, and usage is the model's
	// own reported numbers — never a fabricated zero.
	manuscript, err := arReviewLiveFetchArtifact(h.router, runID, "manuscript", h.token)
	if err != nil {
		t.Fatal(err)
	}
	if cjk := arReviewLiveCountCJK(manuscript); cjk < 2500 {
		t.Fatalf("manuscript too short to be a real generation: %d CJK runes", cjk)
	}
	var citationMap map[string]any
	if err := json.Unmarshal([]byte(manuscript), &citationMap); err == nil {
		t.Fatal("manuscript must be markdown, not JSON")
	}
	usage, _ := job["usage"].(map[string]any)
	if usage["measured"] != true {
		t.Fatalf("live run must report measured usage: %v", usage)
	}
	t.Logf("ar012 live usage: %v", usage)

	// 7. A18 in the live surface: the candidate never touched the run ledger.
	artifactsAfter, err := h.store.ListRunArtifacts(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifactsAfter) != len(artifactsBefore) {
		t.Fatalf("A18 violation: run artifacts grew from %d to %d", len(artifactsBefore), len(artifactsAfter))
	}

	// 8. Replay: identical frozen inputs return the original job (200, same
	// id) — a completed 650-second-class pipeline is never re-dispatched.
	rec = arReviewLiveRequest(h.router, http.MethodPost, "/api/v2/runs/"+runID+"/research/ar012-candidate", h.token)
	if rec.Code != http.StatusOK {
		t.Fatalf("replay POST: want 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if replayed := arReviewLiveData(t, rec); replayed["job_id"] != jobID {
		t.Fatalf("replay must return the original job: %v vs %s", replayed["job_id"], jobID)
	}
}

// ─── HTTP helpers ───

func arReviewLiveRequest(router http.Handler, method, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func arReviewLiveData(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Success bool           `json:"success"`
		Data    map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode envelope: %v — %s", err, rec.Body.String())
	}
	if !envelope.Success || envelope.Data == nil {
		t.Fatalf("unsuccessful response: %s", rec.Body.String())
	}
	return envelope.Data
}

func arReviewLiveFetchArtifact(router http.Handler, runID, kind, token string) (string, error) {
	rec := arReviewLiveRequest(router, http.MethodGet,
		"/api/v2/runs/"+runID+"/research/ar012-candidate/artifacts/"+kind, token)
	if rec.Code != http.StatusOK {
		return "", fmt.Errorf("artifact %s: %d — %s", kind, rec.Code, rec.Body.String())
	}
	return rec.Body.String(), nil
}

func arReviewLiveCountCJK(text string) int {
	count := 0
	for _, r := range text {
		if r >= 0x4E00 && r <= 0x9FFF {
			count++
		}
	}
	return count
}
