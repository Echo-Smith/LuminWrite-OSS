package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingruntime"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

// Research-review API surface (contracts.md §3): research progress, the two
// human gates, decisions, outline revisions, and referenced-artifact content.
// Authorization is owner-first: a run the caller does not own is 404, never
// 403, so resource existence is not leaked.

var (
	errGateStale                = errors.New("writing api: gate revision is stale")
	errGateAlreadyDecided       = errors.New("writing api: gate already decided")
	errGateIdempotencyConflict  = errors.New("writing api: idempotency key replayed with a different body")
	errInvalidResearchSpec      = errors.New("writing api: invalid research request")
	errInsufficientEvidence     = errors.New("writing api: insufficient evidence")
	errEvidenceInvalid          = errors.New("writing api: evidence invalid")
	errOutlineEvidenceMismatch  = errors.New("writing api: outline does not match its evidence pack")
	errResearchUnavailable      = errors.New("writing api: research runtime unavailable")
	errResearchResourceNotFound = errors.New("writing api: research resource not found")
)

// writingResearchService is the research-review slice of the writing API.
// Implemented by *persistentWritingAPI when the governed runtime is mounted.
type writingResearchService interface {
	GetResearchProgress(ctx context.Context, access writingAccess, runID string) (researchProgressView, error)
	GetGate(ctx context.Context, access writingAccess, runID, gateID string) (gateView, error)
	DecideGate(ctx context.Context, access writingAccess, runID, gateID string, command gateDecisionCommand) (gateDecisionView, bool, error)
	SaveOutlineRevision(ctx context.Context, access writingAccess, runID, gateID string, command outlineRevisionCommand) (outlineRevisionView, error)
	ReadRunArtifact(ctx context.Context, access writingAccess, runID, artifactID string) (artifactContentView, error)
}

type artifactRefView struct {
	ArtifactID  string `json:"artifact_id"`
	Version     int    `json:"version"`
	ContentHash string `json:"content_hash"`
}

type gateView struct {
	GateID            string           `json:"gate_id"`
	RunID             string           `json:"run_id"`
	NodeID            string           `json:"node_id"`
	GateKind          string           `json:"gate_kind"`
	PlanID            string           `json:"plan_id"`
	PlanVersion       int              `json:"plan_version"`
	PlanHash          string           `json:"plan_hash"`
	Revision          int              `json:"revision"`
	Status            string           `json:"status"`
	Decision          string           `json:"decision,omitempty"`
	InputRef          *artifactRefView `json:"input_ref,omitempty"`
	AllowedOperations []string         `json:"allowed_operations"`
	BlockedReason     string           `json:"blocked_reason,omitempty"`
	LastEventSequence int64            `json:"last_event_sequence"`
}

type gateDecisionView struct {
	DecisionID   string `json:"decision_id"`
	GateID       string `json:"gate_id"`
	RunID        string `json:"run_id"`
	Status       string `json:"status"`
	ResumeStatus string `json:"resume_status"`
}

type outlineRevisionView struct {
	GateID       string          `json:"gate_id"`
	GateRevision int             `json:"gate_revision"`
	OutlineRef   artifactRefView `json:"outline_ref"`
}

type researchTaskView struct {
	TaskKey   string `json:"task_key"`
	NodeID    string `json:"node_id"`
	Phase     string `json:"phase"`
	Status    string `json:"status"`
	Attempt   int    `json:"attempt"`
	ErrorCode string `json:"error_code,omitempty"`
}

// researchSpecView projects the run contract's ResearchSpec for the research
// frontend (T09 contract with T09b: exact field names).
type researchSpecView struct {
	MaxPapers           int    `json:"max_papers"`
	MinCitableSources   int    `json:"min_citable_sources"`
	EvidenceRequirement string `json:"evidence_requirement"`
	MaxQueries          int    `json:"max_queries"`
	MaxCandidates       int    `json:"max_candidates"`
}

type researchProgressView struct {
	RunID             string             `json:"run_id"`
	Phase             string             `json:"phase"`
	Counts            map[string]int     `json:"counts"`
	Tasks             []researchTaskView `json:"tasks"`
	PackRef           *artifactRefView   `json:"pack_ref,omitempty"`
	ActiveGate        *gateView          `json:"active_gate,omitempty"`
	Errors            []researchTaskView `json:"errors"`
	LastEventSequence int64              `json:"last_event_sequence"`
	// Spec carries the v1.1 research contract projection; nil (and omitted)
	// for legacy runs, whose response shape stays byte-identical.
	Spec *researchSpecView `json:"spec,omitempty"`
}

