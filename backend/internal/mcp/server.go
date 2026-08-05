package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ─── In-Process MCP Server ────────────────────────────────
//
// MCPServer exposes local Go tools via the MCP (Model Context Protocol)
// JSON-RPC 2.0 interface.  It can serve over:
//   - stdio: for local MCP clients (e.g. Claude Desktop)
//   - HTTP/SSE: for remote MCP clients
//
// Lifecycle:
//   1. Register tools via LocalToolRegistry
//   2. Start serving via ServeStdio() or ServeHTTP()
//   3. The server handles initialize → tools/list → tools/call
//   4. Shutdown via Close()
//
// This is the "进程内 MCP Server" pattern: instead of spawning an external
// process, the tools live in-process and are exposed through the same
// MCP protocol that external servers use.

// MCPServer is the in-process MCP server.
type MCPServer struct {
	registry *LocalToolRegistry
	name     string
	version  string

	mu      sync.Mutex
	closed  bool
	nextID  atomic.Int64
	pending map[int64]chan *mcpResponse

	// For stdio mode
	stdin  io.ReadCloser
	stdout io.WriteCloser
	cancel context.CancelFunc

	// For SSE mode: maps session ID → SSE writer
	sseSessions sync.Map // string → *sseSession
}

// NewMCPServer creates a new in-process MCP server.
func NewMCPServer(name, version string, registry *LocalToolRegistry) *MCPServer {
	if registry == nil {
		registry = NewLocalToolRegistry()
	}
	if name == "" {
		name = "luminbuddy-writing-agent"
	}
	if version == "" {
		version = "2.0"
	}
	return &MCPServer{
		registry: registry,
		name:     name,
		version:  version,
		pending:  make(map[int64]chan *mcpResponse),
	}
}

// Registry returns the local tool registry (for registering more tools).
func (s *MCPServer) Registry() *LocalToolRegistry {
	return s.registry
}

// ─── stdio transport ─────────────────────────────────────

// ServeStdio starts serving the MCP protocol over stdin/stdout.
// This blocks until the input stream is closed or Close() is called.
// Typically called from a separate goroutine or a CLI subcommand.
func (s *MCPServer) ServeStdio(ctx context.Context) error {
	s.stdin = os.Stdin
	s.stdout = os.Stdout

	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	defer cancel()

	slog.Info("MCP server starting (stdio mode)",
		"name", s.name,
		"tools", s.registry.Count(),
	)

	scanner := bufio.NewScanner(s.stdin)
	// MCP messages can be large; increase buffer size
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Check for shutdown
		if s.isClosed() {
			break
		}

		// Process the JSON-RPC message
		response := s.handleMessage(ctx, line)
		if response != nil {
			if err := s.writeResponse(response); err != nil {
				slog.Error("MCP server: failed to write response", "error", err)
				return err
			}
		}
	}

	if err := scanner.Err(); err != nil && !s.isClosed() {
		slog.Error("MCP server: scanner error", "error", err)
		return err
	}

	slog.Info("MCP server stopped (stdio mode)")
	return nil
}

// ─── HTTP/SSE transport ───────────────────────────────────

// ServeHTTP returns an http.Handler that serves the MCP protocol over HTTP.
// Each request is a JSON-RPC 2.0 message; responses are returned as JSON.
// For SSE mode, the client opens a long-lived connection to /sse to receive
// streaming responses, and POSTs messages to /mcp?session=<id>.
func (s *MCPServer) ServeHTTP() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GET /sse — open SSE stream
		if r.Method == http.MethodGet && r.URL.Path == "/sse" {
			s.handleSSEStream(w, r)
			return
		}

		// Only accept POST for JSON-RPC
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read body", http.StatusBadRequest)
			return
		}

		response := s.handleMessage(r.Context(), body)

		// Check if this is an SSE session request
		sessionID := r.URL.Query().Get("session")
		if sessionID != "" {
			// SSE mode: write response to the SSE stream
			if sess, ok := s.getSSESession(sessionID); ok {
				if response != nil {
					sess.write(response)
				}
				w.WriteHeader(http.StatusAccepted)
				return
			}
			// Session not found — fall through to JSON response
		}

		// Non-SSE mode: return JSON directly (backward compatible)
		if response == nil {
			// Notification — no response
			w.WriteHeader(http.StatusAccepted)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(response)
	})
}

// ─── JSON-RPC message handling ────────────────────────────

