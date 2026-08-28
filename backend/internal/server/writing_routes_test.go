package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/websocket"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingkernel"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingplan"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingquality"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/writingstore"
)

type fakeWritingAPI struct {
	compileErr   error
	createRunErr error
	lastAccess   writingAccess
	lastControl  controlWritingRunCommand
	lastApproval approveWritingRunCommand
	events       writingEventPage
	quality      writingquality.UserQualitySummary
	audit        writingquality.AuditQualityReport
}

func (fake *fakeWritingAPI) remember(access writingAccess) { fake.lastAccess = access }

func (fake *fakeWritingAPI) CreateDocument(_ context.Context, access writingAccess, command createWritingDocumentCommand) (writingstore.DocumentRecord, error) {
	fake.remember(access)
	return writingstore.DocumentRecord{DocumentID: "doc_test", OwnerUserID: access.UserID, Title: command.Title, Status: "active", Metadata: command.Metadata}, nil
}
func (fake *fakeWritingAPI) GetDocument(_ context.Context, access writingAccess, id string) (writingstore.DocumentRecord, error) {
	fake.remember(access)
	return writingstore.DocumentRecord{DocumentID: id, OwnerUserID: access.UserID, Title: "Test", Status: "active", Metadata: map[string]any{}}, nil
}
func (fake *fakeWritingAPI) ListDocumentVersions(_ context.Context, access writingAccess, _ string) ([]writingstore.StoredDocumentVersion, error) {
	fake.remember(access)
	return []writingstore.StoredDocumentVersion{}, nil
}
func (fake *fakeWritingAPI) PutContract(_ context.Context, access writingAccess, command putWritingContractCommand) (writingstore.ContractRecord, error) {
	fake.remember(access)
	return writingstore.ContractRecord{DocumentID: command.DocumentID, Contract: command.Contract}, nil
}
func (fake *fakeWritingAPI) ConfirmContract(_ context.Context, access writingAccess, command confirmWritingContractCommand) (writingstore.ContractRecord, error) {
	fake.remember(access)
	return writingstore.ContractRecord{DocumentID: "doc_test", Contract: command.Contract}, nil
}
func (fake *fakeWritingAPI) CompilePlan(_ context.Context, access writingAccess, _ compileWritingPlanCommand) (writingPlanPreview, error) {
	fake.remember(access)
	return writingPlanPreview{Permissions: []writingplan.Permission{}}, fake.compileErr
}
func (fake *fakeWritingAPI) CreateRun(_ context.Context, access writingAccess, _ createWritingRunCommand) (writingstore.RuntimeRun, error) {
	fake.remember(access)
	return testRuntimeRun(), fake.createRunErr
}
func (fake *fakeWritingAPI) GetRun(_ context.Context, access writingAccess, _ string) (writingstore.RuntimeRun, error) {
	fake.remember(access)
	return testRuntimeRun(), nil
}
func (fake *fakeWritingAPI) ListEvents(_ context.Context, access writingAccess, _ string, _ int64, _ int) (writingEventPage, error) {
	fake.remember(access)
	return fake.events, nil
}
func (fake *fakeWritingAPI) ApproveRun(_ context.Context, access writingAccess, command approveWritingRunCommand) (writingstore.RuntimeRun, error) {
	fake.remember(access)
	fake.lastApproval = command
	return testRuntimeRun(), nil
}
func (fake *fakeWritingAPI) ControlRun(_ context.Context, access writingAccess, command controlWritingRunCommand) (writingstore.RuntimeRun, error) {
	fake.remember(access)
	fake.lastControl = command
	return testRuntimeRun(), nil
}
func (fake *fakeWritingAPI) QualitySummary(_ context.Context, access writingAccess, _ string) (writingquality.UserQualitySummary, error) {
	fake.remember(access)
	return fake.quality, nil
}
func (fake *fakeWritingAPI) AuditReport(_ context.Context, access writingAccess, _ string) (writingquality.AuditQualityReport, error) {
	fake.remember(access)
	return fake.audit, nil
}

