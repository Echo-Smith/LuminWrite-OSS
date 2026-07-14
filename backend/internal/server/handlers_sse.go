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

// SSEHub manages SSE client connections and topic broadcasting.
type SSEHub struct {
	mu      sync.RWMutex
	clients map[string]chan *SSEEvent // clientID → event channel
	nextID  int
}

// SSEEvent is a single server-sent event.
type SSEEvent struct {
	Event string      `json:"event"`
	Data  interface{} `json:"data"`
	ID    string      `json:"id,omitempty"`
}

// NewSSEHub creates a new SSEHub.
func NewSSEHub() *SSEHub {
	return &SSEHub{
		clients: make(map[string]chan *SSEEvent),
	}
}

// Register adds a new SSE client and returns its ID and event channel.
func (h *SSEHub) Register() (string, <-chan *SSEEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.nextID++
	clientID := fmt.Sprintf("sse-%d-%d", h.nextID, time.Now().UnixNano())
	ch := make(chan *SSEEvent, 64) // buffered to avoid blocking sender
	h.clients[clientID] = ch

	slog.Info("SSE client registered", "client_id", clientID, "total", len(h.clients))
	return clientID, ch
}

// Unregister removes an SSE client.
func (h *SSEHub) Unregister(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if ch, ok := h.clients[clientID]; ok {
		close(ch)
		delete(h.clients, clientID)
		slog.Info("SSE client unregistered", "client_id", clientID, "total", len(h.clients))
	}
}

// Broadcast sends an event to all connected SSE clients.
func (h *SSEHub) Broadcast(event *SSEEvent) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	for _, ch := range h.clients {
		select {
		case ch <- event:
		default:
			// Client buffer full, skip (don't block other clients)
			slog.Warn("SSE client buffer full, skipping event", "event", event.Event)
		}
	}
}

// ClientCount returns the number of connected clients.
func (h *SSEHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
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

	// Register client
	clientID, eventCh := s.sseHub.Register()
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

// handleSSEPushTopic allows external systems (or admin) to push a topic to all SSE clients.
func (s *Server) handleSSEPushTopic(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title       string `json:"title"`
		Description string `json:"description"`
		Source      string `json:"source"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		response.Err(w, http.StatusBadRequest, "bad_request", "invalid request body")
		return
	}

	if req.Title == "" {
		response.Err(w, http.StatusBadRequest, "bad_request", "title is required")
		return
	}

	if req.Source == "" {
		req.Source = "manual"
	}

	// Save to database if available
	if s.traces != nil {
		s.traces.CreateTopic(r.Context(), req.Title, req.Description, "")
	}

	// Broadcast to SSE clients
	s.sseHub.Broadcast(&SSEEvent{
		Event: "topic:new",
		Data: map[string]interface{}{
			"title":       req.Title,
			"description": req.Description,
			"source":      req.Source,
			"timestamp":   time.Now().Format(time.RFC3339),
		},
	})

	response.OK(w, map[string]interface{}{
		"title":    req.Title,
		"source":   req.Source,
		"pushed":   true,
		"clients":  s.sseHub.ClientCount(),
		"message":  "topic pushed to all SSE clients",
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
