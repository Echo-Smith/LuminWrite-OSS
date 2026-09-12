package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/arreview"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// AR-012 candidate evaluation service (T10). One frozen evidence pack +
// approved outline → sidecar comparison candidate, stored as isolated
// content-addressed blobs referenced from the job row. Nothing in this file
// writes run artifacts, attempt-ledger rows, or document versions: a
// candidate structurally cannot enter the delivery path (A18) — the job
// endpoints below are the only read surface.
//
// The sidecar is Proprietary and deployed out-of-band (workspace-local fork;
// specs/research-review/ar012-sidecar.md). With the feature disabled or the
// URL unconfigured, the service is nil and every endpoint reports 503
// AR_REVIEW_UNAVAILABLE.

var (
	errArReviewDisabled       = errors.New("writing api: ar review sidecar is unavailable")
	errArReviewRunNotComplete = errors.New("writing api: ar review requires a completed research run")
	errArReviewInputsMissing  = errors.New("writing api: run lacks the frozen research artifacts an ar review comparison needs")
)

// arReviewArtifactKinds are the sidecar outputs imported per job. Each maps
// to its record artifacts entry; metrics is host-computed.
var arReviewArtifactKinds = []string{"manuscript", "citation_map", "receipt", "quality"}

type arReviewService struct {
	store    *writingstore.Store
	client   *arreview.Client
	exchange *arreview.Exchange
	cfg      config.ArReviewConfig
	now      func() time.Time
}

// newArReviewService builds the sidecar service, or nil when disabled. A
// broken exchange root disables the service too — fail closed, never half.
func newArReviewService(store *writingstore.Store, cfg config.ArReviewConfig) *arReviewService {
	if store == nil || !cfg.Enabled || cfg.SidecarURL == "" {
		return nil
	}
	client, err := arreview.NewClient(cfg.SidecarURL)
	if err != nil {
		slog.Error("ar review sidecar disabled: bad URL", "error", err)
		return nil
	}
	exchange, err := arreview.NewExchange(cfg.ExchangeDir)
	if err != nil {
		slog.Error("ar review sidecar disabled: exchange root", "error", err)
		return nil
	}
	return &arReviewService{store: store, client: client, exchange: exchange, cfg: cfg, now: time.Now}
}

// frozenInputs bundles everything a job needs, re-derivable at execution time
// because every source artifact is content-hash pinned.
type frozenInputs struct {
	centralQuestion string
	contractHash    string
	pack            writingkernel.ResearchEvidencePack
	packHash        string
	outline         writingkernel.ResearchOutline
	outlineHash     string
	draft           string
}

// RequestCandidate serves POST /runs/{runId}/research/ar012-candidate. It
// validates preconditions, derives the content-addressed identity, and
// creates the durable job. An identical replay returns the original job with
// replayed=true — never a second sidecar run.
func (s *arReviewService) RequestCandidate(ctx context.Context, ownerUserID, runID string) (writingstore.ArReviewJob, bool, error) {
	inputs, err := s.loadFrozenInputs(ctx, ownerUserID, runID)
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	corpus, warnings, err := arreview.BuildCorpus(inputs.pack, inputs.centralQuestion)
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	headings := outlineHeadings(inputs.outline)
	// Validate the derived spec at request time so a malformed outline fails
	// the POST, not a silently doomed worker run.
	if _, err := arreview.BuildSpecMapping(inputs.centralQuestion, headings, inputs.pack.Coverage.Topics, len(corpus.Sources)); err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	identity := arreview.IdempotencyInput{
		Owner:               ownerUserID,
		ContractHash:        inputs.contractHash,
		EvidencePackHash:    inputs.packHash,
		ApprovedOutlineHash: inputs.outlineHash,
		GeneratorVersion:    arreview.GeneratorVersion,
		Mode:                arreview.ModeGenerateOnly,
	}
	projectID, err := identity.SurrogateProjectID()
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	key, err := identity.IdempotencyKey()
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	inputHash, err := identity.Digest()
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	job, err := s.store.CreateArReviewJob(ctx, writingstore.ArReviewJob{
		OwnerUserID:         ownerUserID,
		RunID:               runID,
		SurrogateProjectID:  projectID,
		IdempotencyKey:      key,
		CentralQuestion:     inputs.centralQuestion,
		ContractHash:        inputs.contractHash,
		EvidencePackHash:    inputs.packHash,
		ApprovedOutlineHash: inputs.outlineHash,
		GeneratorVersion:    arreview.GeneratorVersion,
		InputHash:           inputHash,
		CorpusWarnings:      warnings,
	})
	if errors.Is(err, writingstore.ErrArReviewJobReplayed) {
		return job, true, nil
	}
	if err != nil {
		return writingstore.ArReviewJob{}, false, err
	}
	return job, false, nil
}

