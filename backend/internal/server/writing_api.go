package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingquality"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

var (
	errWritingForbidden              = errors.New("writing api: forbidden")
	errWritingContractRequired       = errors.New("writing api: confirmed contract required")
	errWritingPlanRequired           = errors.New("writing api: validated plan required")
	errWritingVersionConflict        = errors.New("writing api: document version conflict")
	errWritingApprovalScope          = errors.New("writing api: approval scope mismatch")
	errWritingRuntimeUnavailable     = errors.New("writing api: runtime unavailable")
	errWritingIdempotencyKeyRequired = errors.New("writing api: idempotency key required")
)

var governedWritingPermissions = []writingplan.Permission{
	"document.revision", "external.research", "materials.read", "model.invoke", "validation.run",
}

type writingAccess struct {
	UserID       string
	Role         string
	WorkspaceIDs []string
}

type createWritingDocumentCommand struct {
	IdempotencyKey string
	Title          string
	Metadata       map[string]any
}

type putWritingContractCommand struct {
	DocumentID string
	Contract   writingkernel.WritingContract
}

type confirmWritingContractCommand struct {
	ContractID      string
	PreviousVersion int
	Contract        writingkernel.WritingContract
}

type compileWritingPlanCommand struct {
	DocumentID            string
	ContractID            string
	ContractVersion       int
	BaseVersionID         string
	IntentPlan            writingplan.IntentPlan
	Budget                writingplan.PlanBudget
	InitialArtifactTypes  []writingplan.ArtifactType
	RequiredValidators    []string
	RequiredFinalArtifact writingplan.ArtifactType
	SystemRecommendation  writingkernel.OrchestrationMode
}

type writingPlanPreview struct {
	Envelope      writingplan.WritingPlanEnvelope `json:"plan"`
	Budget        writingplan.PlanBudget          `json:"budget"`
	Permissions   []writingplan.Permission        `json:"permissions"`
	BaseVersionID string                          `json:"base_version_id,omitempty"`
}

type createWritingRunCommand struct {
	IdempotencyKey  string
	DocumentID      string
	ContractID      string
	ContractVersion int
	ContractHash    string
	BaseVersionID   string
	Plan            writingplan.WritingPlanEnvelope
	Budget          writingplan.PlanBudget
	Permissions     []writingplan.Permission
}

type approveWritingRunCommand struct {
	IdempotencyKey string
	RunID          string
	PlanID         string
	PlanVersion    int
	PlanHash       string
	Permissions    []writingplan.Permission
}

type controlWritingRunCommand struct {
	IdempotencyKey string
	RunID          string
	Action         string
}

type writingEventPage struct {
	Events       []websocket.WritingEvent `json:"events"`
	NextSequence int64                    `json:"next_sequence"`
}

type writingAPIService interface {
	CreateDocument(context.Context, writingAccess, createWritingDocumentCommand) (writingstore.DocumentRecord, error)
	GetDocument(context.Context, writingAccess, string) (writingstore.DocumentRecord, error)
	ListDocumentVersions(context.Context, writingAccess, string) ([]writingstore.StoredDocumentVersion, error)
	PutContract(context.Context, writingAccess, putWritingContractCommand) (writingstore.ContractRecord, error)
	ConfirmContract(context.Context, writingAccess, confirmWritingContractCommand) (writingstore.ContractRecord, error)
	CompilePlan(context.Context, writingAccess, compileWritingPlanCommand) (writingPlanPreview, error)
	CreateRun(context.Context, writingAccess, createWritingRunCommand) (writingstore.RuntimeRun, error)
	GetRun(context.Context, writingAccess, string) (writingstore.RuntimeRun, error)
	ListEvents(context.Context, writingAccess, string, int64, int) (writingEventPage, error)
	ApproveRun(context.Context, writingAccess, approveWritingRunCommand) (writingstore.RuntimeRun, error)
	ControlRun(context.Context, writingAccess, controlWritingRunCommand) (writingstore.RuntimeRun, error)
	QualitySummary(context.Context, writingAccess, string) (writingquality.UserQualitySummary, error)
	AuditReport(context.Context, writingAccess, string) (writingquality.AuditQualityReport, error)
}

type writingRunController interface {
	Pause(context.Context, string, string, writingstore.Actor) error
	Resume(context.Context, string, string, writingstore.Actor) error
	Cancel(context.Context, string, string, writingstore.Actor) error
}