// handleMessage processes a single JSON-RPC 2.0 message and returns
// the response (or nil for notifications, which don't get responses).
func (s *MCPServer) handleMessage(ctx context.Context, data []byte) *mcpResponse {
	var req mcpRequest
	if err := json.Unmarshal(data, &req); err != nil {
		slog.Warn("MCP server: failed to parse message", "error", err, "data", string(data))
		return &mcpResponse{
			JSONRPC: "2.0",
			Error: &jsonrpcError{
				Code:    -32700,
				Message: "parse error",
			},
		}
	}

	// Notifications (no ID) don't get responses
	if len(req.ID) == 0 {
		s.handleNotification(ctx, &req)
		return nil
	}

	return s.handleRequest(ctx, &req)
}

// handleNotification processes a notification (no response expected).
func (s *MCPServer) handleNotification(ctx context.Context, req *mcpRequest) {
	switch req.Method {
	case "notifications/initialized":
		slog.Debug("MCP server: client initialized notification received")
	case "notifications/cancelled":
		slog.Debug("MCP server: cancellation notification received", "id", req.Params)
	default:
		slog.Debug("MCP server: unknown notification", "method", req.Method)
	}
}

// handleRequest processes a request and returns the response.
func (s *MCPServer) handleRequest(ctx context.Context, req *mcpRequest) *mcpResponse {
	switch req.Method {
	case "initialize":
		return s.handleInitialize(req)

	case "tools/list":
		return s.handleToolsList(req)

	case "tools/call":
		return s.handleToolsCall(ctx, req)

	case "ping":
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  json.RawMessage(`{}`),
		}

	default:
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonrpcError{
				Code:    -32601,
				Message: fmt.Sprintf("method not found: %s", req.Method),
			},
		}
	}
}

// handleInitialize handles the MCP initialize handshake.
func (s *MCPServer) handleInitialize(req *mcpRequest) *mcpResponse {
	result := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools": map[string]any{
				"listChanged": true,
			},
		},
		"serverInfo": map[string]any{
			"name":    s.name,
			"version": s.version,
		},
	}

	resultJSON, _ := json.Marshal(result)

	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

// handleToolsList returns all registered tools.
func (s *MCPServer) handleToolsList(req *mcpRequest) *mcpResponse {
	tools := s.registry.All()

	toolDefs := make([]MCPToolDef, 0, len(tools))
	for _, t := range tools {
		toolDefs = append(toolDefs, toMCPToolDef(t))
	}

	result := map[string]any{
		"tools": toolDefs,
	}

	resultJSON, _ := json.Marshal(result)

	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

// handleToolsCall executes a tool call.
func (s *MCPServer) handleToolsCall(ctx context.Context, req *mcpRequest) *mcpResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonrpcError{
				Code:    -32602,
				Message: "invalid params: " + err.Error(),
			},
		}
	}

	tool := s.registry.Get(params.Name)
	if tool == nil {
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &jsonrpcError{
				Code:    -32602,
				Message: fmt.Sprintf("unknown tool: %s", params.Name),
			},
		}
	}

	args := marshalArgs(params.Arguments)

	slog.Info("MCP server: tool call",
		"tool", params.Name,
		"args_keys", func() []string {
			keys := make([]string, 0, len(args))
			for k := range args {
				keys = append(keys, k)
			}
			return keys
		}(),
	)

	result, err := tool.Execute(ctx, args)
	if err != nil {
		// Return tool error as MCP error response
		errorResult := map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": err.Error(),
				},
			},
			"isError": true,
		}
		resultJSON, _ := json.Marshal(errorResult)
		return &mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  resultJSON,
		}
	}

	// Success: return content array with text
	mcpResult := map[string]any{
		"content": []map[string]any{
			{
				"type": "text",
				"text": result,
			},
		},
		"isError": false,
	}

	resultJSON, _ := json.Marshal(mcpResult)

	return &mcpResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result:  resultJSON,
	}
}

// ─── Helpers ──────────────────────────────────────────────

// writeResponse writes a JSON-RPC response to stdout (stdio mode).
func (s *MCPServer) writeResponse(resp *mcpResponse) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}

	// Write as a single line (newline-delimited JSON)
	if _, err := s.stdout.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	return nil
}

// isClosed returns true if the server has been closed.
func (s *MCPServer) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Close shuts down the MCP server.
func (s *MCPServer) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return
	}
	s.closed = true

	if s.cancel != nil {
		s.cancel()
	}

	slog.Info("MCP server closed", "name", s.name)
}

// ─── JSON-RPC 2.0 request type ────────────────────────────

// mcpRequest is a JSON-RPC 2.0 request for the in-process MCP server.
// This is separate from client.go's jsonrpcRequest because the server
// needs to handle raw IDs (for notification detection) and raw params.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// mcpResponse is a JSON-RPC 2.0 response from the in-process MCP server.
// Uses json.RawMessage for ID so we can echo back string or number IDs.
type mcpResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

// ─── Built-in Local Tools ─────────────────────────────────