type artifactContentView struct {
	ArtifactID  string          `json:"artifact_id"`
	Version     int             `json:"version"`
	ContentHash string          `json:"content_hash"`
	MediaType   string          `json:"media_type"`
	Content     json.RawMessage `json:"content"`
}

type gateDecisionCommand struct {
	PlanID       string          `json:"plan_id"`
	PlanVersion  int             `json:"plan_version"`
	PlanHash     string          `json:"plan_hash"`
	GateRevision int             `json:"gate_revision"`
	InputRef     artifactRefView `json:"input_ref"`
	Decision     string          `json:"decision"`
	// IdempotencyKey carries the client's Idempotency-Key header value; the
	// service scopes it with run+gate+operation+owner for the stored key.
	IdempotencyKey string `json:"-"`
}

type outlineRevisionCommand struct {
	PlanID       string          `json:"plan_id"`
	PlanVersion  int             `json:"plan_version"`
	PlanHash     string          `json:"plan_hash"`
	GateRevision int             `json:"gate_revision"`
	OutlineRef   artifactRefView `json:"outline_ref"`
	Outline      struct {
		Sections    []writingkernel.OutlineSection `json:"sections"`
		Limitations []string                       `json:"limitations"`
	} `json:"outline"`
	// IdempotencyKey carries the client's Idempotency-Key header value.
	IdempotencyKey string `json:"-"`
}

// gateViewOf projects a gate row onto the API view, deriving the operations
// the gate currently accepts (contracts.md §3 GET gate).
func gateViewOf(gate writingstore.GateRecord, lastEventSequence int64) gateView {
	view := gateView{GateID: gate.GateID, RunID: gate.RunID, NodeID: gate.NodeID,
		GateKind: gate.GateKind, PlanID: gate.PlanID, PlanVersion: gate.PlanVersion,
		PlanHash: gate.PlanHash, Revision: gate.Revision, Status: gate.Status,
		Decision: gate.Decision, AllowedOperations: []string{}, LastEventSequence: lastEventSequence}
	if gate.InputArtifactID != "" {
		view.InputRef = &artifactRefView{ArtifactID: gate.InputArtifactID,
			Version: gate.InputArtifactVersion, ContentHash: gate.InputHash}
	}
	if gate.Status == writingstore.GateStatusPending {
		view.AllowedOperations = append(view.AllowedOperations, "decide")
		if gate.GateKind == writingstore.GateKindOutline {
			view.AllowedOperations = append(view.AllowedOperations, "save_outline_revision")
		}
		view.BlockedReason = "awaiting_gate_decision"
	}
	return view
}

func researchAPIOf(service writingAPIService) (writingResearchService, error) {
	if research, ok := service.(writingResearchService); ok {
		return research, nil
	}
	return nil, errResearchUnavailable
}

func (service *persistentWritingAPI) authorizedRun(ctx context.Context, access writingAccess, runID string) (writingstore.RuntimeRun, error) {
	run, err := service.GetRun(ctx, access, runID)
	if err != nil {
		// Owner-first 404: forbidden and missing are indistinguishable.
		return writingstore.RuntimeRun{}, errResearchResourceNotFound
	}
	return run, nil
}

// gateDecisionIdentity composes the owner+operation-scoped idempotency key
// and the request hash the store replays on.
func gateDecisionIdentity(runID, gateID, operation, owner, clientKey string, body any) (string, string) {
	key := runID + ":gate:" + gateID + ":" + operation + ":" + owner + ":" + clientKey
	payload, _ := json.Marshal(body)
	sum := sha256.Sum256(payload)
	return key, "sha256:" + hex.EncodeToString(sum[:])
}

