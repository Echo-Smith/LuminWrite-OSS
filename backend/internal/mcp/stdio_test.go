package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestStdioTransportEndToEnd tests the stdio transport by using
// ServeStdioWithReader with pipe-based I/O (no subprocess needed).
//
// The test verifies:
// 1. The server responds to initialize over stdio
// 2. Tool listing works over stdio
// 3. A tool call returns the expected result
// 4. Malformed input returns an error without crashing the server
func TestStdioTransportEndToEnd(t *testing.T) {
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
			return `{"echo":"` + msg + `"}`, nil
		},
	))

	server := NewMCPServer("test-stdio-server", "1.0", registry)
	server.RegisterBuiltinTools()

	// Build the input: initialize + tools/list + tools/call
	inputs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test-stdio","version":"1.0"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"echo","arguments":{"message":"hello stdio"}}}`,
	}
	stdin := strings.NewReader(strings.Join(inputs, "\n") + "\n")

	var stdout bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.ServeStdioWithReader(ctx, stdin, &stdout)
	if err != nil {
		t.Fatalf("ServeStdioWithReader failed: %v", err)
	}

	// Parse responses (newline-delimited JSON)
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) < 3 {
		t.Fatalf("Expected at least 3 response lines, got %d: %s", len(lines), stdout.String())
	}

	// Check initialize response
	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("Failed to parse initialize response: %v\n%s", err, lines[0])
	}
	if initResp["id"] != float64(1) {
		t.Errorf("Expected id=1, got %v", initResp["id"])
	}
	if initResp["result"] == nil {
		t.Errorf("Expected result in initialize response")
	}

	// Check tools/list response
	var listResp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatalf("Failed to parse tools/list response: %v\n%s", err, lines[1])
	}
	result, _ := listResp["result"].(map[string]interface{})
	tools, _ := result["tools"].([]interface{})
	if len(tools) < 2 {
		t.Errorf("Expected at least 2 tools, got %d", len(tools))
	}

	// Check echo tool call response
	var echoResp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[2]), &echoResp); err != nil {
		t.Fatalf("Failed to parse echo response: %v\n%s", err, lines[2])
	}
	if echoResp["id"] != float64(3) {
		t.Errorf("Expected id=3, got %v", echoResp["id"])
	}
	echoResult, _ := echoResp["result"].(map[string]interface{})
	content, _ := echoResult["content"].([]interface{})
	if len(content) == 0 {
		t.Errorf("Expected content in echo response")
	}
}

// TestStdioMalformedInput tests that the stdio server handles
// malformed JSON gracefully without crashing.
func TestStdioMalformedInput(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)
	server.RegisterBuiltinTools()

	// Send malformed JSON followed by a valid request
	inputs := []string{
		"this is not valid json",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}`,
	}
	stdin := strings.NewReader(strings.Join(inputs, "\n") + "\n")

	var stdout bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := server.ServeStdioWithReader(ctx, stdin, &stdout)
	if err != nil {
		t.Fatalf("ServeStdioWithReader failed on malformed input: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")

	// First response should be an error for the malformed input
	var errResp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[0]), &errResp); err != nil {
		t.Fatalf("Failed to parse error response: %v\n%s", err, lines[0])
	}
	if errResp["error"] == nil {
		t.Errorf("Expected error for malformed input, got: %s", lines[0])
	}

	// Second response should be valid (server recovered)
	var initResp map[string]interface{}
	if err := json.Unmarshal([]byte(lines[1]), &initResp); err != nil {
		t.Fatalf("Failed to parse init response after error recovery: %v\n%s", err, lines[1])
	}
	if initResp["result"] == nil {
		t.Errorf("Expected valid result after error recovery, got: %s", lines[1])
	}
}

// TestStdioUnknownMethod tests that the server returns a proper error
// for unknown methods.
func TestStdioUnknownMethod(t *testing.T) {
	registry := NewLocalToolRegistry()
	server := NewMCPServer("test-server", "1.0", registry)
	server.RegisterBuiltinTools()

	input := `{"jsonrpc":"2.0","id":1,"method":"nonexistent/method","params":{}}` + "\n"
	stdin := strings.NewReader(input)
	var stdout bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_ = server.ServeStdioWithReader(ctx, stdin, &stdout)

	var resp map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse response: %v\n%s", err, stdout.String())
	}

	if resp["error"] == nil {
		t.Errorf("Expected error for unknown method, got: %s", stdout.String())
	}

	errObj, _ := resp["error"].(map[string]interface{})
	code, _ := errObj["code"].(float64)
	if code != -32601 {
		t.Errorf("Expected error code -32601 (method not found), got %v", code)
	}
}
