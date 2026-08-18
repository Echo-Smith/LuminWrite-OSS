package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/database"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── Web Push Subscription Storage ───────────────────────

// PushSubscription represents a browser push subscription stored in DB.
type PushSubscription struct {
	ID          string `json:"id"`
	UserID      string `json:"user_id"`
	Endpoint    string `json:"endpoint"`
	P256dhKey   string `json:"p256dh_key"`
	AuthSecret  string `json:"auth_secret"`
	UserAgent   string `json:"user_agent,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// PushRepo manages push subscription persistence.
type PushRepo struct {
	db *database.DB
}

// NewPushRepo creates a new PushRepo.
func NewPushRepo(db *database.DB) *PushRepo {
	return &PushRepo{db: db}
}

// Save inserts or updates a push subscription for the given user.
func (r *PushRepo) Save(ctx context.Context, userID, endpoint, p256dh, auth, userAgent string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO push_subscriptions (user_id, endpoint, p256dh_key, auth_secret, user_agent)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (endpoint) DO UPDATE
		SET p256dh_key = EXCLUDED.p256dh_key,
		    auth_secret = EXCLUDED.auth_secret,
		    user_agent = EXCLUDED.user_agent,
		    updated_at = NOW()
	`, userID, endpoint, p256dh, auth, userAgent)
	return err
}

// Delete removes a push subscription by endpoint.
func (r *PushRepo) Delete(ctx context.Context, endpoint string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE endpoint = $1`, endpoint)
	return err
}

// DeleteByUser removes all push subscriptions for a user.
func (r *PushRepo) DeleteByUser(ctx context.Context, userID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM push_subscriptions WHERE user_id = $1`, userID)
	return err
}

// ListByUser returns all push subscriptions for a user.
func (r *PushRepo) ListByUser(ctx context.Context, userID string) ([]PushSubscription, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, user_id, endpoint, p256dh_key, auth_secret, user_agent, created_at
		FROM push_subscriptions
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []PushSubscription
	for rows.Next() {
		var s PushSubscription
		if err := rows.Scan(&s.ID, &s.UserID, &s.Endpoint, &s.P256dhKey, &s.AuthSecret, &s.UserAgent, &s.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, s)
	}
	return subs, nil
}

// ─── Web Push Sender ─────────────────────────────────────

// PushSender handles sending web push notifications using VAPID keys.
// It uses the browser's push service (FCM for Chrome/Edge, Mozilla for Firefox).
type PushSender struct {
	publicKey  string
	privateKey string
	subject    string
	mu         sync.Mutex
}

// NewPushSender creates a new PushSender.
func NewPushSender(publicKey, privateKey, subject string) *PushSender {
	return &PushSender{
		publicKey:  publicKey,
		privateKey: privateKey,
		subject:    subject,
	}
}

// IsConfigured returns true if VAPID keys are set.
func (p *PushSender) IsConfigured() bool {
	return p.publicKey != "" && p.privateKey != ""
}

// SendPushNotification sends a push notification to a single subscription.
// It constructs the raw HTTP request to the browser push service endpoint.
// Returns true if the subscription should be cleaned up (410/404).
func (p *PushSender) SendPushNotification(ctx context.Context, sub PushSubscription, payload []byte) (shouldCleanup bool) {
	// Import the webpush library lazily — we use a minimal ECDSA VAPID JWT approach
	// to avoid external dependencies. The webpush protocol is:
	// POST to subscription endpoint with:
	//   - Authorization: WebPush v=... (VAPID JWT)
	//   - Crypto-Key: dh=... (ECDH) ; p256ecdsa=... (VAPID public key)
	//   - Content-Encoding: aesgcm / aes128gcm
	//   - Body: encrypted payload

	// For simplicity and zero-dependency, we use the aes128gcm encoding
	// via the standard library. However, the full Web Push encryption is complex.
	// Instead, we delegate to a minimal inline implementation.
	return sendWebPush(ctx, sub.Endpoint, sub.P256dhKey, sub.AuthSecret, p.privateKey, p.publicKey, p.subject, payload)
}

// ─── HTTP Handlers ───────────────────────────────────────

// handlePushVapidPublicKey returns the VAPID public key for frontend subscription.
// GET /api/v2/push/vapid-public-key
func (s *Server) handlePushVapidPublicKey(w http.ResponseWriter, r *http.Request) {
	if s.pushSender == nil || !s.pushSender.IsConfigured() {
		response.Err(w, http.StatusServiceUnavailable, "push_not_configured", "Web Push is not configured")
		return
	}
	response.OK(w, map[string]interface{}{
		"public_key": s.pushSender.publicKey,
	})
}

