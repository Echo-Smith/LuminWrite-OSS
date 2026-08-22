package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/pkg/response"
)

// ─── SSE Topic Push ──────────────────────────────────────

// SSEClient represents a single SSE connection with optional userID for isolation.
type SSEClient struct {
	id     string
	userID string // empty = anonymous / not authenticated
	ch     chan *SSEEvent
}

// SSEHub manages SSE client connections and topic broadcasting.
// Supports both global broadcast (admin) and user-targeted push.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[string]*SSEClient // clientID → client
	nextID  int
}

// SSEEvent is a single server-sent event.
type SSEEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
	ID    string      `json:"id,omitempty"`
	// UserID, when non-empty, restricts delivery to clients whose userID matches.
	// Empty = broadcast to all clients (admin notifications, global events).
	UserID string `json:"-"`
}

// NewSSEHub creates a new SSEHub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[string]*SSEClient),
	}
}

// Register adds a new SSE client and returns its ID and event channel.
// userID associates the connection with the authenticated user for targeted push.
func (h *SSEHub) Register(userID string) (string, <-chan *SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	clientID := fmt.Sprintf("sse-%d-%d", h.nextID, time.Now().UnixNano())
	ch := make(chan *SSEEvent, 64) // buffered to avoid blocking sender
	h.clients[clientID] = &SSEClient{
		id:     clientID,
		userID: userID,
		ch:     ch,
	}

	slog.Info("SSE client registered", "client_id", clientID, "user_id", userID, "total", len(h.clients))
	return clientID, ch
}

// Unregister removes an SSE client.
func (h *SSEHub) Unregister(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if client, ok := h.clients[clientID]; ok {
		close(client.ch)
		delete(h.clients, clientID)
		slog.Info("SSE client unregistered", "client_id", clientID, "total", len(h.clients))
	}
}

// Broadcast sends an event to all connected SSE clients.
// If event.UserID is non-empty, only delivers to clients matching that userID.
func (h *SSEHub) Broadcast(event *SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, client := range h.clients {
		// User-scoped delivery: skip clients that don't match
		if event.UserID != "" && client.userID != "" && client.userID != event.UserID {
			continue
		}
		select {
		case client.ch <- event:
		default:
			slog.Warn("SSE client buffer full, skipping event", "event", event.Event, "client_id", client.id)
		}
	}
}

// SendToUser sends an event only to clients matching the given userID.
func (h *SSEHub) SendToUser(userID string, event *SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, client := range h.clients {
		if client.userID != userID {
			continue
		}
		select {
		case client.ch <- event:
		default:
			slog.Warn("SSE client buffer full, skipping event", "event", event.Event, "client_id", client.id)
		}
	}
}

// ClientCount returns the number of connected clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// UserClientCount returns the number of active SSE connections for a given user.
// Used by the multi-device session list to show real-time online status.
func (h *SSEHub) UserClientCount(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	count := 0
	for _, client := range h.clients {
		if client.userID == userID && client.userID != "" {
			count++
		}
	}
	return count
}

// ─── SSE HTTP Handler ────────────────────────────────────

// handleSSETopics handles the SSE endpoint for real-time topic pushes.
// Clients connect with GET /api/v2/sse/topics and receive events as they arrive.
func (s *Server) handleSSETopics(w http.ResponseWriter, r *http.Request) {
	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Disable Nginx buffering

	// Check if streaming is supported
	flusher, ok := w.(http.Flusher)
	if !ok {
		response.Err(w, http.StatusInternalServerError, "internal_error", "streaming not supported")
		return
	}

	// Extract userID from JWT for user-scoped event delivery
	sseUserID := s.getUserIDFromRequest(r)

	// Register client with userID for targeted push
	clientID, eventCh := s.sseHub.Register(sseUserID)
	defer s.sseHub.Unregister(clientID)

	// Send initial connection event
	writeSSEEvent(w, flusher, &SSEEvent{
		Event: "connected",
		Data: map[string]interface{}{
			"client_id": clientID,
			"timestamp": time.Now().Format(time.RFC3339),
			"message":   "SSE connection established",
		},
	})

	// Send recent topics as initial batch
	if s.traces != nil {
		topics, _, err := s.traces.ListTopics(r.Context(), "", 1, 5)
		if err == nil && len(topics) > 0 {
			writeSSEEvent(w, flusher, &SSEEvent{
				Event: "topics:initial",
				Data:  topics,
			})
		}
	}

	// Set up heartbeat ticker
	heartbeat := time.NewTicker(30 * time.Second)
	defer heartbeat.Stop()

	// Set up context cancellation
	ctx := r.Context()

	for {
		select {
		case <-ctx.Done():
			slog.Info("SSE client disconnected", "client_id", clientID)
			return

		case event := <-eventCh:
			writeSSEEvent(w, flusher, event)

		case <-heartbeat.C:
			// Send heartbeat to keep connection alive
			writeSSEEvent(w, flusher, &SSEEvent{
				Event: "heartbeat",
				Data: map[string]interface{}{
					"timestamp": time.Now().Format(time.RFC3339),
					"clients":   s.sseHub.ClientCount(),
				},
			})
		}
	}
}

