package server

// 工具插件引导（runtime-agility M3）：把热加载插件的注册面收敛到
// pluginDirLoader —— 同一次加载双写两个 registry：
//
//   - engine.ToolRegistry：admin tool-plugins 面的管理权威（列举/详情）；
//   - editorial.EditorialToolRegistry：写作 agent 循环的消费面（role
//     executor 的工具表/system prompt 指引按注册遍历，桥接适配见
//     editorial/plugin_bridge.go）。
//
// 在此之前热加载插件只进了 engine registry——对写作链路是死路。落地后
// 「加工具 = 往 TOOL_PLUGINS_DIR 放一个 JSON 文件（或 admin API 一次
// 调用）」，role agent 下一轮运行即可见，免重编译。

import (
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/editorial"
	"github.com/luminbuddy/luminbuddy-writing-agent-v2/internal/engine"
)

const toolPluginsDirEnv = "TOOL_PLUGINS_DIR"

const toolPluginsDirDefault = "data/tool-plugins"

const toolPluginsWatchInterval = time.Minute

// pluginDirLoader 双写加载器：engine registry（管理权威）+ editorial
// registry（写作消费面）。editorial 可为 nil（DB 不可用的降级启动），
// 此时只写 engine registry——与旧行为兼容。
type pluginDirLoader struct {
	mu        sync.Mutex
	registry  *engine.ToolRegistry
	editorial *editorial.EditorialToolRegistry
	dir       string
}

// LoadPlugin 注册到双 registry。任一失败即整体失败（engine 失败回滚
// editorial 侧，保持两边一致）。
func (l *pluginDirLoader) LoadPlugin(plugin *engine.ToolPlugin) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.registry.RegisterPlugin(plugin); err != nil {
		return err
	}
	if l.editorial != nil {
		editorial.RegisterPluginTools(l.editorial, plugin)
	}
	return nil
}

// UnloadPlugin 按插件名双侧反注册（editorial 侧按登记的工具名移除；
// 不存在时静默——与 engine.UnregisterPlugin 同语义）。
func (l *pluginDirLoader) UnloadPlugin(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if info, ok := l.registry.GetPlugin(name); ok && l.editorial != nil {
		for _, toolName := range info.ToolNames {
			l.editorial.Remove(toolName)
		}
	}
	_ = l.registry.UnregisterPlugin(name)
}

// persist 把插件配置落盘（admin API 加载的插件跨重启存活）。
func (l *pluginDirLoader) persist(cfg engine.PluginConfig) (string, error) {
	return engine.PersistToolPluginConfig(l.dir, cfg)
}

// removePersisted 删除插件的落盘配置。
func (l *pluginDirLoader) removePersisted(name string) error {
	return engine.RemoveToolPluginConfig(l.dir, name)
}

// initToolPluginDir 冷启动扫描 + 启动目录 watcher。editorial registry
// 可为 nil。返回加载器（供 admin handler 双写持久化复用）。
func initToolPluginDir(registry *engine.ToolRegistry, editorialRegistry *editorial.EditorialToolRegistry) *pluginDirLoader {
	dir := os.Getenv(toolPluginsDirEnv)
	if dir == "" {
		dir = toolPluginsDirDefault
	}
	loader := &pluginDirLoader{registry: registry, editorial: editorialRegistry, dir: dir}

	loaded, skipped := engine.LoadToolPluginsFromDir(loader, dir)
	slog.Info("tool plugin dir scanned", "dir", dir, "loaded", loaded, "skipped", skipped)

	// 目录 watcher：放/改/删 JSON 文件即热更新，无需重启。进程退出时
	// 随之回收（tick goroutine 无优雅停机接线，v1 接受）。
	_ = engine.StartToolPluginDirWatcher(loader, dir, toolPluginsWatchInterval)
	return loader
}