// loadFrozenInputs validates the run is a completed research run and loads
// the exact artifacts the comparison needs (owner-first 404 semantics).
func (s *arReviewService) loadFrozenInputs(ctx context.Context, ownerUserID, runID string) (frozenInputs, error) {
	run, err := s.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return frozenInputs{}, errResearchResourceNotFound
	}
	if run.OwnerUserID != ownerUserID {
		return frozenInputs{}, errResearchResourceNotFound
	}
	if run.Status != "completed" {
		return frozenInputs{}, fmt.Errorf("%w: run %s is %s", errArReviewRunNotComplete, runID, run.Status)
	}
	contract, err := s.store.GetContract(ctx, run.ContractID, run.ContractVersion)
	if err != nil {
		return frozenInputs{}, errResearchResourceNotFound
	}
	inputs := frozenInputs{
		centralQuestion: contract.Contract.Content.CentralQuestion,
		contractHash:    run.ContractHash,
	}
	packBody, packHash, err := s.latestArtifact(ctx, runID, "research_evidence_pack")
	if err != nil {
		return frozenInputs{}, errors.Join(errArReviewInputsMissing, err)
	}
	inputs.packHash = packHash
	if err := json.Unmarshal(packBody, &inputs.pack); err != nil {
		return frozenInputs{}, fmt.Errorf("%w: evidence pack is not decodable: %v", errArReviewInputsMissing, err)
	}
	if err := inputs.pack.Validate(); err != nil {
		return frozenInputs{}, fmt.Errorf("%w: evidence pack invalid: %v", errArReviewInputsMissing, err)
	}
	outlineBody, outlineHash, err := s.latestArtifact(ctx, runID, "approved_research_outline")
	if err != nil {
		return frozenInputs{}, errors.Join(errArReviewInputsMissing, err)
	}
	inputs.outlineHash = outlineHash
	if err := json.Unmarshal(outlineBody, &inputs.outline); err != nil {
		return frozenInputs{}, fmt.Errorf("%w: outline is not decodable: %v", errArReviewInputsMissing, err)
	}
	if err := inputs.outline.Validate(); err != nil {
		return frozenInputs{}, fmt.Errorf("%w: outline invalid: %v", errArReviewInputsMissing, err)
	}
	draftBody, _, err := s.latestArtifact(ctx, runID, "full_draft")
	if err != nil {
		return frozenInputs{}, errors.Join(errArReviewInputsMissing, err)
	}
	inputs.draft = string(draftBody)
	return inputs, nil
}

// latestArtifact loads the newest artifact of one type from the run's ledger
// and returns its content bytes and content hash.
func (s *arReviewService) latestArtifact(ctx context.Context, runID, artifactType string) ([]byte, string, error) {
	artifacts, err := s.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		return nil, "", err
	}
	best := writingstore.ArtifactRecord{}
	for _, artifact := range artifacts {
		if artifact.ArtifactType == artifactType && artifact.Version >= best.Version && artifact.ContentHash != "" {
			best = artifact
		}
	}
	if best.ContentHash == "" {
		return nil, "", fmt.Errorf("%w: run has no %s artifact", errArReviewInputsMissing, artifactType)
	}
	_, body, err := s.store.GetArtifactContent(ctx, best.ContentHash)
	if err != nil {
		return nil, "", err
	}
	return body, best.ContentHash, nil
}

