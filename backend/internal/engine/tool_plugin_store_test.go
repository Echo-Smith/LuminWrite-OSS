package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 工具插件目录存储单测（runtime-agility M3）。
// endpoint 用 IP 字面量（公网/保留段），避免测试依赖 DNS。

func validPluginConfig(name string) PluginConfig {
	return PluginConfig{
		Name:        name,
		Description: "测试插件",
		Version:     "1.0",
		Tools: []ToolEntryConfig{
			{
				HTTPToolConfig: HTTPToolConfig{
					Name:        name + "_ping",
					Description: "Ping 测试端点",
					Endpoint:    "https://93.184.216.34/v1/ping",
					Method:      "POST",
					Schema:      map[string]any{"type": "object"},
				},
				Roles: []string{"writer"},
			},
		},
	}
}

// 内存注册器：记录 Load/Unload 调用（loader 接口的测试替身）。
type memLoader struct {
	loaded   map[string]*ToolPlugin
	unloaded []string
}

func newMemLoader() *memLoader { return &memLoader{loaded: map[string]*ToolPlugin{}} }

func (m *memLoader) LoadPlugin(p *ToolPlugin) error {
	m.loaded[p.Name] = p
	return nil
}
func (m *memLoader) UnloadPlugin(name string) {
	delete(m.loaded, name)
	m.unloaded = append(m.unloaded, name)
}

func TestLoadToolPluginsFromDir(t *testing.T) {
	dir := t.TempDir()
	loader := newMemLoader()

	// 合法文件 + 非法 JSON + 私网 endpoint + 非 .json 文件，各一
	writeFile(t, dir, "good-plugin.json", mustJSON(validPluginConfig("good-plugin")))
	writeFile(t, dir, "broken.json", []byte("{not json"))
	bad := validPluginConfig("bad-ssrf")
	bad.Tools[0].Endpoint = "http://127.0.0.1:8500/v1"
	writeFile(t, dir, "bad-ssrf.json", mustJSON(bad))
	writeFile(t, dir, "notes.txt", []byte("ignored"))

	loaded, skipped := LoadToolPluginsFromDir(loader, dir)
	if loaded != 1 || skipped != 2 {
		t.Fatalf("loaded=%d skipped=%d, want 1/2（坏 JSON 与 SSRF 拒绝，.txt 忽略）", loaded, skipped)
	}
	p, ok := loader.loaded["good-plugin"]
	if !ok {
		t.Fatal("good-plugin 未加载")
	}
	// cfg.Name 为空时回落文件 slug；此处显式命名不受 slug 影响
	if p.Tools[0].Name() != "good-plugin_ping" {
		t.Fatalf("工具名 = %q", p.Tools[0].Name())
	}
}

func TestLoadToolPluginsSlugAnchorsName(t *testing.T) {
	dir := t.TempDir()
	loader := newMemLoader()
	cfg := validPluginConfig("")
	writeFile(t, dir, "weather-kit.json", mustJSON(cfg))

	if _, skipped := LoadToolPluginsFromDir(loader, dir); skipped != 0 {
		t.Fatal("合法文件不应被跳过")
	}
	if _, ok := loader.loaded["weather-kit"]; !ok {
		t.Fatalf("cfg.Name 为空时应取文件 slug 作为插件名: %v", loader.loaded)
	}
}

func TestPersistAndRemoveConfigRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := validPluginConfig("round-trip")

	path, err := PersistToolPluginConfig(dir, cfg)
	if err != nil {
		t.Fatalf("persist: %v", err)
	}
	if filepath.Base(path) != "round-trip.json" {
		t.Fatalf("落盘文件名 = %q", filepath.Base(path))
	}

	// 落盘内容可被目录加载读回
	loader := newMemLoader()
	loaded, _ := LoadToolPluginsFromDir(loader, dir)
	if loaded != 1 {
		t.Fatalf("落盘配置应可读回加载: loaded=%d", loaded)
	}

	// 非法 slug 拒绝落盘/删除（路径穿越红线）
	if _, err := PersistToolPluginConfig(dir, validPluginConfig("../evil")); err == nil {
		t.Fatal("路径穿越 slug 应被拒绝")
	}
	if err := RemoveToolPluginConfig(dir, "../../etc/passwd"); err == nil {
		t.Fatal("路径穿越删除应被拒绝")
	}

	if err := RemoveToolPluginConfig(dir, "round-trip"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	// 幂等：再删不存在的不报错
	if err := RemoveToolPluginConfig(dir, "round-trip"); err != nil {
		t.Fatalf("remove 幂等: %v", err)
	}
}

func TestDirWatcherHotReload(t *testing.T) {
	dir := t.TempDir()
	loader := newMemLoader()

	stop := StartToolPluginDirWatcher(loader, dir, 20*time.Millisecond)
	defer stop()

	// 等首轮扫描完成
	time.Sleep(60 * time.Millisecond)
	if len(loader.loaded) != 0 {
		t.Fatalf("空目录不应有加载: %v", loader.loaded)
	}

	// 放入文件 → 热加载
	writeFile(t, dir, "hot-add.json", mustJSON(validPluginConfig("hot-add")))
	waitFor(t, 2*time.Second, func() bool { return len(loader.loaded) == 1 })

	// 修改内容 → 重新加载（同名替换）
	updated := validPluginConfig("hot-add")
	updated.Description = "更新后的描述"
	writeFile(t, dir, "hot-add.json", mustJSON(updated))
	waitFor(t, 2*time.Second, func() bool {
		return loader.loaded["hot-add"] != nil && loader.loaded["hot-add"].Description == "更新后的描述"
	})

	// 删除文件 → 反注册
	if err := os.Remove(filepath.Join(dir, "hot-add.json")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { return len(loader.loaded) == 0 })
}

// ─── SSRF 红线 ──────────────────────────────────────────────────────────────

func TestBuildPluginRejectsPrivateEndpoints(t *testing.T) {
	cases := map[string]string{
		"loopback":     "http://127.0.0.1:8500/v1",
		"private-v4":   "http://192.168.1.10/api",
		"metadata-v4":  "http://169.254.169.254/latest/meta-data/",
		"loopback-v6":  "http://[::1]:9000/x",
		"cgnat":        "http://100.64.0.1/x",
		"scheme-echo":  "file:///etc/passwd",
		"scheme-https2": "ftp://93.184.216.34/x",
	}
	for name, endpoint := range cases {
		cfg := validPluginConfig("ssrf-" + name)
		cfg.Tools[0].Endpoint = endpoint
		if _, err := BuildPluginFromConfig(cfg); err == nil {
			t.Fatalf("%s endpoint %q 应被拒绝", name, endpoint)
		}
	}
}

func TestBuildPluginAcceptsPublicIPLiteral(t *testing.T) {
	cfg := validPluginConfig("public")
	if _, err := BuildPluginFromConfig(cfg); err != nil {
		t.Fatalf("公网 IP 字面量 endpoint 不应被拒绝: %v", err)
	}
}

func TestBuildPluginDuplicateToolNamesRejected(t *testing.T) {
	cfg := validPluginConfig("dup")
	cfg.Tools = append(cfg.Tools, cfg.Tools[0])
	if _, err := BuildPluginFromConfig(cfg); err == nil {
		t.Fatal("重复工具名应被拒绝")
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func writeFile(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(v any) []byte {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