type persistentWritingAPI struct {
	store        *writingstore.Store
	capabilities *writingplan.CapabilityRegistry
	templates    *writingplan.TemplateRegistry
	controller   writingRunController
}

func newPersistentWritingAPI(store *writingstore.Store) *persistentWritingAPI {
	return &persistentWritingAPI{store: store, capabilities: writingplan.DefaultCapabilityRegistry(), templates: writingplan.DefaultTemplateRegistry()}
}

func writingAccessFromRequest(r *http.Request) (writingAccess, error) {
	principal := userFromContext(r.Context())
	if principal == nil || strings.TrimSpace(principal.Sub) == "" || principal.Role == "guest" {
		return writingAccess{}, errWritingForbidden
	}
	return writingAccess{UserID: principal.Sub, Role: principal.Role, WorkspaceIDs: append([]string(nil), principal.WorkspaceIDs...)}, nil
}

func writingTrace(access writingAccess, operation string) writingstore.TraceContext {
	return writingstore.TraceContext{Provenance: map[string]any{"api": "v2", "operation": operation}, SourceRefs: []string{}, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: access.UserID}}
}

func (service *persistentWritingAPI) authorizeDocument(ctx context.Context, access writingAccess, documentID string) (writingstore.DocumentRecord, error) {
	document, err := service.store.GetDocument(ctx, documentID)
	if err != nil {
		return writingstore.DocumentRecord{}, err
	}
	if writingDocumentAccessible(document, access) {
		return document, nil
	}
	return writingstore.DocumentRecord{}, errWritingForbidden
}

func writingDocumentAccessible(document writingstore.DocumentRecord, access writingAccess) bool {
	if document.OwnerUserID == access.UserID {
		return true
	}
	workspaceID, _ := document.Metadata["workspace_id"].(string)
	for _, allowed := range access.WorkspaceIDs {
		if workspaceID != "" && workspaceID == allowed {
			return true
		}
	}
	return false
}

func (service *persistentWritingAPI) CreateDocument(ctx context.Context, access writingAccess, command createWritingDocumentCommand) (writingstore.DocumentRecord, error) {
	if strings.TrimSpace(command.IdempotencyKey) == "" || strings.TrimSpace(command.Title) == "" {
		return writingstore.DocumentRecord{}, writingstore.ErrInvalidRecord
	}
	if command.Metadata == nil {
		command.Metadata = map[string]any{}
	}
	if workspaceID, _ := command.Metadata["workspace_id"].(string); workspaceID != "" && !containsString(access.WorkspaceIDs, workspaceID) {
		return writingstore.DocumentRecord{}, errWritingForbidden
	}
	record := writingstore.DocumentRecord{DocumentID: writingstore.StableID("doc_", access.UserID, command.IdempotencyKey), OwnerUserID: access.UserID, Title: strings.TrimSpace(command.Title), Status: "active", Metadata: command.Metadata, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: access.UserID}}
	if err := service.store.CreateDocument(ctx, record); err != nil {
		return writingstore.DocumentRecord{}, err
	}
	return service.store.GetDocument(ctx, record.DocumentID)
}

func (service *persistentWritingAPI) GetDocument(ctx context.Context, access writingAccess, documentID string) (writingstore.DocumentRecord, error) {
	return service.authorizeDocument(ctx, access, documentID)
}

func (service *persistentWritingAPI) ListDocumentVersions(ctx context.Context, access writingAccess, documentID string) ([]writingstore.StoredDocumentVersion, error) {
	if _, err := service.authorizeDocument(ctx, access, documentID); err != nil {
		return nil, err
	}
	return service.store.ListDocumentVersions(ctx, documentID)
}

func (service *persistentWritingAPI) PutContract(ctx context.Context, access writingAccess, command putWritingContractCommand) (writingstore.ContractRecord, error) {
	if _, err := service.authorizeDocument(ctx, access, command.DocumentID); err != nil {
		return writingstore.ContractRecord{}, err
	}
	if command.Contract.Status != writingkernel.ContractStatusDraft {
		return writingstore.ContractRecord{}, writingstore.ErrInvalidRecord
	}
	record := writingstore.ContractRecord{DocumentID: command.DocumentID, Contract: command.Contract, Trace: writingTrace(access, "contract.create")}
	if err := service.store.PutContract(ctx, record); err != nil {
		return writingstore.ContractRecord{}, err
	}
	return service.store.GetContract(ctx, command.Contract.ContractID, command.Contract.Version)
}

