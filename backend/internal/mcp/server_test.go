package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ─── LocalToolRegistry Tests ─────────────────────────────

func TestLocalToolRegistry_RegisterAndGet(t *testing.T) {
	r := NewLocalToolRegistry()
	tool := NewLocalTool("test", "A test tool",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "ok", nil
		},
	)
	r.Register(tool)

	got := r.Get("test")
	if got == nil {
		t.Fatal("expected to get registered tool")
	}
	if got.Name() != "test" {
		t.Errorf("expected name 'test', got '%s'", got.Name())
	}
}

func TestLocalToolRegistry_GetNonExistent(t *testing.T) {
	r := NewLocalToolRegistry()
	if r.Get("nonexistent") != nil {
		t.Fatal("expected nil for non-existent tool")
	}
}

func TestLocalToolRegistry_Count(t *testing.T) {
	r := NewLocalToolRegistry()
	r.Register(NewLocalTool("a", "A", nil, func(ctx context.Context, args map[string]any) (string, error) { return "", nil }))
	r.Register(NewLocalTool("b", "B", nil, func(ctx context.Context, args map[string]any) (string, error) { return "", nil }))
	if r.Count() != 2 {
		t.Errorf("expected count 2, got %d", r.Count())
	}
}

func TestLocalToolFunc_Execute(t *testing.T) {
	tool := NewLocalTool("echo", "Echo tool",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) {
			msg, _ := args["message"].(string)
			return msg, nil
		},
	)

	result, err := tool.Execute(context.Background(), map[string]any{"message": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got '%s'", result)
	}
}

// ─── AgentToolAdapter Tests ───────────────────────────────

func TestAgentToolAdapter(t *testing.T) {
	mock := &mockAgentTool{
		name:        "adapted_tool",
		description: "An adapted tool",
		schema:      map[string]any{"type": "object"},
	}
	exec := func(ctx context.Context, name string, args map[string]any) (string, error) {
		return "executed: " + name, nil
	}

	adapter := NewAgentToolAdapter(mock, exec)
	if adapter.Name() != "adapted_tool" {
		t.Errorf("expected name 'adapted_tool', got '%s'", adapter.Name())
	}
	if adapter.Description() != "An adapted tool" {
		t.Errorf("unexpected description")
	}

	result, err := adapter.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "executed: adapted_tool" {
		t.Errorf("unexpected result: %s", result)
	}
}

// mockAgentTool implements AgentToolLike for testing.
type mockAgentTool struct {
	name        string
	description string
	schema      map[string]any
}

func (m *mockAgentTool) Name() string           { return m.name }
func (m *mockAgentTool) Description() string    { return m.description }
func (m *mockAgentTool) Schema() map[string]any { return m.schema }

// ─── MCPServer Protocol Tests ────────────────────────────