// handlePushSubscribe saves a push subscription.
// POST /api/v2/push/subscribe
// Body: { "endpoint": "...", "keys": { "p256dh": "...", "auth": "..." } }
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if user.Role == "guest" {
		response.Err(w, http.StatusForbidden, "forbidden", "guests cannot subscribe to push notifications")
		return
	}

	if s.pushRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "push_not_configured", "push storage not available")
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "endpoint, p256dh, and auth are required")
		return
	}

	ua := r.Header.Get("User-Agent")
	if err := s.pushRepo.Save(r.Context(), user.Sub, req.Endpoint, req.Keys.P256dh, req.Keys.Auth, ua); err != nil {
		slog.Warn("failed to save push subscription", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to save subscription")
		return
	}

	slog.Info("push subscription saved", "user_id", user.Sub, "endpoint", req.Endpoint[:min(len(req.Endpoint), 50)])
	response.OK(w, map[string]interface{}{
		"subscribed": true,
		"message":    "push subscription saved",
	})
}

// handlePushUnsubscribe removes a push subscription.
// DELETE /api/v2/push/unsubscribe
// Body: { "endpoint": "..." }
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if s.pushRepo == nil {
		response.Err(w, http.StatusServiceUnavailable, "push_not_configured", "push storage not available")
		return
	}

	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Endpoint == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "endpoint is required")
		return
	}

	if err := s.pushRepo.Delete(r.Context(), req.Endpoint); err != nil {
		slog.Warn("failed to delete push subscription", "error", err)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to remove subscription")
		return
	}

	response.OK(w, map[string]interface{}{
		"unsubscribed": true,
	})
}

// handlePushTest sends a test push notification to the authenticated user.
// POST /api/v2/push/test
func (s *Server) handlePushTest(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if user == nil {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	if user.Role == "guest" {
		response.Err(w, http.StatusForbidden, "forbidden", "guests cannot use push notifications")
		return
	}

	if s.pushRepo == nil || s.pushSender == nil || !s.pushSender.IsConfigured() {
		response.Err(w, http.StatusServiceUnavailable, "push_not_configured", "Web Push is not configured")
		return
	}

	subs, err := s.pushRepo.ListByUser(r.Context(), user.Sub)
	if err != nil {
		slog.Warn("failed to list push subscriptions", "error", err, "user_id", user.Sub)
		response.Err(w, http.StatusInternalServerError, "internal_error", "failed to list subscriptions")
		return
	}

	if len(subs) == 0 {
		response.Err(w, http.StatusBadRequest, "no_subscription", "no push subscription found, please subscribe first")
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"title": "笔润智谈测试通知",
		"body":  "如果你看到了这条消息，说明浏览器推送已成功配置！",
		"icon":  "/icon-192.png",
		"badge": "/icon-96.png",
		"data":  map[string]string{"url": "/write"},
	})

	sentCount := 0
	cleanupCount := 0
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	for _, sub := range subs {
		shouldCleanup := s.pushSender.SendPushNotification(ctx, sub, payload)
		if shouldCleanup {
			_ = s.pushRepo.Delete(r.Context(), sub.Endpoint)
			cleanupCount++
		} else {
			sentCount++
		}
	}

	response.OK(w, map[string]interface{}{
		"sent":          sentCount,
		"cleaned_up":    cleanupCount,
		"total":         len(subs),
		"message":       "test push notification sent",
	})
}

// ─── Push Integration Helper ─────────────────────────────

// SendPushToUser sends a push notification to all subscriptions of a user.
// This is called from SSE event triggers or agent completion hooks.
func (s *Server) SendPushToUser(ctx context.Context, userID string, title, body string) {
	if s.pushRepo == nil || s.pushSender == nil || !s.pushSender.IsConfigured() {
		return
	}

	subs, err := s.pushRepo.ListByUser(ctx, userID)
	if err != nil || len(subs) == 0 {
		return
	}

	payload, _ := json.Marshal(map[string]interface{}{
		"title": title,
		"body":  body,
		"icon":  "/icon-192.png",
		"badge": "/icon-96.png",
		"data":  map[string]string{"url": "/write"},
	})

	pushCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for _, sub := range subs {
		shouldCleanup := s.pushSender.SendPushNotification(pushCtx, sub, payload)
		if shouldCleanup {
			_ = s.pushRepo.Delete(ctx, sub.Endpoint)
		}
	}
}

// broadcastWebPush sends a push notification to ALL subscribed users.
// This is used for global events like new topic broadcasts.
func (s *Server) broadcastWebPush(ctx context.Context, title, body string) {
	if s.pushRepo == nil || s.pushSender == nil || !s.pushSender.IsConfigured() {
		return
	}

	// Query all distinct user_ids with push subscriptions
	rows, err := s.pushRepo.db.QueryContext(ctx, `SELECT DISTINCT user_id FROM push_subscriptions`)
	if err != nil {
		slog.Warn("broadcast webpush: query users", "error", err)
		return
	}
	defer rows.Close()

	var userIDs []string
	for rows.Next() {
		var uid string
		if err := rows.Scan(&uid); err == nil {
			userIDs = append(userIDs, uid)
		}
	}

	slog.Info("broadcasting web push", "users", len(userIDs), "title", title)

	for _, uid := range userIDs {
		s.SendPushToUser(ctx, uid, title, body)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
