package mcp

import (
	"context"
	"testing"
)

func TestRegistrySnapshotsZeroAndFailedConfiguration(t *testing.T) {
	registry := NewRegistry()
	if statuses := registry.Statuses(); len(statuses) != 0 {
		t.Fatalf("new registry statuses=%#v", statuses)
	}

	err := registry.Connect(context.Background(), MCPClientConfig{Name: "broken", Transport: "invalid"})
	if err == nil {
		t.Fatal("invalid transport connected")
	}
	statuses := registry.Statuses()
	if len(statuses) != 1 {
		t.Fatalf("statuses=%#v", statuses)
	}
	status := statuses[0]
	if status.Name != "broken" || status.Transport != "invalid" || status.Connected ||
		status.ToolCount != 0 || status.ErrorCode != MCPErrorConfigInvalid || status.LastChecked.IsZero() {
		t.Fatalf("status=%#v", status)
	}
	if len(registry.ServerNames()) != 0 {
		t.Fatalf("failed server advertised as connected: %#v", registry.ServerNames())
	}
}

func TestRegistryStatusTracksConnectedToolsAndDisconnect(t *testing.T) {
	registry := NewRegistry()
	client := &MCPClient{name: "local", transport: "stdio", tools: []MCPToolDef{{Name: "search"}, {Name: "fetch"}}}
	registry.recordConnected(MCPClientConfig{Name: "local", Transport: "stdio"}, client)

	status := registry.Statuses()[0]
	if !status.Connected || status.ToolCount != 2 || status.ErrorCode != "" {
		t.Fatalf("connected status=%#v", status)
	}
	if err := registry.Disconnect("local"); err != nil {
		t.Fatal(err)
	}
	status = registry.Statuses()[0]
	if status.Connected || status.ErrorCode != MCPErrorDisconnected {
		t.Fatalf("disconnected status=%#v", status)
	}
}

func TestMCPAgentToolExecuteAcceptsNilExecutionContext(t *testing.T) {
	tool := &MCPAgentTool{
		name: "mcp__fake__search", toolName: "search",
		client: &MCPClient{name: "fake", transport: "invalid", pending: make(map[int64]chan *jsonrpcResponse)},
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"query": "safe"}, nil, nil); err == nil {
		t.Fatal("expected fake transport error")
	}
}