func (service *persistentWritingAPI) ConfirmContract(ctx context.Context, access writingAccess, command confirmWritingContractCommand) (writingstore.ContractRecord, error) {
	previous, err := service.store.GetContract(ctx, command.ContractID, command.PreviousVersion)
	if err != nil {
		return writingstore.ContractRecord{}, err
	}
	if _, err := service.authorizeDocument(ctx, access, previous.DocumentID); err != nil {
		return writingstore.ContractRecord{}, err
	}
	if command.Contract.ContractID != command.ContractID || command.Contract.Status != writingkernel.ContractStatusConfirmed {
		return writingstore.ContractRecord{}, writingstore.ErrInvalidRecord
	}
	if err := writingkernel.ValidateTransition(previous.Contract, command.Contract); err != nil {
		return writingstore.ContractRecord{}, fmt.Errorf("%w: %v", writingstore.ErrInvalidRecord, err)
	}
	record := writingstore.ContractRecord{DocumentID: previous.DocumentID, Contract: command.Contract, Trace: writingTrace(access, "contract.confirm")}
	if err := service.store.PutContract(ctx, record); err != nil {
		return writingstore.ContractRecord{}, err
	}
	return service.store.GetContract(ctx, command.ContractID, command.Contract.Version)
}

func (service *persistentWritingAPI) CompilePlan(ctx context.Context, access writingAccess, command compileWritingPlanCommand) (writingPlanPreview, error) {
	document, err := service.authorizeDocument(ctx, access, command.DocumentID)
	if err != nil {
		return writingPlanPreview{}, err
	}
	contract, err := service.store.GetContract(ctx, command.ContractID, command.ContractVersion)
	if errors.Is(err, writingstore.ErrNotFound) {
		return writingPlanPreview{}, errWritingContractRequired
	}
	if err != nil {
		return writingPlanPreview{}, err
	}
	if contract.DocumentID != command.DocumentID || contract.Contract.Status != writingkernel.ContractStatusConfirmed {
		return writingPlanPreview{}, errWritingContractRequired
	}
	if document.CurrentVersionID != command.BaseVersionID {
		return writingPlanPreview{}, errWritingVersionConflict
	}
	if len(command.InitialArtifactTypes) > 0 && !sameArtifactTypes(command.InitialArtifactTypes, []writingplan.ArtifactType{"contract"}) {
		return writingPlanPreview{}, fmt.Errorf("%w: initial artifacts must be backed by persisted server references", writingstore.ErrInvalidRecord)
	}
	if command.RequiredFinalArtifact != "" && command.RequiredFinalArtifact != "revision_set" {
		return writingPlanPreview{}, fmt.Errorf("%w: governed writing plans must produce revision_set", writingstore.ErrInvalidRecord)
	}
	requiredValidators := unionWritingStrings(writingplan.RequiredValidatorsForAssurance(contract.Contract.Collaboration.AssuranceLevel), command.RequiredValidators)
	result, err := writingplan.Compile(writingplan.CompileRequest{IntentPlan: command.IntentPlan, Contract: contract.Contract, Registry: service.capabilities, Templates: service.templates, InitialArtifactTypes: []writingplan.ArtifactType{"contract"}, AllowedPermissions: governedWritingPermissions, Budget: command.Budget, RequiredValidators: requiredValidators, RequiredFinalArtifact: "revision_set", SystemRecommendation: command.SystemRecommendation})
	envelope := writingplan.WritingPlanEnvelope{SchemaVersion: writingplan.SchemaVersion, IntentPlan: command.IntentPlan, ExecutablePlan: result.Plan, StrategyDecision: result.Decision}
	permissions := permissionsForPlan(result.Plan, service.capabilities)
	preview := writingPlanPreview{Envelope: envelope, Budget: command.Budget, Permissions: permissions, BaseVersionID: command.BaseVersionID}
	if err != nil {
		return preview, err
	}
	return preview, nil
}

