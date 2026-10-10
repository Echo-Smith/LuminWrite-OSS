package engine

// 插件目录存储（runtime-agility M3）：把「加工具」变成「放一个 JSON 文件」。
//
//   - PluginLoader 是宿主注入的注册出口（engine 只管发现与构建，注册/
//     桥接到哪个 registry 由宿主决定——server 侧的双 registry 同步写在
//     bootstrap 层）。
//   - LoadToolPluginsFromDir：扫描目录内 *.json（PluginConfig 形状，与
//     admin POST 同一 schema），逐个构建并交给 loader；坏文件记日志跳过
//     （fail-open，一个坏插件不拖垮启动）。
//   - PersistToolPluginConfig / RemoveToolPluginConfig：admin API 加载/
//     卸载时同步落盘/删盘——插件从此跨重启存活。
//   - StartToolPluginDirWatcher：轮询重扫（默认 60s），新增/变更的文件
//     重新加载、删除的文件反注册——不重启完成热更新。
//
// 文件名即插件身份锚点（<slug>.json），slug 校验与包安装器同款白名单，
// 防路径穿越。

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// PluginLoader 是目录发现层的注册出口。
type PluginLoader interface {
	// LoadPlugin 注册插件（实现方决定注册到哪些 registry；同名替换）。
	LoadPlugin(plugin *ToolPlugin) error
	// UnloadPlugin 按插件名反注册（不存在时静默）。
	UnloadPlugin(name string)
}

// ValidPluginSlug 校验插件文件名 slug（路径段安全字符）。
func ValidPluginSlug(slug string) bool {
	if slug == "" || len(slug) > 64 {
		return false
	}
	for _, r := range slug {
		ok := r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z')
		if !ok {
			return false
		}
	}
	return true
}

// LoadToolPluginsFromDir 扫描目录并经 loader 注册全部合法插件。
// 返回 (加载数, 跳过数)。目录不存在时静默返回（首次部署零配置是常态）。
func LoadToolPluginsFromDir(loader PluginLoader, dir string) (loaded, skipped int) {
	names, err := listPluginFiles(dir)
	if err != nil {
		slog.Warn("tool plugin dir unreadable", "dir", dir, "error", err)
		return 0, 0
	}
	for _, name := range names {
		if _, err := loadPluginFile(loader, dir, name); err != nil {
			slog.Error("tool plugin file skipped", "file", name, "error", err)
			skipped++
			continue
		}
		loaded++
	}
	return loaded, skipped
}

func listPluginFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names, nil
}

func loadPluginFile(loader PluginLoader, dir, name string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	var cfg PluginConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	slug := strings.TrimSuffix(name, ".json")
	if !ValidPluginSlug(slug) {
		return "", fmt.Errorf("file slug %q invalid（小写字母/数字/-/_）", slug)
	}
	if cfg.Name == "" {
		cfg.Name = slug
	}
	plugin, err := BuildPluginFromConfig(cfg)
	if err != nil {
		return "", err
	}
	return plugin.Name, loader.LoadPlugin(plugin)
}

// PersistToolPluginConfig 把 admin API 加载的插件配置落盘（跨重启存活）。
// 文件名以配置 slug 锚定；名字不合法则拒绝落盘。
func PersistToolPluginConfig(dir string, cfg PluginConfig) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(cfg.Name))
	if !ValidPluginSlug(slug) {
		return "", fmt.Errorf("plugin name %q 不能作为文件 slug（小写字母/数字/-/_）", cfg.Name)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, slug+".json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// RemoveToolPluginConfig 删除插件的落盘配置（admin 卸载时同步）。
func RemoveToolPluginConfig(dir, name string) error {
	slug := strings.ToLower(strings.TrimSpace(name))
	if !ValidPluginSlug(slug) {
		return fmt.Errorf("plugin name %q 不能作为文件 slug", name)
	}
	path := filepath.Join(dir, slug+".json")
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return nil // 内存注册、无落盘形态（历史插件）：视为已删
		}
		return err
	}
	return nil
}

// StartToolPluginDirWatcher 周期重扫插件目录：新增/内容变更的文件重新
// 加载，文件消失的反注册。返回停止函数。内容以 FNV 摘要比对，未变化
// 的文件不产生加载噪音。
func StartToolPluginDirWatcher(loader PluginLoader, dir string, interval time.Duration) (stop func()) {
	// file → 登记项（注册的插件名 + 文件内容摘要）
	registeredByFile := scanAndRegister(loader, dir)
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
			}
			current := scanPluginHashes(dir)
			for name, hash := range current {
				if entry, ok := registeredByFile[name]; ok && entry.fileHash == hash {
					continue
				}
				pluginName, err := loadPluginFile(loader, dir, name)
				if err != nil {
					slog.Error("tool plugin hot-reload skipped", "file", name, "error", err)
					continue
				}
				slog.Info("tool plugin hot-reloaded", "file", name, "plugin", pluginName)
				registeredByFile[name] = pluginFileEntry{pluginName: pluginName, fileHash: hash}
			}
			for name, entry := range registeredByFile {
				if _, ok := current[name]; !ok {
					loader.UnloadPlugin(entry.pluginName)
					slog.Info("tool plugin removed by dir watch", "file", name, "plugin", entry.pluginName)
					delete(registeredByFile, name)
				}
			}
		}
	}()
	return func() { close(stopCh) }
}

type pluginFileEntry struct {
	pluginName string
	fileHash   string
}

// scanAndRegister 首轮全量加载，返回 文件 → 登记项 映射。
func scanAndRegister(loader PluginLoader, dir string) map[string]pluginFileEntry {
	registered := make(map[string]pluginFileEntry)
	hashes := scanPluginHashes(dir)
	for name := range hashes {
		pluginName, err := loadPluginFile(loader, dir, name)
		if err != nil {
			slog.Error("tool plugin file skipped", "file", name, "error", err)
			continue
		}
		registered[name] = pluginFileEntry{pluginName: pluginName, fileHash: hashes[name]}
	}
	return registered
}

func scanPluginHashes(dir string) map[string]string {
	hashes := make(map[string]string)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return hashes
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		hashes[entry.Name()] = fnv1aSum(raw)
	}
	return hashes
}

// fnv1aSum 轻量内容摘要——watcher 只需要变化检测，不需要密码学强度。
func fnv1aSum(data []byte) string {
	var h uint64 = 14695981039346656037
	for _, b := range data {
		h ^= uint64(b)
		h *= 1099511628211
	}
	return fmt.Sprintf("%016x", h)
}