func TestMCPServer_Initialize(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	// Build initialize request
	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil {
		t.Fatal("expected response, got nil")
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %s", resp.Error.Message)
	}

	// Parse result
	var result struct {
		ProtocolVersion string         `json:"protocolVersion"`
		Capabilities    map[string]any `json:"capabilities"`
		ServerInfo      map[string]any `json:"serverInfo"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if result.ProtocolVersion != "2024-11-05" {
		t.Errorf("expected protocol '2024-11-05', got '%s'", result.ProtocolVersion)
	}
	if result.ServerInfo["name"] != "test-server" {
		t.Errorf("expected server name 'test-server', got '%v'", result.ServerInfo["name"])
	}
}

func TestMCPServer_ToolsList(t *testing.T) {
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool("tool_a", "Tool A",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) { return "a", nil },
	))
	registry.Register(NewLocalTool("tool_b", "Tool B",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) { return "b", nil },
	))

	server := NewMCPServer("test-server", "1.0", registry)

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "tools/list",
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}

	var result struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if len(result.Tools) != 2 {
		t.Errorf("expected 2 tools, got %d", len(result.Tools))
	}
}

func TestMCPServer_ToolsCall(t *testing.T) {
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool("echo", "Echo tool",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{"type": "string"},
			},
			"required": []string{"message"},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			msg, _ := args["message"].(string)
			return "echo: " + msg, nil
		},
	))

	server := NewMCPServer("test-server", "1.0", registry)

	// Build tools/call request
	params, _ := json.Marshal(map[string]any{
		"name": "echo",
		"arguments": map[string]any{
			"message": "hello",
		},
	})

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/call",
		Params:  params,
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Parse MCP result
	var mcpResult struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &mcpResult); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if mcpResult.IsError {
		t.Error("expected success, got error")
	}
	if len(mcpResult.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(mcpResult.Content))
	}
	if mcpResult.Content[0].Text != "echo: hello" {
		t.Errorf("expected 'echo: hello', got '%s'", mcpResult.Content[0].Text)
	}
}

func TestMCPServer_ToolsCall_UnknownTool(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	params, _ := json.Marshal(map[string]any{
		"name":      "nonexistent",
		"arguments": map[string]any{},
	})

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  params,
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Error == nil {
		t.Fatal("expected error for unknown tool")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected error code -32602, got %d", resp.Error.Code)
	}
}

func TestMCPServer_ToolsCall_ToolError(t *testing.T) {
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool("failing", "Always fails",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "", &testError{msg: "tool execution failed"}
		},
	))

	server := NewMCPServer("test-server", "1.0", registry)

	params, _ := json.Marshal(map[string]any{
		"name":      "failing",
		"arguments": map[string]any{},
	})

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "tools/call",
		Params:  params,
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil {
		t.Fatal("expected response")
	}

	// Tool errors should be returned as MCP error results (not JSON-RPC errors)
	var mcpResult struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(resp.Result, &mcpResult); err != nil {
		t.Fatalf("failed to parse result: %v", err)
	}

	if !mcpResult.IsError {
		t.Error("expected isError=true")
	}
}

func TestMCPServer_Notification(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	// Notifications have no ID
	req := mcpRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	resp := server.handleMessage(context.Background(), mustMarshal(t, req))
	if resp != nil {
		t.Error("expected nil response for notification")
	}
}

func TestMCPServer_UnknownMethod(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`6`),
		Method:  "unknown/method",
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error response")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestMCPServer_Ping(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	req := mcpRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`7`),
		Method:  "ping",
	}

	resp := server.handleRequest(context.Background(), &req)
	if resp == nil || resp.Error != nil {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestMCPServer_BuiltinTools(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)
	server.RegisterBuiltinTools()

	// Check ping tool
	ping := registry.Get("ping")
	if ping == nil {
		t.Fatal("expected ping tool after RegisterBuiltinTools")
	}

	result, err := ping.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("ping failed: %v", err)
	}
	if !strings.Contains(result, "ok") {
		t.Errorf("expected 'ok' in ping result, got '%s'", result)
	}

	// Check server_info tool
	info := registry.Get("server_info")
	if info == nil {
		t.Fatal("expected server_info tool")
	}

	result, err = info.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("server_info failed: %v", err)
	}
	if !strings.Contains(result, "test-server") {
		t.Errorf("expected server name in result, got '%s'", result)
	}
}

// ─── MCPServer stdio Transport Tests ──────────────────────

func TestMCPServer_ServeStdioWithReader(t *testing.T) {
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool("echo", "Echo",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) {
			msg, _ := args["message"].(string)
			return msg, nil
		},
	))

	server := NewMCPServer("test-server", "1.0", registry)

	// Simulate an MCP client: send initialize + tools/list
	initReq := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	listReq := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`

	input := initReq + "\n" + listReq + "\n"
	var output bytes.Buffer

	err := server.ServeStdioWithReader(context.Background(), strings.NewReader(input), &output)
	if err != nil {
		t.Fatalf("ServeStdioWithReader failed: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 response lines, got %d", len(lines))
	}

	// Check initialize response
	var initResp mcpResponse
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("failed to parse init response: %v", err)
	}
	if initResp.Error != nil {
		t.Errorf("init error: %s", initResp.Error.Message)
	}

	// Check tools/list response
	var listResp mcpResponse
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("failed to parse list response: %v", err)
	}
	if listResp.Error != nil {
		t.Errorf("list error: %s", listResp.Error.Message)
	}

	var listResult struct {
		Tools []MCPToolDef `json:"tools"`
	}
	if err := json.Unmarshal(listResp.Result, &listResult); err != nil {
		t.Fatalf("failed to parse tools: %v", err)
	}
	if len(listResult.Tools) != 1 {
		t.Errorf("expected 1 tool, got %d", len(listResult.Tools))
	}
	if listResult.Tools[0].Name != "echo" {
		t.Errorf("expected tool 'echo', got '%s'", listResult.Tools[0].Name)
	}
}

// ─── HTTP Handler Tests ───────────────────────────────────

func TestMCPServer_HTTPHandler(t *testing.T) {
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool("echo", "Echo",
		map[string]any{"type": "object"},
		func(ctx context.Context, args map[string]any) (string, error) {
			return "hello", nil
		},
	))

	server := NewMCPServer("test-server", "1.0", registry)
	handler := server.ServeHTTP()

	// Test tools/list via HTTP
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	req, _ := http.NewRequest("POST", "/mcp", strings.NewReader(body))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp mcpResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if resp.Error != nil {
		t.Errorf("unexpected error: %s", resp.Error.Message)
	}
}

// ─── Helpers ──────────────────────────────────────────────

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

func mustMarshal(t *testing.T, v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}
	return data
}