// GetResearchProgress serves GET /runs/{runId}/research.
func (service *persistentWritingAPI) GetResearchProgress(ctx context.Context, access writingAccess, runID string) (researchProgressView, error) {
	run, err := service.authorizedRun(ctx, access, runID)
	if err != nil {
		return researchProgressView{}, err
	}
	tasks, err := service.store.ListResearchTasks(ctx, runID, run.OwnerUserID)
	if err != nil {
		return researchProgressView{}, err
	}
	gates, err := service.store.ListGatesByRun(ctx, runID, run.OwnerUserID)
	if err != nil {
		return researchProgressView{}, err
	}
	view := researchProgressView{RunID: runID, Phase: "pending",
		Counts: map[string]int{"total": 0}, Tasks: []researchTaskView{}, Errors: []researchTaskView{},
		LastEventSequence: run.LastEventSequence}
	var activeGate *writingstore.GateRecord
	for _, gate := range gates {
		if gate.Status == writingstore.GateStatusPending {
			candidate := gate
			activeGate = &candidate
			break
		}
	}
	for _, task := range tasks {
		view.Counts["total"]++
		view.Counts[task.Status]++
		view.Tasks = append(view.Tasks, researchTaskView{TaskKey: task.TaskKey, NodeID: task.NodeID,
			Phase: task.Phase, Status: task.Status, Attempt: task.Attempt, ErrorCode: task.ErrorCode})
		if task.Status == writingstore.ResearchTaskFailed || task.Status == writingstore.ResearchTaskOutcomeUnknwn {
			view.Errors = append(view.Errors, researchTaskView{TaskKey: task.TaskKey, NodeID: task.NodeID,
				Phase: task.Phase, Status: task.Status, Attempt: task.Attempt, ErrorCode: task.ErrorCode})
		}
		view.Phase = task.Phase
	}
	if activeGate != nil {
		projected := gateViewOf(*activeGate, run.LastEventSequence)
		view.ActiveGate = &projected
		view.Phase = "gate_" + activeGate.GateKind
	}
	// T09 view enhancement: research-review runs project their contract's
	// ResearchSpec and the evidence pack's per-paper reading-scope counts
	// (papers_full_text / papers_abstract / papers_unread; 0 before a pack
	// exists). Legacy runs (no research spec) keep the previous shape.
	if contractRecord, contractErr := service.store.GetContract(ctx, run.ContractID, run.ContractVersion); contractErr == nil && contractRecord.Contract.Research != nil {
		spec := contractRecord.Contract.Research
		view.Spec = &researchSpecView{MaxPapers: spec.MaxPapers, MinCitableSources: spec.MinCitableSources,
			EvidenceRequirement: spec.EvidenceRequirement, MaxQueries: spec.MaxQueries, MaxCandidates: spec.MaxCandidates}
		pack, packErr := service.runEvidencePack(ctx, runID)
		if packErr != nil {
			pack = writingkernel.ResearchEvidencePack{}
		}
		view.Counts["papers_full_text"] = 0
		view.Counts["papers_abstract"] = 0
		view.Counts["papers_unread"] = 0
		for _, paper := range pack.Papers {
			switch paper.ReadingScope {
			case writingkernel.ReadingScopeFullText:
				view.Counts["papers_full_text"]++
			case writingkernel.ReadingScopeAbstract:
				view.Counts["papers_abstract"]++
			default:
				view.Counts["papers_unread"]++
			}
		}
	}
	return view, nil
}

// runEvidencePack loads the run's newest frozen evidence pack for view
// projection. Projection is best-effort: any resolution/decode failure yields
// a zero pack, never a failed progress request.
func (service *persistentWritingAPI) runEvidencePack(ctx context.Context, runID string) (writingkernel.ResearchEvidencePack, error) {
	artifacts, err := service.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		return writingkernel.ResearchEvidencePack{}, err
	}
	packHash := ""
	packVersion := 0
	for _, artifact := range artifacts {
		if artifact.ArtifactType != "research_evidence_pack" {
			continue
		}
		if artifact.Version >= packVersion {
			packHash, packVersion = artifact.ContentHash, artifact.Version
		}
	}
	if packHash == "" {
		return writingkernel.ResearchEvidencePack{}, writingstore.ErrNotFound
	}
	_, body, err := service.store.GetArtifactContent(ctx, packHash)
	if err != nil {
		return writingkernel.ResearchEvidencePack{}, err
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(body, &pack); err != nil {
		return writingkernel.ResearchEvidencePack{}, err
	}
	return pack, nil
}

// GetGate serves GET /runs/{runId}/gates/{gateId}.
func (service *persistentWritingAPI) GetGate(ctx context.Context, access writingAccess, runID, gateID string) (gateView, error) {
	run, err := service.authorizedRun(ctx, access, runID)
	if err != nil {
		return gateView{}, err
	}
	gate, err := service.store.GetGate(ctx, runID, gateID)
	if errors.Is(err, writingstore.ErrNotFound) {
		return gateView{}, errResearchResourceNotFound
	}
	if err != nil {
		return gateView{}, err
	}
	return gateViewOf(gate, run.LastEventSequence), nil
}