// handleSSESendNotification allows admin to send a test notification to all SSE clients.
func (s *Server) handleSSESendNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Title == "" {
		req.Title = "测试通知"
	}
	if req.Body == "" {
		req.Body = "这是一条来自管理员的测试通知"
	}

	// Broadcast notification to all SSE clients (admin broadcast)
	s.sseHub.Broadcast(&SSEEvent{
		Event: "notification",
		Data: map[string]interface{}{
			"title":     req.Title,
			"body":      req.Body,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})

	response.OK(w, map[string]interface{}{
		"title":   req.Title,
		"body":    req.Body,
		"pushed":  true,
		"clients": s.sseHub.ClientCount(),
		"message": "notification sent to all SSE clients",
	})
}

// handleSSETestNotification allows any authenticated user to send a test
// notification to themselves. This replaces the admin-only endpoint for
// the personal-center "发送测试通知" button.
func (s *Server) handleSSETestNotification(w http.ResponseWriter, r *http.Request) {
	userID := s.getUserIDFromRequest(r)
	if userID == "" || userID == "anonymous" {
		response.Err(w, http.StatusUnauthorized, "unauthorized", "authentication required")
		return
	}

	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	// Body is optional — we have sensible defaults
	_ = json.NewDecoder(r.Body).Decode(&req)

	if req.Title == "" {
		req.Title = "测试通知"
	}
	if req.Body == "" {
		req.Body = "如果你能看到这条消息，说明 SSE 在线通知工作正常！"
	}

	// Send only to this user's SSE connections
	s.sseHub.SendToUser(userID, &SSEEvent{
		Event: "notification",
		Data: map[string]interface{}{
			"title":     req.Title,
			"body":      req.Body,
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})

	response.OK(w, map[string]interface{}{
		"title":   req.Title,
		"body":    req.Body,
		"pushed":  true,
		"message": "test notification sent to your connections",
	})
}

// handleSSEStats returns SSE connection statistics.
func (s *Server) handleSSEStats(w http.ResponseWriter, r *http.Request) {
	response.OK(w, map[string]interface{}{
		"connected_clients": s.sseHub.ClientCount(),
		"timestamp":         time.Now().Format(time.RFC3339),
	})
}

// writeSSEEvent writes a single SSE event to the response writer.
func writeSSEEvent(w http.ResponseWriter, flusher http.Flusher, event *SSEEvent) {
	data, _ := json.Marshal(event.Data)

	if event.Event != "" {
		fmt.Fprintf(w, "event: %s\n", event.Event)
	}
	if event.ID != "" {
		fmt.Fprintf(w, "id: %s\n", event.ID)
	}
	fmt.Fprintf(w, "data: %s\n\n", string(data))

	flusher.Flush()
}

// PushTopicsFromDB periodically queries the database for new topics and pushes them to SSE clients.
// This should be run as a background goroutine.
func (s *Server) PushTopicsFromDB(ctx context.Context, interval time.Duration) {
	if s.traces == nil {
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	lastCheck := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Query for topics created since last check
			topics, _, err := s.traces.ListTopics(ctx, "", 1, 10)
			if err != nil {
				slog.Warn("SSE: failed to query new topics", "error", err)
				continue
			}

			for _, topic := range topics {
				if createdAt, ok := topic["created_at"].(time.Time); ok {
					if createdAt.After(lastCheck) {
						s.sseHub.Broadcast(&SSEEvent{
							Event: "topic:new",
							Data:  topic,
						})
					}
				}
			}

			lastCheck = time.Now()
		}
	}
}
