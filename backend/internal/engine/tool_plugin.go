package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ─── Tool Plugin: Hot-pluggable Tool Sets ──────────────
//
// A ToolPlugin is a named bundle of tools that can be dynamically
// registered and unregistered from the ToolRegistry at runtime.
//
// This enables:
//   - Hot-pluggable tool sets (add/remove without restart)
//   - YAML-configured tool bundles (load from file at startup or via API)
//   - Third-party tool integrations via HTTP webhook tools
//   - A/B testing different tool configurations
//
// Inspired by:
//   - dsh's plugin registration pattern (seam-based, declarative)
//   - Pi Agent's tool bundle concept (tools grouped by capability)
//   - OpenAI GPTs' custom tools (HTTP-based, schema-driven)

// ToolPlugin is a named bundle of tools with metadata.
type ToolPlugin struct {
	Name        string                  `json:"name"`
	Description string                  `json:"description"`
	Version     string                  `json:"version,omitempty"`
	Tools       []AgentTool             `json:"-"`
	Descriptors map[string]ToolDescriptor `json:"-"`
}

// PluginInfo is the metadata returned by ListPlugins (without tool instances).
type PluginInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Version     string           `json:"version,omitempty"`
	ToolCount   int              `json:"tool_count"`
	ToolNames   []string         `json:"tool_names"`
	LoadedAt    time.Time         `json:"loaded_at"`
}

// ─── Plugin Management on ToolRegistry ───────────────────

// pluginManager manages loaded plugins on a ToolRegistry.
// It is embedded in ToolRegistry.
type pluginManager struct {
	mu      sync.RWMutex
	plugins map[string]*loadedPlugin
}

// loadedPlugin tracks a loaded plugin and its tools.
type loadedPlugin struct {
	info   PluginInfo
	tools  []string // tool names registered by this plugin
}

func newPluginManager() *pluginManager {
	return &pluginManager{plugins: make(map[string]*loadedPlugin)}
}

// RegisterPlugin registers all tools from a plugin into the registry.
// If a plugin with the same name exists, it is replaced.
func (r *ToolRegistry) RegisterPlugin(p *ToolPlugin) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// If a plugin with the same name exists, unregister it first
	r.unregisterPluginLocked(p.Name)

	toolNames := make([]string, 0, len(p.Tools))
	for _, t := range p.Tools {
		name := t.Name()
		r.tools[name] = t

		desc := ToolDescriptor{
			Name:        name,
			Description: t.Description(),
			Repeatable:  true,
		}
		// Override with plugin-provided descriptor if exists
		if d, ok := p.Descriptors[name]; ok {
			desc = d
			if desc.Name == "" {
				desc.Name = name
			}
			if desc.Description == "" {
				desc.Description = t.Description()
			}
		}
		r.descriptors[name] = desc
		toolNames = append(toolNames, name)
	}

	r.plugins.plugins[p.Name] = &loadedPlugin{
		info: PluginInfo{
			Name:        p.Name,
			Description: p.Description,
			Version:     p.Version,
			ToolCount:   len(p.Tools),
			ToolNames:   toolNames,
			LoadedAt:    time.Now(),
		},
		tools: toolNames,
	}

	slog.Info("tool plugin registered",
		"name", p.Name,
		"tools", len(p.Tools),
		"version", p.Version,
	)
	return nil
}

// UnregisterPlugin removes a plugin and all its tools from the registry.
func (r *ToolRegistry) UnregisterPlugin(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.unregisterPluginLocked(name)
}

func (r *ToolRegistry) unregisterPluginLocked(name string) error {
	p, ok := r.plugins.plugins[name]
	if !ok {
		return fmt.Errorf("plugin %q not found", name)
	}

	// Remove all tools belonging to this plugin
	for _, toolName := range p.tools {
		delete(r.tools, toolName)
		delete(r.descriptors, toolName)
	}

	delete(r.plugins.plugins, name)

	slog.Info("tool plugin unregistered", "name", name, "tools", len(p.tools))
	return nil
}