func (service *persistentWritingAPI) CreateRun(ctx context.Context, access writingAccess, command createWritingRunCommand) (writingstore.RuntimeRun, error) {
	document, err := service.authorizeDocument(ctx, access, command.DocumentID)
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	contract, err := service.store.GetContract(ctx, command.ContractID, command.ContractVersion)
	if errors.Is(err, writingstore.ErrNotFound) {
		return writingstore.RuntimeRun{}, errWritingContractRequired
	}
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	if contract.DocumentID != command.DocumentID || contract.Contract.Status != writingkernel.ContractStatusConfirmed || contract.Contract.ContractHash != command.ContractHash {
		return writingstore.RuntimeRun{}, errWritingContractRequired
	}
	if document.CurrentVersionID != command.BaseVersionID {
		return writingstore.RuntimeRun{}, errWritingVersionConflict
	}
	if err := command.Plan.Validate(); err != nil || !command.Plan.ExecutablePlan.StaticValidation.Valid {
		return writingstore.RuntimeRun{}, errWritingPlanRequired
	}
	if command.Plan.IntentPlan.ContractRef != (writingplan.ObjectRef{ID: command.ContractID, Version: command.ContractVersion, Hash: command.ContractHash}) {
		return writingstore.RuntimeRun{}, errWritingContractRequired
	}
	permissions := permissionsForPlan(command.Plan.ExecutablePlan, service.capabilities)
	if !sameWritingPermissions(permissions, command.Permissions) || !permissionSubset(permissions, governedWritingPermissions) {
		return writingstore.RuntimeRun{}, errWritingApprovalScope
	}
	validators := writingplan.RequiredValidatorsForAssurance(contract.Contract.Collaboration.AssuranceLevel)
	validation := writingplan.ValidationContext{Registry: service.capabilities, InitialArtifactTypes: []writingplan.ArtifactType{"contract"}, AllowedPermissions: governedWritingPermissions, Budget: command.Budget, RequiredValidators: validators, RequiredFinalArtifact: "revision_set", ExternalResearchAllowed: contract.Contract.MaterialPolicy.AllowExternalResearch}
	if err := command.Plan.ValidateForDispatch(validation); err != nil {
		return writingstore.RuntimeRun{}, fmt.Errorf("%w: %v", errWritingPlanRequired, err)
	}
	if strings.TrimSpace(command.IdempotencyKey) == "" {
		return writingstore.RuntimeRun{}, writingstore.ErrInvalidRecord
	}
	runID := writingstore.StableID("run_", access.UserID, command.IdempotencyKey)
	status := "planned"
	if command.Plan.StrategyDecision.ApprovalRequired {
		status = "awaiting_approval"
	}
	trace := writingTrace(access, "run.create")
	run := writingstore.RunRecord{RunID: runID, DocumentID: command.DocumentID, ContractID: command.ContractID, ContractVersion: command.ContractVersion, ContractHash: command.ContractHash, BaseVersionID: command.BaseVersionID, Status: status, ApprovalMode: contract.Contract.Collaboration.ApprovalMode, RequestedAssurance: contract.Contract.Collaboration.AssuranceLevel, Budget: command.Budget, Permissions: permissions, Trace: trace}
	plan := writingstore.PlanRecord{RunID: runID, PlanVersion: 1, Envelope: command.Plan, Budget: command.Budget, Permissions: permissions, Trace: trace}
	if err := service.store.CreateRunWithPlan(ctx, run, plan, status); err != nil {
		return writingstore.RuntimeRun{}, err
	}
	return service.store.LoadRuntimeRun(ctx, runID)
}

func (service *persistentWritingAPI) GetRun(ctx context.Context, access writingAccess, runID string) (writingstore.RuntimeRun, error) {
	run, err := service.store.LoadRuntimeRun(ctx, runID)
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	if _, err := service.authorizeDocument(ctx, access, run.DocumentID); err != nil {
		return writingstore.RuntimeRun{}, err
	}
	return run, nil
}

func (service *persistentWritingAPI) ListEvents(ctx context.Context, access writingAccess, runID string, after int64, limit int) (writingEventPage, error) {
	run, err := service.GetRun(ctx, access, runID)
	if err != nil {
		return writingEventPage{}, err
	}
	events, err := service.store.ListRunEvents(ctx, runID, after, limit)
	if err != nil {
		return writingEventPage{}, err
	}
	page := writingEventPage{Events: make([]websocket.WritingEvent, 0, len(events)), NextSequence: after}
	for _, event := range events {
		adapted, err := adaptWritingRunEvent(event, run.Status)
		if err != nil {
			return writingEventPage{}, err
		}
		page.Events = append(page.Events, adapted)
		page.NextSequence = event.Sequence
	}
	return page, nil
}