// DecideGate serves POST /runs/{runId}/gates/{gateId}/decisions. The returned
// bool marks an idempotent replay (HTTP 200) versus a fresh decision (202).
func (service *persistentWritingAPI) DecideGate(ctx context.Context, access writingAccess, runID, gateID string, command gateDecisionCommand) (gateDecisionView, bool, error) {
	if _, err := service.authorizedRun(ctx, access, runID); err != nil {
		return gateDecisionView{}, false, err
	}
	if _, err := service.store.GetGate(ctx, runID, gateID); errors.Is(err, writingstore.ErrNotFound) {
		return gateDecisionView{}, false, errResearchResourceNotFound
	} else if err != nil {
		return gateDecisionView{}, false, err
	}
	if command.Decision != writingstore.GateDecisionApprove {
		return gateDecisionView{}, false, errInvalidResearchSpec
	}
	key, requestHash := gateDecisionIdentity(runID, gateID, "decision", access.UserID, command.IdempotencyKey, command)
	result, err := writingruntime.DecideGate(ctx, service.store, service.gateCheckpoints, service.gateOrchestrator,
		writingruntime.GateDecisionRequest{RunID: runID, GateID: gateID, ActorID: access.UserID,
			PlanID: command.PlanID, PlanVersion: command.PlanVersion, PlanHash: command.PlanHash,
			GateRevision: command.GateRevision, InputArtifactID: command.InputRef.ArtifactID,
			InputVersion: command.InputRef.Version, InputContentHash: command.InputRef.ContentHash,
			Decision: command.Decision, IdempotencyKey: key, RequestHash: requestHash})
	if err != nil {
		return gateDecisionView{}, false, translateGateError(err)
	}
	// The decision is durable; schedule the resume trigger (route A) and let
	// the gate-resume scan backstop a lost trigger (route B).
	service.triggerGateResume(runID)
	replayed := result.Replayed
	return gateDecisionView{DecisionID: gateID, GateID: gateID, RunID: runID,
		Status: result.Gate.Status, ResumeStatus: "queued"}, replayed, nil
}

func translateGateError(err error) error {
	switch {
	case errors.Is(err, writingstore.ErrGateForbidden):
		return errResearchResourceNotFound
	case errors.Is(err, writingstore.ErrStaleGate):
		return errGateStale
	case errors.Is(err, writingstore.ErrGateAlreadyDecided):
		return errGateAlreadyDecided
	case errors.Is(err, writingstore.ErrIdempotencyConflict):
		return errGateIdempotencyConflict
	case errors.Is(err, writingstore.ErrNotFound):
		return errResearchResourceNotFound
	default:
		return err
	}
}

// SaveOutlineRevision serves POST /runs/{runId}/gates/{gateId}/outline-revisions:
// only a pending outline gate accepts revisions, and the edited outline is
// re-validated against its evidence pack before the gate revision bumps.
func (service *persistentWritingAPI) SaveOutlineRevision(ctx context.Context, access writingAccess, runID, gateID string, command outlineRevisionCommand) (outlineRevisionView, error) {
	if _, err := service.authorizedRun(ctx, access, runID); err != nil {
		return outlineRevisionView{}, err
	}
	gate, err := service.store.GetGate(ctx, runID, gateID)
	if errors.Is(err, writingstore.ErrNotFound) {
		return outlineRevisionView{}, errResearchResourceNotFound
	}
	if err != nil {
		return outlineRevisionView{}, err
	}
	if gate.Status != writingstore.GateStatusPending {
		return outlineRevisionView{}, errGateAlreadyDecided
	}
	if gate.GateKind != writingstore.GateKindOutline {
		return outlineRevisionView{}, errOutlineEvidenceMismatch
	}
	// The current outline content pins the contract/pack bindings; the client
	// only supplies sections and limitations.
	_, outlineBody, err := service.store.GetArtifactContent(ctx, gate.InputHash)
	if errors.Is(err, writingstore.ErrNotFound) {
		return outlineRevisionView{}, errEvidenceInvalid
	}
	if err != nil {
		return outlineRevisionView{}, err
	}
	var outline writingkernel.ResearchOutline
	if err := json.Unmarshal(outlineBody, &outline); err != nil {
		return outlineRevisionView{}, fmt.Errorf("%w: stored outline is not decodable: %v", errEvidenceInvalid, err)
	}
	outline.Sections = command.Outline.Sections
	outline.Limitations = command.Outline.Limitations
	pack, err := loadResearchPack(ctx, service.store, outline.EvidencePackRef)
	if err != nil {
		return outlineRevisionView{}, err
	}
	if err := writingkernel.ValidateOutlineAgainstPack(outline, pack); err != nil {
		return outlineRevisionView{}, fmt.Errorf("%w: %v", errOutlineEvidenceMismatch, err)
	}
	body, err := json.Marshal(outline)
	if err != nil {
		return outlineRevisionView{}, err
	}
	contentHash := sha256.Sum256(body)
	outlineHash := "sha256:" + hex.EncodeToString(contentHash[:])
	if err := service.store.PutArtifactContent(ctx, outlineHash, "application/json", body); err != nil {
		return outlineRevisionView{}, err
	}
	key, requestHash := gateDecisionIdentity(runID, gateID, "outline_revision", access.UserID, command.IdempotencyKey, command)
	saved, err := service.store.SaveOutlineRevision(ctx, writingstore.GateOutlineRevision{RunID: runID,
		GateID: gateID, PlanID: command.PlanID, PlanVersion: command.PlanVersion, PlanHash: command.PlanHash,
		GateRevision: command.GateRevision,
		Outline: writingstore.ArtifactContentRef{ArtifactID: writingstore.StableID("art_", runID, gateID, "outline", fmt.Sprint(gate.Revision+1)),
			Version: 1, ContentHash: outlineHash},
		ActorID: access.UserID, IdempotencyKey: key, RequestHash: requestHash})
	if err != nil {
		return outlineRevisionView{}, translateGateError(err)
	}
	return outlineRevisionView{GateID: gateID, GateRevision: saved.Gate.Revision,
		OutlineRef: artifactRefView{ArtifactID: saved.Gate.InputArtifactID,
			Version: saved.Gate.InputArtifactVersion, ContentHash: saved.Gate.InputHash}}, nil
}

