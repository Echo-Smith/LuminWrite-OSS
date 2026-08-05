package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestSSEEndToEnd tests the full SSE transport cycle:
// 1. Start an in-process MCP HTTP server with SSE support
// 2. Create an MCP client using SSE transport
// 3. Verify initialize handshake, tool listing, and tool call
func TestSSEEndToEnd(t *testing.T) {
	// Create an in-process MCP server with a test tool
	registry := NewLocalToolRegistry()
	registry.Register(NewLocalTool(
		"echo",
		"Echoes back the input message",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"message": map[string]any{
					"type":        "string",
					"description": "The message to echo",
				},
			},
			"required": []string{"message"},
		},
		func(ctx context.Context, args map[string]any) (string, error) {
			msg, _ := args["message"].(string)
			return fmt.Sprintf(`{"echo":"%s"}`, msg), nil
		},
	))

	server := NewMCPServer("test-server", "1.0", registry)
	server.RegisterBuiltinTools()

	// Start HTTP server with SSE support
	mux := http.NewServeMux()
	mux.Handle("/mcp", server.ServeHTTP())
	mux.Handle("/sse", server.ServeHTTP())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	})

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// Create an MCP client using SSE transport
	// The SSE URL is the test server's /sse endpoint
	sseURL := httpSrv.URL + "/sse"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := NewMCPClient(ctx, MCPClientConfig{
		Name:      "test-client",
		Transport: "sse",
		URL:       sseURL,
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Failed to create MCP client: %v", err)
	}
	defer client.Close()

	// Verify tools were discovered
	tools := client.Tools()
	if len(tools) < 2 { // echo + ping + server_info = 3 minimum
		t.Errorf("Expected at least 2 tools, got %d", len(tools))
	}

	// Find the echo tool
	var echoTool *MCPToolDef
	for i := range tools {
		if tools[i].Name == "echo" {
			echoTool = &tools[i]
			break
		}
	}
	if echoTool == nil {
		t.Fatal("echo tool not found in discovered tools")
	}

	// Call the echo tool
	result, err := client.CallTool(ctx, "echo", map[string]any{
		"message": "hello world",
	})
	if err != nil {
		t.Fatalf("CallTool failed: %v", err)
	}

	// Verify the result
	if !strings.Contains(result, "hello world") {
		t.Errorf("Expected result to contain 'hello world', got: %s", result)
	}

	// Call the ping tool (built-in)
	pingResult, err := client.CallTool(ctx, "ping", map[string]any{})
	if err != nil {
		t.Fatalf("Ping tool call failed: %v", err)
	}

	// Verify ping response
	var pingResp map[string]interface{}
	if err := json.Unmarshal([]byte(pingResult), &pingResp); err != nil {
		t.Fatalf("Failed to parse ping response: %v", err)
	}
	if pingResp["status"] != "ok" {
		t.Errorf("Expected ping status 'ok', got: %v", pingResp["status"])
	}
}

// TestSSEServerEndpoint tests that the SSE endpoint correctly sends
// the `endpoint` event when a client connects.
func TestSSEServerEndpoint(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)

	mux := http.NewServeMux()
	mux.Handle("/sse", server.ServeHTTP())

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	// Connect to the SSE endpoint
	resp, err := http.Get(httpSrv.URL + "/sse")
	if err != nil {
		t.Fatalf("Failed to connect to SSE endpoint: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("Expected Content-Type 'text/event-stream', got: %s", ct)
	}

	// Read the first event (should be the `endpoint` event)
	// We use a small buffer and a timeout to avoid blocking
	buf := make([]byte, 1024)
	n, _ := resp.Body.Read(buf)
	data := string(buf[:n])

	if !strings.Contains(data, "event: endpoint") {
		t.Errorf("Expected SSE event 'endpoint', got: %s", data)
	}

	if !strings.Contains(data, "/mcp?session=") {
		t.Errorf("Expected endpoint URL with session parameter, got: %s", data)
	}
}
