package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"
)

// ─── MCP Protocol Types ──────────────────────────────────

// MCPToolDef is a tool definition returned by an MCP server's tools/list.
type MCPToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// MCPToolResult is the result of a tools/call.
type MCPToolResult struct {
	Content []struct {
		Type string `json:"type"` // "text" | "image" | "resource"
		Text string `json:"text,omitempty"`
	} `json:"content"`
	IsError bool `json:"isError,omitempty"`
}

// jsonrpcRequest is a JSON-RPC 2.0 request.
type jsonrpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Method  string          `json:"method"`
	Params  map[string]any  `json:"params,omitempty"`
}

// jsonrpcResponse is a JSON-RPC 2.0 response.
type jsonrpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonrpcError   `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ─── MCPClient ──────────────────────────────────────────
//
// MCPClient connects to an MCP server via stdio or SSE transport.
// It handles the JSON-RPC 2.0 protocol: initialize handshake,
// tool discovery (tools/list), and tool execution (tools/call).
//
// Lifecycle:
//   1. Connect (spawn process or open SSE)
//   2. Initialize (send "initialize", expect capabilities)
//   3. Send "notifications/initialized"
//   4. ListTools (send "tools/list", cache results)
//   5. CallTool (send "tools/call" with name + arguments)
//   6. Close (kill process or close SSE)

type MCPClient struct {
	name       string
	transport  string // "stdio" | "sse"
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	httpClient *http.Client
	sseURL     string

	mu       sync.Mutex
	nextID   atomic.Int64
	pending  map[int64]chan *jsonrpcResponse
	tools    []MCPToolDef
	closed   bool
}

// MCPClientConfig holds configuration for creating an MCP client.
type MCPClientConfig struct {
	Name    string `json:"name"`
	Transport string `json:"transport"` // "stdio" | "sse"

	// stdio transport
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	Env     []string `json:"env,omitempty"`

	// SSE transport
	URL     string        `json:"url,omitempty"`
	Timeout time.Duration `json:"timeout,omitempty"`
}

// NewMCPClient creates and connects a new MCP client.
func NewMCPClient(ctx context.Context, cfg MCPClientConfig) (*MCPClient, error) {
	c := &MCPClient{
		name:    cfg.Name,
		transport: cfg.Transport,
		pending: make(map[int64]chan *jsonrpcResponse),
	}

	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}

	switch cfg.Transport {
	case "stdio":
		if cfg.Command == "" {
			return nil, fmt.Errorf("stdio transport requires 'command'")
		}
		cmd := exec.Command(cfg.Command, cfg.Args...)
		if len(cfg.Env) > 0 {
			cmd.Env = cfg.Env
		}

		stdin, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
		}
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
		}

		c.cmd = cmd
		c.stdin = stdin
		c.stdout = stdout

		if err := cmd.Start(); err != nil {
			return nil, fmt.Errorf("failed to start MCP server: %w", err)
		}

		// Start reading responses
		go c.readLoop(stdout)

	case "sse":
		if cfg.URL == "" {
			return nil, fmt.Errorf("sse transport requires 'url'")
		}
		c.sseURL = cfg.URL
		c.httpClient = &http.Client{Timeout: cfg.Timeout}

	default:
		return nil, fmt.Errorf("unknown transport: %s (use 'stdio' or 'sse')", cfg.Transport)
	}

	// Initialize handshake
	if err := c.initialize(ctx); err != nil {
		c.Close()
		return nil, fmt.Errorf("MCP initialize failed: %w", err)
	}

	// Discover tools
	tools, err := c.ListTools(ctx)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("MCP tools/list failed: %w", err)
	}
	c.tools = tools

	slog.Info("MCP client connected",
		"name", c.name,
		"transport", c.transport,
		"tools", len(tools),
	)

	return c, nil
}

// initialize performs the MCP handshake.
func (c *MCPClient) initialize(ctx context.Context) error {
	resp, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "luminbuddy-writing-agent",
			"version": "2.0",
		},
	})
	if err != nil {
		return err
	}

	// Parse capabilities (we don't need to use them, just acknowledge)
	var initResult struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	_ = json.Unmarshal(resp.Result, &initResult)

	slog.Debug("MCP initialized",
		"server", c.name,
		"protocol_version", initResult.ProtocolVersion,
		"server_info", initResult.ServerInfo,
	)

	// Send initialized notification (no response expected)
	c.notify(ctx, "notifications/initialized", nil)

	return nil
}

// ListTools discovers available tools from the MCP server.
func (c *MCPClient) ListTools(ctx context.Context) ([]MCPToolDef, error) {
	resp, err := c.call(ctx, "tools/list", nil)
	if err != nil {
		return nil, err
	}

	var result struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return nil, fmt.Errorf("failed to parse tools/list: %w", err)
	}

	return result.Tools, nil
}

// CallTool executes a tool on the MCP server.
func (c *MCPClient) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	resp, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", err
	}

	var result MCPToolResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("failed to parse tools/call result: %w", err)
	}

	// Extract text content
	var texts []string
	for _, item := range result.Content {
		if item.Type == "text" && item.Text != "" {
			texts = append(texts, item.Text)
		}
	}

	output := ""
	if len(texts) > 0 {
		output = texts[0]
		if len(texts) > 1 {
			for _, t := range texts[1:] {
				output += "\n" + t
			}
		}
	}

	if result.IsError {
		return "", fmt.Errorf("MCP tool error: %s", output)
	}

	return output, nil
}

// Tools returns the cached tool definitions.
func (c *MCPClient) Tools() []MCPToolDef {
	return c.tools
}

// Name returns the server name.
func (c *MCPClient) Name() string {
	return c.name
}

// Close shuts down the connection.
func (c *MCPClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return
	}
	c.closed = true

	if c.transport == "stdio" {
		if c.stdin != nil {
			c.stdin.Close()
		}
		if c.cmd != nil && c.cmd.Process != nil {
			c.cmd.Process.Kill()
		}
	}

	slog.Info("MCP client closed", "server", c.name)
}

// ─── JSON-RPC Transport ──────────────────────────────────

// call sends a JSON-RPC request and waits for the response.
func (c *MCPClient) call(ctx context.Context, method string, params map[string]any) (*jsonrpcResponse, error) {
	id := c.nextID.Add(1)
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}

	respCh := make(chan *jsonrpcResponse, 1)
	c.mu.Lock()
	c.pending[id] = respCh
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}()

	if err := c.send(req); err != nil {
		return nil, fmt.Errorf("send %s: %w", method, err)
	}

	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("RPC %s error [%d]: %s", method, resp.Error.Code, resp.Error.Message)
		}
		return resp, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notify sends a JSON-RPC notification (no response expected).
func (c *MCPClient) notify(ctx context.Context, method string, params map[string]any) {
	req := jsonrpcRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	// notifications have no ID in JSON-RPC, but MCP accepts ID-less messages
	if err := c.send(req); err != nil {
		slog.Warn("MCP notify failed", "method", method, "error", err)
	}
}

// send writes a JSON-RPC message to the transport.
func (c *MCPClient) send(req jsonrpcRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}

	switch c.transport {
	case "stdio":
		// Write line-delimited JSON to stdin
		data = append(data, '\n')
		c.mu.Lock()
		_, err = c.stdin.Write(data)
		c.mu.Unlock()
		return err
	case "sse":
		// Send HTTP POST to the SSE endpoint
		resp, err := c.httpClient.Post(c.sseURL, "application/json", bytes.NewReader(data))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
		}
		// For SSE, the response comes as an event stream
		// For simplicity, we read the full body as the JSON-RPC response
		var rpcResp jsonrpcResponse
		if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
			return fmt.Errorf("failed to decode SSE response: %w", err)
		}
		// Dispatch to pending
		c.mu.Lock()
		if ch, ok := c.pending[rpcResp.ID]; ok {
			ch <- &rpcResp
		}
		c.mu.Unlock()
		return nil
	default:
		return fmt.Errorf("unknown transport: %s", c.transport)
	}
}

// readLoop reads JSON-RPC responses from stdio stdout.
func (c *MCPClient) readLoop(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024) // up to 1MB per line

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var resp jsonrpcResponse
		if err := json.Unmarshal(line, &resp); err != nil {
			slog.Debug("MCP: unparseable line from stdout", "server", c.name, "line_len", len(line))
			continue
		}

		// Dispatch to pending caller
		c.mu.Lock()
		if ch, ok := c.pending[resp.ID]; ok {
			select {
			case ch <- &resp:
			default:
				slog.Warn("MCP: response channel full, dropping", "id", resp.ID, "server", c.name)
			}
		}
		c.mu.Unlock()
	}

	if err := scanner.Err(); err != nil && !c.closed {
		slog.Warn("MCP stdio reader stopped", "server", c.name, "error", err)
	}
}
