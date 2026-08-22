package websocket

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

const (
	writeTimeout    = 120 * time.Second
	readTimeout     = 600 * time.Second // 10 min — allows time for user to edit outlines in guided mode
	bufSize         = 256
	heartbeatPeriod = 30 * time.Second // send ping every 30s to keep Nginx proxy alive
)

// WSMetrics holds lightweight atomic counters for WebSocket errors.
// These are read by the server's /metrics endpoint.
var WSMetrics = struct {
	ReadErrors  atomic.Int64
	WriteErrors atomic.Int64
	ParseErrors atomic.Int64
}{}

// Client represents a single WebSocket connection.
type Client struct {
	conn    *websocket.Conn
	send    chan []byte
	traceID string
	mu      sync.Mutex
}

// NewClient creates a new WebSocket client.
func NewClient(conn *websocket.Conn) *Client {
	return &Client{
		conn: conn,
		send: make(chan []byte, bufSize),
	}
}

// Send queues a message to be sent to the client.
func (c *Client) Send(msg *ServerMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		slog.Error("failed to marshal server message", "error", err)
		return
	}
	select {
	case c.send <- data:
	default:
		slog.Warn("client send buffer full, dropping message", "trace_id", c.traceID)
	}
}

// SendDirect writes a message directly to the connection (low latency path for streaming).
func (c *Client) SendDirect(msg *ServerMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, data)
}

// SetTraceID associates a trace ID with this client.
func (c *Client) SetTraceID(traceID string) {
	c.traceID = traceID
}

// TraceID returns the associated trace ID.
func (c *Client) TraceID() string {
	return c.traceID
}

// ReadLoop reads messages from the WebSocket connection.
// It also starts a heartbeat goroutine that sends ping messages every 30s
// to keep the connection alive through Nginx reverse proxies (default
// proxy_read_timeout is 60s).
func (c *Client) ReadLoop(handler func(*ClientMessage)) {
	defer close(c.send)

	// Start heartbeat ping goroutine
	go c.heartbeat()

	for {
		ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
		_, data, err := c.conn.Read(ctx)
		cancel()
		if err != nil {
			if websocket.CloseStatus(err) != -1 {
				slog.Debug("websocket closed", "trace_id", c.traceID)
			} else {
				slog.Error("websocket read error", "error", err, "trace_id", c.traceID)
				WSMetrics.ReadErrors.Add(1)
			}
			return
		}

		var msg ClientMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			slog.Error("failed to unmarshal client message", "error", err)
			WSMetrics.ParseErrors.Add(1)
			continue
		}
		handler(&msg)
	}
}

// WriteLoop writes queued messages to the WebSocket connection.
func (c *Client) WriteLoop() {
	for data := range c.send {
		c.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		err := c.conn.Write(ctx, websocket.MessageText, data)
		cancel()
		c.mu.Unlock()
		if err != nil {
			slog.Error("websocket write error", "error", err, "trace_id", c.traceID)
			WSMetrics.WriteErrors.Add(1)
			return
		}
	}
}

// heartbeat sends a ping ServerMessage every heartbeatPeriod to keep
// the WebSocket connection alive. This is critical when behind Nginx or
// other reverse proxies that have idle timeout settings (default 60s).
// The ping is a lightweight JSON message; the client can optionally
// respond but is not required to.
func (c *Client) heartbeat() {
	ticker := time.NewTicker(heartbeatPeriod)
	defer ticker.Stop()

	for range ticker.C {
		c.mu.Lock()
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		pingMsg, _ := json.Marshal(&ServerMessage{
			Type:    "ping",
			Payload: map[string]interface{}{"ts": time.Now().Unix()},
		})
		err := c.conn.Write(ctx, websocket.MessageText, pingMsg)
		cancel()
		c.mu.Unlock()
		if err != nil {
			// Connection likely closed; stop heartbeat
			return
		}
	}
}

// Close closes the WebSocket connection.
func (c *Client) Close() {
	c.conn.Close(websocket.StatusNormalClosure, "")
}

// Hub manages WebSocket clients and their association with trace IDs.
type Hub struct {
	clients       map[string]*Client // traceID → client
	clientsByConn map[*websocket.Conn]*Client
	mu            sync.RWMutex
}

// NewHub creates a new Hub.
func NewHub() *Hub {
	return &Hub{
		clients:       make(map[string]*Client),
		clientsByConn: make(map[*websocket.Conn]*Client),
	}
}

// Register associates a client with a trace ID.
func (h *Hub) Register(traceID string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.clients[traceID]; ok {
		// Only close if it's a different connection; same connection re-registering
		// (e.g. workflow plan → execute on same WS) should not close itself.
		if old != client {
			old.Close()
		}
		delete(h.clients, traceID)
	}
	h.clients[traceID] = client
	h.clientsByConn[client.conn] = client
	client.SetTraceID(traceID)
	slog.Debug("client registered", "trace_id", traceID)
}

// Unregister removes a client.
func (h *Hub) Unregister(traceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if client, ok := h.clients[traceID]; ok {
		delete(h.clients, traceID)
		delete(h.clientsByConn, client.conn)
	}
}

// GetClient returns the client for a given trace ID.
func (h *Hub) GetClient(traceID string) (*Client, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	c, ok := h.clients[traceID]
	return c, ok
}

// SendToTrace sends a message to the client associated with a trace ID.
func (h *Hub) SendToTrace(traceID string, msg *ServerMessage) bool {
	h.mu.RLock()
	client, ok := h.clients[traceID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	client.Send(msg)
	return true
}

// SendToTraceDirect sends a message directly (low latency) to the client.
func (h *Hub) SendToTraceDirect(traceID string, msg *ServerMessage) bool {
	h.mu.RLock()
	client, ok := h.clients[traceID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	return client.SendDirect(msg) == nil
}

// Broadcast sends a message to all connected clients.
func (h *Hub) Broadcast(msg *ServerMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, client := range h.clients {
		client.Send(msg)
	}
}
