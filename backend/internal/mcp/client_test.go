package mcp

import (
	"encoding/json"
	"testing"
)

// ─── MCP Protocol Types Tests ────────────────────────────

func TestMCPToolDef_JSONRoundTrip(t *testing.T) {
	original := MCPToolDef{
		Name:        "read_file",
		Description: "Read a file from disk",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "File path",
				},
			},
			"required": []string{"path"},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var decoded MCPToolDef
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if decoded.Name != original.Name {
		t.Errorf("name mismatch: %s vs %s", decoded.Name, original.Name)
	}
	if decoded.Description != original.Description {
		t.Errorf("description mismatch")
	}
}

func TestMCPToolResult_Parse(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"file content here"}],"isError":false}`
	var result MCPToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result.Content) != 1 {
		t.Fatalf("expected 1 content item, got %d", len(result.Content))
	}
	if result.Content[0].Text != "file content here" {
		t.Errorf("unexpected text: %s", result.Content[0].Text)
	}
	if result.IsError {
		t.Error("expected IsError=false")
	}
}

func TestMCPToolResult_ErrorResult(t *testing.T) {
	raw := `{"content":[{"type":"text","text":"File not found"}],"isError":true}`
	var result MCPToolResult
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if !result.IsError {
		t.Error("expected IsError=true")
	}
}

// ─── MCP Tool Name Parsing Tests ─────────────────────────

func TestIsMCPTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{"mcp__filesystem__read_file", true},
		{"mcp__github__create_issue", true},
		{"search_web", false},
		{"intent", false},
		{"mcp__", true},
		{"", false},
	}

	for _, tt := range tests {
		got := IsMCPTool(tt.name)
		if got != tt.want {
			t.Errorf("IsMCPTool(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParseMCPToolName(t *testing.T) {
	tests := []struct {
		fullName  string
		wantServer string
		wantTool   string
		wantOk     bool
	}{
		{"mcp__filesystem__read_file", "filesystem", "read_file", true},
		{"mcp__github__create_issue", "github", "create_issue", true},
		{"search_web", "", "", false},
		{"mcp__incomplete", "", "", false},
		{"", "", "", false},
	}

	for _, tt := range tests {
		server, tool, ok := ParseMCPToolName(tt.fullName)
		if ok != tt.wantOk {
			t.Errorf("ParseMCPToolName(%q): ok=%v, want %v", tt.fullName, ok, tt.wantOk)
			continue
		}
		if ok {
			if server != tt.wantServer {
				t.Errorf("server: got %q, want %q", server, tt.wantServer)
			}
			if tool != tt.wantTool {
				t.Errorf("tool: got %q, want %q", tool, tt.wantTool)
			}
		}
	}
}