// loadResearchPack resolves and validates the evidence pack an outline binds
// to (content-addressed; the hash in the ref must resolve).
func loadResearchPack(ctx context.Context, store *writingstore.Store, packRef writingkernel.ArtifactRef) (writingkernel.ResearchEvidencePack, error) {
	if err := packRef.Validate(); err != nil {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: pack ref: %v", errEvidenceInvalid, err)
	}
	_, body, err := store.GetArtifactContent(ctx, packRef.ContentHash)
	if errors.Is(err, writingstore.ErrNotFound) {
		return writingkernel.ResearchEvidencePack{}, errEvidenceInvalid
	}
	if err != nil {
		return writingkernel.ResearchEvidencePack{}, err
	}
	var pack writingkernel.ResearchEvidencePack
	if err := json.Unmarshal(body, &pack); err != nil {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: pack content is not decodable: %v", errEvidenceInvalid, err)
	}
	if err := pack.Validate(); err != nil {
		return writingkernel.ResearchEvidencePack{}, fmt.Errorf("%w: %v", errEvidenceInvalid, err)
	}
	return pack, nil
}

// ReadRunArtifact serves GET /runs/{runId}/artifacts/{artifactId}/content.
// Readable when the artifact belongs to the run, is referenced by one of the
// run's gates, or is the committed output of one of the run's research
// sub-tasks (the cross-run cache-reuse reference, contracts.md §3).
func (service *persistentWritingAPI) ReadRunArtifact(ctx context.Context, access writingAccess, runID, artifactID string) (artifactContentView, error) {
	run, err := service.authorizedRun(ctx, access, runID)
	if err != nil {
		return artifactContentView{}, err
	}
	mediaType, contentHash, version := "", "", 0
	artifacts, err := service.store.ListRunArtifacts(ctx, runID)
	if err != nil {
		return artifactContentView{}, err
	}
	for _, artifact := range artifacts {
		if artifact.ArtifactID == artifactID {
			mediaType, contentHash, version = artifact.MediaType, artifact.ContentHash, artifact.Version
		}
	}
	if contentHash == "" {
		gates, gateErr := service.store.ListGatesByRun(ctx, runID, run.OwnerUserID)
		if gateErr == nil {
			for _, gate := range gates {
				if gate.InputArtifactID == artifactID && gate.InputHash != "" {
					mediaType, contentHash, version = "application/json", gate.InputHash, gate.InputArtifactVersion
				}
			}
		}
	}
	if contentHash == "" {
		tasks, taskErr := service.store.ListResearchTasks(ctx, runID, run.OwnerUserID)
		if taskErr == nil {
			for _, task := range tasks {
				if task.OutputArtifactID == artifactID && task.OutputHash != "" {
					mediaType, contentHash, version = "application/json", task.OutputHash, 1
				}
			}
		}
	}
	if contentHash == "" {
		return artifactContentView{}, errResearchResourceNotFound
	}
	storedType, body, err := service.store.GetArtifactContent(ctx, contentHash)
	if errors.Is(err, writingstore.ErrNotFound) {
		return artifactContentView{}, errResearchResourceNotFound
	}
	if err != nil {
		return artifactContentView{}, err
	}
	if mediaType == "" {
		mediaType = storedType
	}
	return artifactContentView{ArtifactID: artifactID, Version: version, ContentHash: contentHash,
		MediaType: mediaType, Content: json.RawMessage(body)}, nil
}