func (service *persistentWritingAPI) ApproveRun(ctx context.Context, access writingAccess, command approveWritingRunCommand) (writingstore.RuntimeRun, error) {
	run, err := service.GetRun(ctx, access, command.RunID)
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	if run.ActivePlanID != command.PlanID || run.ActivePlanVersion != command.PlanVersion || !sameWritingPermissions(run.Permissions, command.Permissions) {
		return writingstore.RuntimeRun{}, errWritingApprovalScope
	}
	approvalKey := command.RunID + ":approval:" + access.UserID + ":" + command.IdempotencyKey
	err = service.store.ApprovePlan(ctx, writingstore.PlanApprovalCommand{RunID: command.RunID, PlanID: command.PlanID, PlanVersion: command.PlanVersion, PlanHash: command.PlanHash, Permissions: command.Permissions, IdempotencyKey: approvalKey, Actor: writingstore.Actor{Type: writingstore.ActorUser, ID: access.UserID}})
	if errors.Is(err, writingstore.ErrConflict) || errors.Is(err, writingstore.ErrIdempotencyConflict) {
		return writingstore.RuntimeRun{}, errWritingApprovalScope
	}
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	return service.store.LoadRuntimeRun(ctx, command.RunID)
}

func (service *persistentWritingAPI) ControlRun(ctx context.Context, access writingAccess, command controlWritingRunCommand) (writingstore.RuntimeRun, error) {
	if _, err := service.GetRun(ctx, access, command.RunID); err != nil {
		return writingstore.RuntimeRun{}, err
	}
	if service.controller == nil {
		return writingstore.RuntimeRun{}, errWritingRuntimeUnavailable
	}
	actor := writingstore.Actor{Type: writingstore.ActorUser, ID: access.UserID}
	var err error
	switch command.Action {
	case "pause":
		err = service.controller.Pause(ctx, command.RunID, command.IdempotencyKey, actor)
	case "resume":
		err = service.controller.Resume(ctx, command.RunID, command.IdempotencyKey, actor)
	case "cancel":
		err = service.controller.Cancel(ctx, command.RunID, command.IdempotencyKey, actor)
	default:
		err = writingstore.ErrInvalidRecord
	}
	if err != nil {
		return writingstore.RuntimeRun{}, err
	}
	return service.store.LoadRuntimeRun(ctx, command.RunID)
}

func (service *persistentWritingAPI) QualitySummary(ctx context.Context, access writingAccess, documentID string) (writingquality.UserQualitySummary, error) {
	report, err := service.qualityReport(ctx, access, documentID)
	if err != nil {
		return writingquality.UserQualitySummary{}, err
	}
	return writingquality.ProjectUserSummary(report, 5), nil
}

func (service *persistentWritingAPI) AuditReport(ctx context.Context, access writingAccess, documentID string) (writingquality.AuditQualityReport, error) {
	report, err := service.qualityReport(ctx, access, documentID)
	if err != nil {
		return writingquality.AuditQualityReport{}, err
	}
	return writingquality.ProjectAuditReport(report), nil
}

func (service *persistentWritingAPI) qualityReport(ctx context.Context, access writingAccess, documentID string) (writingkernel.QualityReport, error) {
	if _, err := service.authorizeDocument(ctx, access, documentID); err != nil {
		return writingkernel.QualityReport{}, err
	}
	stored, err := service.store.GetLatestQualityReport(ctx, documentID)
	if err != nil {
		return writingkernel.QualityReport{}, err
	}
	payload, err := json.Marshal(stored.Payload)
	if err != nil {
		return writingkernel.QualityReport{}, err
	}
	var report writingkernel.QualityReport
	if err := json.Unmarshal(payload, &report); err != nil {
		return writingkernel.QualityReport{}, err
	}
	if err := writingquality.ValidateReport(report); err != nil {
		return writingkernel.QualityReport{}, err
	}
	return report, nil
}