func testRuntimeRun() writingstore.RuntimeRun {
	return writingstore.RuntimeRun{RunID: "run_test", DocumentID: "doc_test", ContractID: "ctr_test", ContractHash: "sha256:" + strings.Repeat("a", 64), ContractVersion: 2, Status: "planned", Budget: writingplan.PlanBudget{}, Permissions: []writingplan.Permission{}}
}

func testWritingRouter(t *testing.T, fake *fakeWritingAPI) (http.Handler, string) {
	t.Helper()
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}}, writingAPI: fake}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	token, err := server.GenerateJWT("00000000-0000-0000-0000-000000000001", "user", "session_test")
	if err != nil {
		t.Fatal(err)
	}
	return router, token
}

func writingRequest(t *testing.T, router http.Handler, token, method, path, body, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestWritingRoutesRequireAuthenticatedNonGuestPrincipal(t *testing.T) {
	router, _ := testWritingRouter(t, &fakeWritingAPI{})
	recorder := writingRequest(t, router, "", http.MethodGet, "/api/v2/documents/doc_test", "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWritingDocumentAccessIsOwnerOrExplicitWorkspaceMembership(t *testing.T) {
	document := writingstore.DocumentRecord{DocumentID: "doc_test", OwnerUserID: "owner", Metadata: map[string]any{"workspace_id": "workspace_editorial"}}
	if !writingDocumentAccessible(document, writingAccess{UserID: "owner"}) {
		t.Fatal("direct owner was denied")
	}
	if !writingDocumentAccessible(document, writingAccess{UserID: "member", WorkspaceIDs: []string{"workspace_editorial"}}) {
		t.Fatal("explicit workspace member was denied")
	}
	if writingDocumentAccessible(document, writingAccess{UserID: "stranger", Role: "admin", WorkspaceIDs: []string{"workspace_other"}}) {
		t.Fatal("role or unrelated workspace bypassed document ownership")
	}
}

func TestWritingDocumentCreateRequiresIdempotencyAndStrictJSON(t *testing.T) {
	fake := &fakeWritingAPI{}
	router, token := testWritingRouter(t, fake)
	missing := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents", `{"title":"A"}`, "")
	assertWritingErrorCode(t, missing, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED")
	unknown := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents", `{"title":"A","surprise":true}`, "create-1")
	assertWritingErrorCode(t, unknown, http.StatusBadRequest, "INVALID_JSON")
	duplicate := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents", `{"title":"A","title":"B"}`, "create-1")
	assertWritingErrorCode(t, duplicate, http.StatusBadRequest, "INVALID_JSON")
	created := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents", `{"title":"A","metadata":{}}`, "create-1")
	if created.Code != http.StatusCreated || !strings.Contains(created.Body.String(), `"document_id":"doc_test"`) {
		t.Fatalf("status=%d body=%s", created.Code, created.Body.String())
	}
}

func TestWritingRunReturnsStableMissingPlanError(t *testing.T) {
	fake := &fakeWritingAPI{createRunErr: errWritingPlanRequired}
	router, token := testWritingRouter(t, fake)
	recorder := writingRequest(t, router, token, http.MethodPost, "/api/v2/runs", `{"plan":{},"permissions":[]}`, "run-1")
	assertWritingErrorCode(t, recorder, http.StatusUnprocessableEntity, "PLAN_NOT_EXECUTABLE")
}

func TestWritingPlanReturnsStableMissingContractErrorWithDiagnosticPreview(t *testing.T) {
	fake := &fakeWritingAPI{compileErr: errWritingContractRequired}
	router, token := testWritingRouter(t, fake)
	recorder := writingRequest(t, router, token, http.MethodPost, "/api/v2/documents/doc_test/plans", `{}`, "")
	assertWritingErrorCode(t, recorder, http.StatusUnprocessableEntity, "CONTRACT_REQUIRED")
	if !strings.Contains(recorder.Body.String(), `"permissions":[]`) {
		t.Fatalf("diagnostic preview missing: %s", recorder.Body.String())
	}
}

func TestWritingRunControlsAndApprovalRequireExactNamedRoutes(t *testing.T) {
	fake := &fakeWritingAPI{}
	router, token := testWritingRouter(t, fake)
	pause := writingRequest(t, router, token, http.MethodPost, "/api/v2/runs/run_test/pause", `{}`, "pause-1")
	if pause.Code != http.StatusOK || fake.lastControl.Action != "pause" || fake.lastControl.IdempotencyKey != "pause-1" {
		t.Fatalf("pause status=%d command=%+v", pause.Code, fake.lastControl)
	}
	waive := writingRequest(t, router, token, http.MethodPost, "/api/v2/runs/run_test/waive", `{}`, "waive-1")
	if waive.Code != http.StatusNotFound {
		t.Fatalf("BLOCKER waiver-like route unexpectedly exists: %d", waive.Code)
	}
	approve := writingRequest(t, router, token, http.MethodPost, "/api/v2/runs/run_test/approve", `{"plan_id":"plan_test","plan_version":1,"plan_hash":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","permissions":[]}`, "approve-1")
	if approve.Code != http.StatusOK || fake.lastApproval.PlanID != "plan_test" || fake.lastApproval.IdempotencyKey != "approve-1" {
		t.Fatalf("approval status=%d command=%+v", approve.Code, fake.lastApproval)
	}
}

func TestWritingEventsExposeDurableEnvelopeAsJSONAndSSE(t *testing.T) {
	event := websocket.WritingEvent{Protocol: websocket.WritingProtocolV2, Type: websocket.MsgWritingRunStatus, RunID: "run_test", Sequence: 4, Timestamp: time.Now().UTC(), Status: "running", Payload: websocket.WritingRunStatusPayload{To: "running"}}
	fake := &fakeWritingAPI{events: writingEventPage{Events: []websocket.WritingEvent{event}, NextSequence: 4}}
	router, token := testWritingRouter(t, fake)
	jsonResponse := writingRequest(t, router, token, http.MethodGet, "/api/v2/runs/run_test/events?after=3", "", "")
	if jsonResponse.Code != http.StatusOK || !strings.Contains(jsonResponse.Body.String(), `"sequence":4`) {
		t.Fatalf("JSON events status=%d body=%s", jsonResponse.Code, jsonResponse.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v2/runs/run_test/events?after=3&follow=false", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "text/event-stream")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "id: 4\nevent: writing.run.status") {
		t.Fatalf("SSE status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWritingEventAdapterSeparatesProvisionalDeltaFromCommittedVersion(t *testing.T) {
	delta := newProvisionalDocumentDelta("run_test", "doc_test", "block_intro", "draft", "running", 7, time.Now())
	if err := delta.Validate(); err != nil {
		t.Fatal(err)
	}
	committed, err := adaptWritingRunEvent(writingstore.RunEvent{EventID: "evt_commit", RunID: "run_test", Sequence: 8, EventType: "document.committed", OccurredAt: time.Now().UTC(), EntityKind: "document_version", EntityID: "ver_test", Payload: map[string]any{"document_id": "doc_test", "content_hash": "sha256:" + strings.Repeat("b", 64), "quality_state": "accepted_draft"}}, "completed")
	if err != nil {
		t.Fatal(err)
	}
	if committed.Type != websocket.MsgWritingDocumentCommitted {
		t.Fatalf("type=%s", committed.Type)
	}
	if payload, ok := committed.Payload.(websocket.WritingDocumentCommittedPayload); !ok || payload.Lifecycle != "committed" || payload.VersionID != "ver_test" {
		t.Fatalf("payload=%#v", committed.Payload)
	}
}

func TestWritingQualityUsesSeparateUserAndAuditProjections(t *testing.T) {
	fake := &fakeWritingAPI{quality: writingquality.UserQualitySummary{QualityState: writingkernel.QualityStateAcceptedDraft, KeyFindings: []writingkernel.QualityFinding{}}, audit: writingquality.AuditQualityReport{Report: writingkernel.QualityReport{SchemaVersion: writingkernel.SchemaVersionV1, ReportID: "qr_test", Validators: []writingkernel.ValidatorResult{}}}}
	router, token := testWritingRouter(t, fake)
	summary := writingRequest(t, router, token, http.MethodGet, "/api/v2/documents/doc_test/quality", "", "")
	audit := writingRequest(t, router, token, http.MethodGet, "/api/v2/documents/doc_test/audit-report", "", "")
	if summary.Code != http.StatusOK || strings.Contains(summary.Body.String(), `"validators"`) {
		t.Fatalf("user projection leaked audit detail: %s", summary.Body.String())
	}
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), `"validators":[]`) {
		t.Fatalf("audit projection missing validators: %s", audit.Body.String())
	}
}

func TestWritingRouteSurfaceIncludesEveryGovernedOperation(t *testing.T) {
	fake := &fakeWritingAPI{}
	router, token := testWritingRouter(t, fake)
	cases := []struct {
		method, path, body, key string
		want                    int
	}{
		{http.MethodGet, "/api/v2/documents/doc_test", "", "", http.StatusOK},
		{http.MethodGet, "/api/v2/documents/doc_test/versions", "", "", http.StatusOK},
		{http.MethodPost, "/api/v2/documents/doc_test/contracts", `{"contract":{}}`, "", http.StatusCreated},
		{http.MethodPost, "/api/v2/contracts/ctr_test/confirm", `{"previous_version":1,"contract":{}}`, "", http.StatusOK},
		{http.MethodPost, "/api/v2/runs", `{"plan":{},"permissions":[]}`, "run-1", http.StatusCreated},
		{http.MethodGet, "/api/v2/runs/run_test", "", "", http.StatusOK},
		{http.MethodPost, "/api/v2/runs/run_test/resume", `{}`, "resume-1", http.StatusOK},
		{http.MethodPost, "/api/v2/runs/run_test/cancel", `{}`, "cancel-1", http.StatusOK},
	}
	for _, test := range cases {
		recorder := writingRequest(t, router, token, test.method, test.path, test.body, test.key)
		if recorder.Code != test.want {
			t.Errorf("%s %s status=%d want=%d body=%s", test.method, test.path, recorder.Code, test.want, recorder.Body.String())
		}
	}
}

func TestWritingRouteMetadataMarksAuthenticationAndEventsSSE(t *testing.T) {
	fake := &fakeWritingAPI{}
	server := &Server{cfg: &config.Config{JWT: config.JWTConfig{Secret: "test-secret", Expiry: time.Hour}}, writingAPI: fake, routeReg: newRouteRegistry()}
	router := chi.NewRouter()
	router.Route("/api/v2", func(router chi.Router) { server.registerWritingRoutes(router) })
	server.registerRoutesFromChi(router)
	foundDocument, foundEvents := false, false
	for _, route := range server.routeReg.All() {
		switch route.Path {
		case "/api/v2/documents/{documentId}":
			foundDocument = route.Auth == "jwt" && route.Category == "writing"
		case "/api/v2/runs/{runId}/events":
			foundEvents = route.Auth == "jwt" && route.SSE
		}
	}
	if !foundDocument || !foundEvents {
		t.Fatalf("document=%v events=%v routes=%#v", foundDocument, foundEvents, server.routeReg.All())
	}
}

func assertWritingErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status=%d want=%d body=%s", recorder.Code, status, recorder.Body.String())
	}
	var payload struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Error.Code != code {
		t.Fatalf("code=%q want=%q body=%s", payload.Error.Code, code, recorder.Body.String())
	}
}

var _ writingAPIService = (*fakeWritingAPI)(nil)
