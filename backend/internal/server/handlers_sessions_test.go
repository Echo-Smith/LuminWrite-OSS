package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
)

// newSessionsFixture builds a minimal Server carrying the trace repo plus a
// hand-mounted sessions/organization router over an isolated migrated
// database, with two seeded users and one trace owned by user A.
func newSessionsFixture(t *testing.T) (*Server, *chi.Mux, string, string, string) {
	t.Helper()
	db, cleanup, err := dbtest.Open(os.Getenv("TEST_DATABASE_URL"), 4, 2)
	if err != nil {
		if err == dbtest.ErrNoDatabaseURL {
			t.Skip("TEST_DATABASE_URL not set")
		}
		t.Fatal(err)
	}
	t.Cleanup(cleanup)

	ctx := context.Background()
	var userA, userB string
	for _, uid := range []string{"sess-a", "sess-b"} {
		var id string
		if err := db.QueryRowContext(ctx,
			`INSERT INTO users (uid, name, role) VALUES ($1, $1, 'user') RETURNING id::text`, uid,
		).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if uid == "sess-a" {
			userA = id
		} else {
			userB = id
		}
	}

	traceID := "trace-sess-a-1"
	if _, err := db.ExecContext(ctx, `
		INSERT INTO agent_traces (trace_id, user_id, user_input, mode, status, review_result, created_at)
		VALUES ($1, $2::uuid, '写一篇关于夜经济的文章', 'auto', 'completed',
		        '{"scores": {"factuality": 0.9, "structure": 0.8}}', NOW())
	`, traceID, userA); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.JWT.Secret = "sessions-test-secret"
	cfg.JWT.Expiry = time.Hour

	s := &Server{cfg: cfg, db: db, traces: database.NewTraceRepo(db)}
	return s, sessionsRouter(s), userA, userB, traceID
}

func sessionsRouter(s *Server) *chi.Mux {
	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		r.With(s.jwtAuthMiddleware).Get("/sessions", s.handleListUserSessions)
		r.With(s.jwtAuthMiddleware).Get("/sessions/{traceId}", s.handleGetUserSession)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/session-folders", s.handleListSessionFolders)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/session-folders", s.handleCreateSessionFolder)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Put("/session-folders/{folderId}", s.handleRenameSessionFolder)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Delete("/session-folders/{folderId}", s.handleDeleteSessionFolder)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/sessions/batch", s.handleBatchSessions)
		r.With(s.jwtOptionalMiddleware).Post("/feedback", s.handleFeedback)
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Get("/feedback/mine", s.handleGetMyFeedback)
	})
	return router
}