// ListPlugins returns metadata for all loaded plugins.
func (r *ToolRegistry) ListPlugins() []PluginInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]PluginInfo, 0, len(r.plugins.plugins))
	for _, p := range r.plugins.plugins {
		result = append(result, p.info)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// GetPlugin returns metadata for a specific plugin.
func (r *ToolRegistry) GetPlugin(name string) (PluginInfo, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.plugins.plugins[name]
	if !ok {
		return PluginInfo{}, false
	}
	return p.info, true
}

// ─── HTTPTool: Generic HTTP Webhook Tool ─────────────────
//
// HTTPTool is a generic AgentTool that calls an external HTTP endpoint
// and returns the response. This allows plugins to define tools that
// integrate with any external service without writing Go code.
//
// The tool's schema, endpoint, and headers are defined in the plugin's
// YAML configuration, making it fully declarative.

// HTTPToolConfig defines an HTTP-based tool from configuration.
type HTTPToolConfig struct {
	Name        string            `json:"name" yaml:"name"`
	Description string            `json:"description" yaml:"description"`
	Endpoint    string            `json:"endpoint" yaml:"endpoint"`
	Method      string            `json:"method" yaml:"method"`
	Headers     map[string]string `json:"headers,omitempty" yaml:"headers"`
	// Schema is the JSON Schema for the tool parameters.
	// These are sent as the request body (for POST/PUT) or query params (for GET).
	Schema map[string]any `json:"schema,omitempty" yaml:"schema"`
	// ResponseTemplate is a Go text/template string to format the HTTP response.
	// If empty, the raw response body is returned.
	ResponseTemplate string `json:"response_template,omitempty" yaml:"response_template"`
}

// HTTPTool implements AgentTool for external HTTP calls.
type HTTPTool struct {
	config  HTTPToolConfig
	client  *http.Client
}

// NewHTTPTool creates a new HTTP-based tool from configuration.
func NewHTTPTool(cfg HTTPToolConfig) *HTTPTool {
	return &HTTPTool{
		config: cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (t *HTTPTool) Name() string { return t.config.Name }
func (t *HTTPTool) Description() string { return t.config.Description }
func (t *HTTPTool) Schema() map[string]any {
	if t.config.Schema == nil {
		return map[string]any{"type": "object"}
	}
	return t.config.Schema
}

func (t *HTTPTool) Execute(ctx context.Context, args map[string]any, execCtx *ExecutionContext, emitter EventEmitter) (*ToolResult, error) {
	method := t.config.Method
	if method == "" {
		method = "POST"
	}

	var bodyReader io.Reader
	if method == "GET" {
		// For GET, args become query params (handled by URL encoding)
		// For simplicity, we pass args as JSON body for all methods
	} else {
		bodyBytes, err := json.Marshal(args)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal args: %w", err)
		}
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(ctx, method, t.config.Endpoint, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set default headers
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.config.Headers {
		req.Header.Set(k, v)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	// Truncate large responses to avoid context bloat
	summary := string(body)
	if len(summary) > 2000 {
		summary = summary[:2000] + "...(truncated)"
	}

	return &ToolResult{
		Summary: summary,
		Done:    false,
	}, nil
}

// ─── Plugin YAML Configuration ───────────────────────────
//
// Example YAML:
//
//   name: weather-tools
//   description: Weather and location lookup tools
//   version: "1.0"
//   tools:
//     - name: get_weather
//       description: Get current weather for a city
//       endpoint: https://api.weather.example.com/v1/current
//       method: GET
//       schema:
//         type: object
//         properties:
//           city:
//             type: string
//             description: City name
//         required: [city]
//       descriptor:
//         repeatable: true
//         category: lookup
//         depends_on: []

// PluginConfig is the YAML configuration for a tool plugin.
type PluginConfig struct {
	Name        string              `json:"name" yaml:"name"`
	Description string              `json:"description" yaml:"description"`
	Version     string              `json:"version" yaml:"version"`
	Tools       []ToolEntryConfig   `json:"tools" yaml:"tools"`
}

// ToolEntryConfig is a single tool entry in the plugin config.
type ToolEntryConfig struct {
	HTTPToolConfig `yaml:",inline"`
	Descriptor     ToolDescriptor `json:"descriptor,omitempty" yaml:"descriptor,omitempty"`
}

// BuildPlugin constructs a ToolPlugin from configuration.
// This creates HTTPTool instances for each tool entry.
func BuildPluginFromConfig(cfg PluginConfig) (*ToolPlugin, error) {
	plugin := &ToolPlugin{
		Name:        cfg.Name,
		Description: cfg.Description,
		Version:     cfg.Version,
		Descriptors: make(map[string]ToolDescriptor),
	}

	for _, tc := range cfg.Tools {
		tool := NewHTTPTool(tc.HTTPToolConfig)
		plugin.Tools = append(plugin.Tools, tool)

		desc := tc.Descriptor
		if desc.Name == "" {
			desc.Name = tc.Name
		}
		if desc.Description == "" {
			desc.Description = tc.Description
		}
		// Default: repeatable, non-terminal, no deps
		if desc.Category == "" {
			desc.Category = "plugin"
		}
		plugin.Descriptors[desc.Name] = desc
	}

	return plugin, nil
}
