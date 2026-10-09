package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/config"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database/dbtest"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/services"
)

// newTopicKnowledgeFixture builds a minimal server carrying the KB manager
// and trace repo over an isolated migrated database, plus one seeded user and
// one topic (with and without a link).
func newTopicKnowledgeFixture(t *testing.T) (*Server, *chi.Mux, string, string, string) {
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
	var userID string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO users (uid, name, role) VALUES ('topic-kb', 'topic-kb', 'user') RETURNING id::text`,
	).Scan(&userID); err != nil {
		t.Fatal(err)
	}

	// Topic with a description (text mode) and one without but with a link
	// (url mode). raw_data carries the url the way hot-topic ingestion does.
	var textTopicID, urlTopicID string
	if err := db.QueryRowContext(ctx, `
		INSERT INTO topics (title, description, source) VALUES ('带描述的选题', '城市夜经济的三个支柱。', 'user')
		RETURNING id::text
	`).Scan(&textTopicID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `
		INSERT INTO topics (title, source, raw_data) VALUES ('带链接的选题', 'user', '{"url": "https://example.com/article"}')
		RETURNING id::text
	`).Scan(&urlTopicID); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{}
	cfg.JWT.Secret = "topic-kb-secret"
	cfg.JWT.Expiry = time.Hour

	s := &Server{
		cfg:    cfg,
		db:     db,
		traces: database.NewTraceRepo(db),
		kbMgr:  services.NewKbManager(db.DB, nil),
	}
	router := chi.NewRouter()
	router.Route("/api/v2", func(r chi.Router) {
		r.With(s.jwtAuthMiddleware, s.rejectGuestMiddleware).Post("/topics/{id}/knowledge", s.handleSaveTopicToKnowledge)
	})
	return s, router, userID, textTopicID, urlTopicID
}

func topicKnowledgeRequest(t *testing.T, s *Server, router *chi.Mux, userID, topicID, body string) *httptest.ResponseRecorder {
	t.Helper()
	token, err := s.GenerateJWT(userID, "user", "topic_kb_session")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/topics/"+topicID+"/knowledge", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	return recorder
}

func TestSaveTopicToKnowledgeTextMode(t *testing.T) {
	s, router, userID, textTopicID, _ := newTopicKnowledgeFixture(t)

	rec := topicKnowledgeRequest(t, s, router, userID, textTopicID, `{"mode":"text"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("save: got %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Data struct {
			DocID string `json:"doc_id"`
			Title string `json:"title"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Data.DocID == "" {
		t.Fatal("missing doc_id")
	}

	// The document lands in the default KB (write-path unification) and is
	// visible through the KB-scoped listing.
	var kbID string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT kb_id FROM knowledge_base WHERE id = $1`, created.Data.DocID).Scan(&kbID); err != nil {
		t.Fatalf("lookup document: %v", err)
	}
	if kbID != "default" {
		t.Fatalf("document kb_id = %q, want default", kbID)
	}

	// A topic material row exists with source_type=topic.
	var sourceType string
	if err := s.db.QueryRowContext(context.Background(),
		`SELECT source_type FROM user_materials WHERE user_id = $1 AND doc_id = $2`, userID, created.Data.DocID).Scan(&sourceType); err != nil {
		t.Fatalf("lookup material: %v", err)
	}
	if sourceType != "topic" {
		t.Fatalf("material source_type = %q, want topic", sourceType)
	}
}

func TestSaveTopicToKnowledgeRejections(t *testing.T) {
	s, router, userID, textTopicID, _ := newTopicKnowledgeFixture(t)

	// Unknown topic → 404.
	rec := topicKnowledgeRequest(t, s, router, userID, "00000000-0000-0000-0000-000000000000", `{"mode":"text","content":"x"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown topic: got %d, want 404", rec.Code)
	}

	// Invalid mode → 400.
	rec = topicKnowledgeRequest(t, s, router, userID, textTopicID, `{"mode":"bogus","content":"x"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad mode: got %d, want 400", rec.Code)
	}

	// Guest → 403 (rejectGuest middleware).
	guestID := userID // reuse the route; guest token below
	token, err := s.GenerateJWT(guestID, "guest", "topic_kb_guest")
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/topics/"+textTopicID+"/knowledge", strings.NewReader(`{"mode":"text","content":"x"}`))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("guest save: got %d, want 403", recorder.Code)
	}
}