// RegisterBuiltinTools registers useful built-in tools for the MCP server.
// These are lightweight tools that don't require engine context:
//   - ping: health check
//   - server_info: return server metadata
//   - list_tools: list all available tools (meta-tool)
func (s *MCPServer) RegisterBuiltinTools() {
	s.registry.Register(NewLocalTool(
		"ping",
		"Health check — returns pong with timestamp",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			return fmt.Sprintf(`{"status":"ok","pong":"%s"}`, time.Now().Format(time.RFC3339)), nil
		},
	))

	s.registry.Register(NewLocalTool(
		"server_info",
		"Return MCP server metadata (name, version, tool count)",
		map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			info := map[string]any{
				"name":        s.name,
				"version":     s.version,
				"tool_count":  s.registry.Count(),
				"transport":   "stdio+http",
			}
			data, _ := json.Marshal(info)
			return string(data), nil
		},
	))
}

// ─── HTTP Server Wrapper ──────────────────────────────────

// StartHTTPServer starts the MCP server on an HTTP listener.
// Returns the *http.Server so the caller can GracefulStop it.
func (s *MCPServer) StartHTTPServer(addr string) *http.Server {
	mux := http.NewServeMux()

	// MCP endpoint (POST /mcp for JSON-RPC, GET /sse for SSE stream)
	mux.Handle("/mcp", s.ServeHTTP())
	mux.Handle("/sse", s.ServeHTTP())

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"status":     "ok",
			"mcp_server": s.name,
			"tools":      s.registry.Count(),
		})
	})

	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // no write timeout — SSE connections are long-lived
	}

	go func() {
		slog.Info("MCP HTTP server starting", "addr", addr, "tools", s.registry.Count())
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("MCP HTTP server error", "error", err)
		}
	}()

	return srv
}

// ─── SSE Session Management ───────────────────────────────

// sseSession represents an active SSE connection from a client.
type sseSession struct {
	id     string
	writer http.ResponseWriter
	flusher http.Flusher
	done   chan struct{}
}

// write sends a JSON-RPC response to the SSE client.
func (sess *sseSession) write(resp *mcpResponse) {
	data, err := json.Marshal(resp)
	if err != nil {
		return
	}
	fmt.Fprintf(sess.writer, "data: %s\n\n", data)
	if sess.flusher != nil {
		sess.flusher.Flush()
	}
}

// handleSSEStream handles GET /sse — opens a text/event-stream connection.
func (s *MCPServer) handleSSEStream(w http.ResponseWriter, r *http.Request) {
	// Check that the ResponseWriter supports flushing
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Generate a session ID
	sessionID := fmt.Sprintf("sess_%d", s.nextID.Add(1))

	// Register the session
	sess := &sseSession{
		id:      sessionID,
		writer:  w,
		flusher: flusher,
		done:    make(chan struct{}),
	}
	s.sseSessions.Store(sessionID, sess)
	defer s.sseSessions.Delete(sessionID)

	slog.Info("MCP SSE client connected", "session", sessionID)

	// Send the `endpoint` event — tells the client where to POST messages
	endpointURL := fmt.Sprintf("/mcp?session=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	// Block until the client disconnects or server closes
	select {
	case <-r.Context().Done():
	case <-sess.done:
	}

	slog.Info("MCP SSE client disconnected", "session", sessionID)
}

// getSSESession retrieves an SSE session by ID.
func (s *MCPServer) getSSESession(id string) (*sseSession, bool) {
	v, ok := s.sseSessions.Load(id)
	if !ok {
		return nil, false
	}
	return v.(*sseSession), true
}

// ─── Stdin reader for testing ─────────────────────────────

// ServeStdioWithReader is like ServeStdio but uses custom io.Reader/Writer.
// This is primarily for testing.
func (s *MCPServer) ServeStdioWithReader(ctx context.Context, stdin io.Reader, stdout io.Writer) error {
	s.mu.Lock()
	s.cancel = nil
	s.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 0, 1024*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		if s.isClosed() {
			break
		}

		response := s.handleMessage(ctx, line)
		if response != nil {
			data, err := json.Marshal(response)
			if err != nil {
				return fmt.Errorf("failed to marshal response: %w", err)
			}
			if _, err := stdout.Write(append(data, '\n')); err != nil {
				return fmt.Errorf("failed to write response: %w", err)
			}
		}
	}

	return scanner.Err()
}

// ─── Utility: extract raw ID from request ─────────────────

// getRawID extracts the raw JSON ID from a request (for logging).
func getRawID(req *mcpRequest) string {
	if len(req.ID) == 0 {
		return ""
	}
	id := strings.TrimSpace(string(req.ID))
	return id
}