func permissionsForPlan(plan writingplan.ExecutablePlan, registry *writingplan.CapabilityRegistry) []writingplan.Permission {
	set := map[writingplan.Permission]struct{}{}
	for _, node := range plan.Nodes {
		if manifest, ok := registry.Get(node.Capability); ok {
			for _, permission := range manifest.Permissions {
				set[permission] = struct{}{}
			}
		}
	}
	result := make([]writingplan.Permission, 0, len(set))
	for permission := range set {
		result = append(result, permission)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func unionWritingStrings(left, right []string) []string {
	set := map[string]struct{}{}
	for _, value := range append(append([]string(nil), left...), right...) {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func permissionSubset(left, right []writingplan.Permission) bool {
	allowed := map[writingplan.Permission]struct{}{}
	for _, permission := range right {
		allowed[permission] = struct{}{}
	}
	for _, permission := range left {
		if _, ok := allowed[permission]; !ok {
			return false
		}
	}
	return true
}

func sameWritingPermissions(left, right []writingplan.Permission) bool {
	if len(left) != len(right) {
		return false
	}
	return permissionSubset(left, right) && permissionSubset(right, left)
}

func sameArtifactTypes(left, right []writingplan.ArtifactType) bool {
	if len(left) != len(right) {
		return false
	}
	set := make(map[writingplan.ArtifactType]int, len(left))
	for _, value := range left {
		set[value]++
	}
	for _, value := range right {
		set[value]--
		if set[value] < 0 {
			return false
		}
	}
	return true
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func decodeWritingJSON(w http.ResponseWriter, r *http.Request, target any) error {
	payload, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if err != nil {
		return err
	}
	if err := rejectDuplicateWritingJSON(payload); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func rejectDuplicateWritingJSON(payload []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var consume func() error
	consume = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return errors.New("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON key %q", key)
				}
				seen[key] = struct{}{}
				if err := consume(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := consume(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return errors.New("unexpected JSON delimiter")
		}
	}
	if err := consume(); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON value")
		}
		return err
	}
	return nil
}

func (s *Server) writeWritingError(w http.ResponseWriter, err error) {
	s.writeWritingErrorWithData(w, err, nil)
}

func (s *Server) writeWritingErrorWithData(w http.ResponseWriter, err error, data any) {
	status, code := http.StatusInternalServerError, "WRITING_INTERNAL_ERROR"
	switch {
	case errors.Is(err, errWritingIdempotencyKeyRequired):
		status, code = http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED"
	case errors.Is(err, errWritingForbidden):
		status, code = http.StatusForbidden, "FORBIDDEN"
	case errors.Is(err, errWritingContractRequired):
		status, code = http.StatusUnprocessableEntity, "CONTRACT_REQUIRED"
	case errors.Is(err, errWritingPlanRequired):
		status, code = http.StatusUnprocessableEntity, "PLAN_NOT_EXECUTABLE"
	case errors.Is(err, errWritingVersionConflict), errors.Is(err, writingstore.ErrConflict):
		status, code = http.StatusConflict, "VERSION_CONFLICT"
	case errors.Is(err, errWritingApprovalScope), errors.Is(err, writingstore.ErrIdempotencyConflict):
		status, code = http.StatusConflict, "APPROVAL_SCOPE_MISMATCH"
	case errors.Is(err, errWritingRuntimeUnavailable):
		status, code = http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE"
	case errors.Is(err, writingstore.ErrNotFound):
		status, code = http.StatusNotFound, "WRITING_RESOURCE_NOT_FOUND"
	case errors.Is(err, writingstore.ErrImmutableConflict):
		status, code = http.StatusConflict, "IMMUTABLE_CONFLICT"
	case errors.Is(err, writingplan.ErrPlanNotExecutable):
		status, code = http.StatusUnprocessableEntity, "PLAN_NOT_EXECUTABLE"
	case errors.Is(err, writingstore.ErrInvalidRecord):
		status, code = http.StatusBadRequest, "INVALID_WRITING_REQUEST"
	default:
		var syntaxError *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		if errors.As(err, &syntaxError) || errors.As(err, &typeError) || strings.Contains(err.Error(), "json: unknown field") || strings.Contains(err.Error(), "duplicate JSON key") || errors.Is(err, io.EOF) {
			status, code = http.StatusBadRequest, "INVALID_JSON"
		}
	}
	if data == nil {
		message := err.Error()
		if status == http.StatusInternalServerError {
			message = "governed writing request failed"
		}
		response.Err(w, status, code, message)
		return
	}
	message := err.Error()
	if status == http.StatusInternalServerError {
		message = "governed writing request failed"
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(response.APIResponse{Success: false, Data: data, Error: &response.APIError{Code: code, Message: message}})
}

func (s *Server) requireWritingAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.writingAPI == nil {
			s.writeWritingError(w, errWritingRuntimeUnavailable)
			return
		}
		next.ServeHTTP(w, r)
	})
}