func outlineHeadings(outline writingkernel.ResearchOutline) []string {
	headings := make([]string, 0, len(outline.Sections))
	for _, section := range outline.Sections {
		headings = append(headings, section.Title)
	}
	return headings
}

// Serve runs the worker loop: claim pending jobs, reconcile stuck ones. One
// job at a time — the sidecar serializes runs behind a single RLock anyway,
// so extra workers only add queue pressure, never throughput.
func (s *arReviewService) Serve(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		if err := s.scanOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("ar review worker scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *arReviewService) scanOnce(ctx context.Context) error {
	const worker = "arreview.worker"
	now := s.now()
	job, claimed, err := s.store.ClaimPendingArReviewJob(ctx, worker, s.runLeaseTTL(), now)
	if err != nil {
		return err
	}
	if claimed {
		s.safeExecute(ctx, job)
		return nil
	}
	// Reconcile: expired leases (worker died mid-run) and outcome-unknown
	// jobs past the reconciliation interval (the sync response was lost but
	// the sidecar may have finished). Neither is ever re-dispatched.
	stuck, err := s.store.StuckArReviewJobs(ctx, now, now.Add(-time.Minute), 10)
	if err != nil {
		return err
	}
	for _, stuckJob := range stuck {
		s.reconcile(ctx, stuckJob)
	}
	return nil
}

func (s *arReviewService) runLeaseTTL() time.Duration {
	ttl := time.Duration(s.cfg.TimeoutMS) * time.Millisecond
	if ttl <= 0 {
		ttl = arreview.DefaultRunTimeout
	}
	return ttl + 5*time.Minute
}

// safeExecute drives one claimed job to a terminal state. Panics are
// contained: a crashed execution loses only its lease, and the next scan
// reconciles instead of re-running.
func (s *arReviewService) safeExecute(ctx context.Context, job writingstore.ArReviewJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("ar review job execution panicked", "panic", recovered)
		}
	}()
	s.execute(ctx, job)
}

func (s *arReviewService) execute(ctx context.Context, job writingstore.ArReviewJob) {
	inputs, err := s.loadFrozenInputs(ctx, job.OwnerUserID, job.RunID)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"inputs_missing", err.Error(), nil, nil, job.CorpusWarnings, "")
		return
	}
	corpus, warnings, err := arreview.BuildCorpus(inputs.pack, inputs.centralQuestion)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"insufficient_corpus", err.Error(), nil, nil, warnings, "")
		return
	}
	corpusJSON, err := json.Marshal(corpus)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"marshal_corpus", err.Error(), nil, nil, warnings, "")
		return
	}
	specMapping, err := arreview.BuildSpecMapping(inputs.centralQuestion, outlineHeadings(inputs.outline), inputs.pack.Coverage.Topics, len(corpus.Sources))
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"invalid_spec", err.Error(), nil, nil, warnings, "")
		return
	}
	specJSON, err := json.Marshal(specMapping)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"marshal_spec", err.Error(), nil, nil, warnings, "")
		return
	}
	if _, err := s.exchange.PrepareProject(job.SurrogateProjectID, corpusJSON, specJSON); err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"exchange_conflict", err.Error(), nil, nil, warnings, "")
		return
	}

	timeout := time.Duration(s.cfg.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = arreview.DefaultRunTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	record, err := s.client.StartRun(runCtx, arreview.RunRequest{
		ProjectID:            job.SurrogateProjectID,
		Mode:                 arreview.ModeGenerateOnly,
		CorpusRelativePath:   "02_literature/REVIEW_CORPUS.json",
		BaselineRelativePath: "05_writing/MANUSCRIPT_REVIEW.md",
		IdempotencyKey:       job.IdempotencyKey,
	})
	if record != nil && record.ReviewRunID != "" {
		_ = s.store.SetArReviewJobRemoteRun(ctx, job.ID, record.ReviewRunID)
	}
	if err != nil {
		// Outcome-unknown (transport, timeout, 5xx): reconcile from the
		// sidecar's project ledger instead of assuming failure.
		if errors.Is(err, arreview.ErrOutcomeUnknown) {
			s.reconcile(ctx, job)
			return
		}
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"sidecar_rejected", err.Error(), nil, nil, warnings, remoteID(record))
		return
	}
	s.importCompleted(ctx, job, corpus, inputs, warnings)
}