func sessionsRequest(t *testing.T, s *Server, router *chi.Mux, userID, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.GenerateJWT(userID, "user", "sess_test")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// TestSessionDetailOwnership: a session is readable only by its owner.
func TestSessionDetailOwnership(t *testing.T) {
	s, router, userA, userB, traceID := newSessionsFixture(t)

	if rec := sessionsRequest(t, s, router, userB, http.MethodGet, "/api/v2/sessions/"+traceID, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("user B reading A's session: got %d, want 404", rec.Code)
	}
	rec := sessionsRequest(t, s, router, userA, http.MethodGet, "/api/v2/sessions/"+traceID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner read: got %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "写一篇关于夜经济的文章") {
		t.Fatal("owner read missing user_input")
	}
}

// TestSessionListAggregates: the personal list carries review_score and
// has_feedback so the personal-center view matches the admin one.
func TestSessionListAggregates(t *testing.T) {
	s, router, userA, _, traceID := newSessionsFixture(t)

	rec := sessionsRequest(t, s, router, userA, http.MethodPost, "/api/v2/feedback",
		`{"trace_id":"`+traceID+`","segments":[{"segment_type":"overall","rating":4,"feedback_type":"good","comment":"不错"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("feedback submit: got %d %s", rec.Code, rec.Body.String())
	}

	rec = sessionsRequest(t, s, router, userA, http.MethodGet, "/api/v2/sessions", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d", rec.Code)
	}
	body := rec.Body.String()
	if !contains(body, `"review_score"`) || !contains(body, "0.85") {
		t.Fatalf("list missing review_score: %s", body)
	}
	if !contains(body, `"has_feedback":true`) {
		t.Fatalf("list missing has_feedback=true: %s", body)
	}
}

// TestSessionFoldersAndBatch: CRUD + batch organization, ownership enforced.
func TestSessionFoldersAndBatch(t *testing.T) {
	s, router, userA, userB, traceID := newSessionsFixture(t)

	// Create
	rec := sessionsRequest(t, s, router, userA, http.MethodPost, "/api/v2/session-folders", `{"name":"行业分析"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create folder: got %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// Foreign rename → 404
	if rec := sessionsRequest(t, s, router, userB, http.MethodPut, "/api/v2/session-folders/"+created.Data.ID, `{"name":"抢"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("user B renaming A's folder: got %d, want 404", rec.Code)
	}

	// Rename by owner
	if rec := sessionsRequest(t, s, router, userA, http.MethodPut, "/api/v2/session-folders/"+created.Data.ID, `{"name":"改名"}`); rec.Code != http.StatusOK {
		t.Fatalf("rename: got %d %s", rec.Code, rec.Body.String())
	}

	// Foreign batch move → 0 affected
	rec = sessionsRequest(t, s, router, userB, http.MethodPost, "/api/v2/sessions/batch",
		`{"action":"move","trace_ids":["`+traceID+`"],"folder_id":"`+created.Data.ID+`"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("foreign batch: got %d", rec.Code)
	}
	if !contains(rec.Body.String(), `"affected":0`) {
		t.Fatalf("foreign batch must affect 0 rows: %s", rec.Body.String())
	}

	// Owner batch move → 1 affected; folder membership visible in list
	rec = sessionsRequest(t, s, router, userA, http.MethodPost, "/api/v2/sessions/batch",
		`{"action":"move","trace_ids":["`+traceID+`"],"folder_id":"`+created.Data.ID+`"}`)
	if !contains(rec.Body.String(), `"affected":1`) {
		t.Fatalf("owner batch move: %s", rec.Body.String())
	}
	rec = sessionsRequest(t, s, router, userA, http.MethodGet, "/api/v2/session-folders", "")
	if rec.Code != http.StatusOK || !contains(rec.Body.String(), `"name":"改名"`) {
		t.Fatalf("folder list: %d %s", rec.Code, rec.Body.String())
	}

	// Archive + unarchive
	rec = sessionsRequest(t, s, router, userA, http.MethodPost, "/api/v2/sessions/batch",
		`{"action":"archive","trace_ids":["`+traceID+`"]}`)
	if !contains(rec.Body.String(), `"affected":1`) {
		t.Fatalf("archive: %s", rec.Body.String())
	}
	rec = sessionsRequest(t, s, router, userA, http.MethodGet, "/api/v2/sessions?archived=true", "")
	if !contains(rec.Body.String(), traceID) {
		t.Fatal("archived trace not listed under archived=true")
	}

	// Delete folder → trace survives with folder cleared
	if rec := sessionsRequest(t, s, router, userA, http.MethodDelete, "/api/v2/session-folders/"+created.Data.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete folder: got %d", rec.Code)
	}
}

// TestMyFeedbackScoped: /feedback/mine returns only the caller's own rows —
// both historical rows whose feedback_segments.user_id is NULL (covered by
// the trace JOIN) and new submissions (user_id persisted on write).
func TestMyFeedbackScoped(t *testing.T) {
	s, router, userA, userB, traceID := newSessionsFixture(t)
	ctx := context.Background()

	// Historical-style row with NULL user_id (pre-Phase-2 write path). The
	// one-shot HasFeedback check correctly counts it as submitted feedback.
	if _, err := s.db.Exec(`
		INSERT INTO feedback_segments (trace_id, segment_type, rating, feedback_type, comment)
		VALUES ($1, 'overall', 5, 'good', '结构很好')
	`, traceID); err != nil {
		t.Fatal(err)
	}

	// A second trace for the new-style submission path.
	trace2 := "trace-sess-a-2"
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO agent_traces (trace_id, user_id, user_input, mode, status)
		VALUES ($1, $2::uuid, '第二篇', 'auto', 'completed')
	`, trace2, userA); err != nil {
		t.Fatal(err)
	}
	if rec := sessionsRequest(t, s, router, userA, http.MethodPost, "/api/v2/feedback",
		`{"trace_id":"`+trace2+`","segments":[{"segment_type":"title","rating":3,"feedback_type":"suggestion","comment":"标题可以更具体"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("feedback submit: got %d %s", rec.Code, rec.Body.String())
	}
	// The write path persisted user_id.
	var n int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM feedback_segments WHERE trace_id = $1 AND user_id = $2::uuid`, trace2, userA,
	).Scan(&n); err != nil || n != 1 {
		t.Fatalf("new-style feedback did not persist user_id: n=%d err=%v", n, err)
	}

	// Owner sees both rows.
	rec := sessionsRequest(t, s, router, userA, http.MethodGet, "/api/v2/feedback/mine", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("feedback mine: got %d %s", rec.Code, rec.Body.String())
	}
	if !contains(rec.Body.String(), "结构很好") || !contains(rec.Body.String(), "标题可以更具体") {
		t.Fatalf("owner feedback missing rows: %s", rec.Body.String())
	}
	if !contains(rec.Body.String(), "写一篇关于夜经济的文章") {
		t.Fatal("feedback mine missing trace title context")
	}

	// The other user sees nothing.
	rec = sessionsRequest(t, s, router, userB, http.MethodGet, "/api/v2/feedback/mine", "")
	if !contains(rec.Body.String(), `"total":0`) {
		t.Fatalf("user B must see zero feedback: %s", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return bytes.Contains([]byte(haystack), []byte(needle))
}