// reconcile re-drives one non-terminal job from the sidecar's persisted
// ledger: read-only sidecar calls, no re-dispatch. A still-running sidecar
// records outcome_unknown (or re-touches an existing one) so the scan
// retries later.
func (s *arReviewService) reconcile(ctx context.Context, job writingstore.ArReviewJob) {
	records, err := s.client.ListRuns(ctx, job.SurrogateProjectID)
	if err != nil {
		if errors.Is(err, arreview.ErrNotFound) {
			// The sidecar never saw the run: definitive, no record exists.
			_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
				"sidecar_no_record", "sidecar has no record for this idempotency key", nil, nil, job.CorpusWarnings, "")
			return
		}
		// Sidecar unreachable: leave the job for the next scan.
		_ = s.store.TouchArReviewJob(ctx, job.ID)
		return
	}
	var match *arreview.RunRecord
	for index := range records {
		if records[index].IdempotencyKey == job.IdempotencyKey {
			match = &records[index]
			break
		}
	}
	if match == nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"sidecar_no_record", "sidecar has no record for this idempotency key", nil, nil, job.CorpusWarnings, "")
		return
	}
	_ = s.store.SetArReviewJobRemoteRun(ctx, job.ID, match.ReviewRunID)
	switch match.Status {
	case arreview.StatusCompleted:
		inputs, inputsErr := s.loadFrozenInputs(ctx, job.OwnerUserID, job.RunID)
		if inputsErr != nil {
			_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
				"inputs_missing", inputsErr.Error(), nil, nil, job.CorpusWarnings, match.ReviewRunID)
			return
		}
		corpus, warnings, corpusErr := arreview.BuildCorpus(inputs.pack, inputs.centralQuestion)
		if corpusErr != nil {
			_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
				"insufficient_corpus", corpusErr.Error(), nil, nil, job.CorpusWarnings, match.ReviewRunID)
			return
		}
		s.importCompleted(ctx, job, corpus, inputs, warnings)
	case arreview.StatusFailed:
		message := ""
		if match.ErrorMessage != nil {
			message = *match.ErrorMessage
		}
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"sidecar_failed", message, nil, nil, job.CorpusWarnings, match.ReviewRunID)
	default:
		if job.Status == writingstore.ArReviewJobOutcomeUnknwn {
			_ = s.store.TouchArReviewJob(ctx, job.ID)
			return
		}
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobOutcomeUnknwn,
			"sidecar_still_running", "the sidecar run has not reached a terminal state", nil, nil, job.CorpusWarnings, match.ReviewRunID)
	}
}

// importCompleted pulls the sidecar outputs from the exchange volume,
// re-hashes them, stores them as content-addressed blobs, computes the
// mechanical comparison metrics, and commits the job. A cancel request wins:
// the sidecar outputs exist, but the owner asked for them to be discarded.
func (s *arReviewService) importCompleted(ctx context.Context, job writingstore.ArReviewJob, corpus *arreview.Corpus, inputs frozenInputs, warnings []string) {
	fresh, err := s.store.GetArReviewJob(ctx, job.OwnerUserID, job.ID)
	if err == nil && fresh.CancelRequested {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobCancelled,
			"owner_cancelled", "cancelled before import", nil, nil, warnings, "")
		return
	}
	refs := make([]writingstore.ArReviewArtifactRef, 0, len(arReviewArtifactKinds)+1)
	imported := map[string][]byte{}
	paths := map[string]string{
		"manuscript":   "05_writing/MANUSCRIPT_REVIEW_AGENT.md",
		"citation_map": "05_writing/AGENT_CITATION_MAP.json",
		"receipt":      "05_writing/AGENT_REVIEW_RECEIPT.json",
		"quality":      "06_review/AGENT_REVIEW_QUALITY.json",
	}
	for _, kind := range arReviewArtifactKinds {
		payload, err := s.exchange.ReadArtifact(job.SurrogateProjectID, paths[kind])
		if err != nil {
			_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
				"sidecar_output_missing", err.Error(), refsSoFar(refs), nil, warnings, "")
			return
		}
		mediaType := "application/json"
		if kind == "manuscript" {
			mediaType = "text/markdown"
		}
		if err := s.store.PutArtifactContent(ctx, arreview.HashContent(payload), mediaType, payload); err != nil {
			_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
				"artifact_store_failed", err.Error(), refsSoFar(refs), nil, warnings, "")
			return
		}
		imported[kind] = payload
		refs = append(refs, writingstore.ArReviewArtifactRef{Kind: kind, ContentHash: arreview.HashContent(payload),
			MediaType: mediaType, Size: len(payload), SidecarPath: paths[kind]})
	}
	metrics, err := arreview.BuildMetrics(imported["manuscript"], imported["citation_map"], corpus, inputs.draft)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"metrics_failed", err.Error(), refsSoFar(refs), nil, warnings, "")
		return
	}
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"metrics_failed", err.Error(), refsSoFar(refs), nil, warnings, "")
		return
	}
	if err := s.store.PutArtifactContent(ctx, arreview.HashContent(metricsJSON), "application/json", metricsJSON); err != nil {
		_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobFailed,
			"artifact_store_failed", err.Error(), refsSoFar(refs), nil, warnings, "")
		return
	}
	refs = append(refs, writingstore.ArReviewArtifactRef{Kind: "metrics", ContentHash: arreview.HashContent(metricsJSON),
		MediaType: "application/json", Size: len(metricsJSON), SidecarPath: "host://metrics"})
	_ = s.store.FinishArReviewJob(ctx, job.ID, writingstore.ArReviewJobCompleted,
		"", "", refs, usageFromReceipt(imported["receipt"]), warnings, "")
}

// usageFromReceipt sums the sidecar receipt's per-stage token usage. When
// the receipt carries no usage the map stays empty — the job never claims a
// zero cost that was not measured.
func usageFromReceipt(receiptJSON []byte) map[string]any {
	var receipt struct {
		LLM struct {
			Provider string `json:"provider"`
			Model    string `json:"model"`
			Stages   []struct {
				Stage string `json:"stage"`
				Calls []struct {
					Usage struct {
						PromptTokens     int `json:"prompt_tokens"`
						CompletionTokens int `json:"completion_tokens"`
					} `json:"usage"`
				} `json:"calls"`
			} `json:"stages"`
		} `json:"llm"`
	}
	if err := json.Unmarshal(receiptJSON, &receipt); err != nil {
		return map[string]any{}
	}
	input, output := int64(0), int64(0)
	for _, stage := range receipt.LLM.Stages {
		for _, call := range stage.Calls {
			input += int64(call.Usage.PromptTokens)
			output += int64(call.Usage.CompletionTokens)
		}
	}
	if input == 0 && output == 0 {
		return map[string]any{}
	}
	return map[string]any{
		"measured":       true,
		"input_tokens":   input,
		"output_tokens":  output,
		"provider":       receipt.LLM.Provider,
		"model":          receipt.LLM.Model,
		"sidecar_synced": true,
	}
}

func refsSoFar(refs []writingstore.ArReviewArtifactRef) []writingstore.ArReviewArtifactRef {
	if refs == nil {
		return []writingstore.ArReviewArtifactRef{}
	}
	return refs
}

func remoteID(record *arreview.RunRecord) string {
	if record == nil {
		return ""
	}
	return record.ReviewRunID
}
