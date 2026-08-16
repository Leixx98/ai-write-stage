package bootstrap

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const configDirName = ".ainovel"

// DefaultConfigDir 返回 ~/.ainovel 目录路径；取不到家目录时返回空字符串。
// 仅用于读/写不强制存在的文件（如模型缓存），不会自动创建目录。
func DefaultConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDirName)
}

// configDir 返回 ~/.ainovel 目录路径，不存在时创建。
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home dir: %w", err)
	}
	dir := filepath.Join(home, configDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create config dir: %w", err)
	}
	return dir, nil
}

// ProjectConfigPath returns the only effective workspace config path.
func ProjectConfigPath() string {
	path := filepath.Join(configDirName, "config.json")
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	return abs
}

// EffectiveConfigPath is kept as the Host-facing name. It always points to
// the workspace; runtime choices are never written to the shared library.
func EffectiveConfigPath() string {
	return ProjectConfigPath()
}

// LoadConfig loads the shared model library and the workspace's choices. A
// new workspace gets a minimal config selecting the first library model.
func LoadConfig() (Config, error) {
	library, err := LoadModelLibrary()
	if err != nil {
		return Config{}, fmt.Errorf("load shared model library: %w", err)
	}
	path := ProjectConfigPath()
	project, found, err := loadOptionalJSON(path)
	if err != nil {
		return Config{}, fmt.Errorf("load workspace config %s: %w", path, err)
	}
	if !found {
		provider, model, firstErr := library.FirstModel()
		if firstErr != nil {
			return Config{}, firstErr
		}
		project = Config{
			Provider: provider, ModelName: model.Name,
			Roles: map[string]RoleConfig{}, Style: "default",
		}
		if err := SaveWorkspaceConfig(path, project); err != nil {
			return Config{}, fmt.Errorf("create workspace config: %w", err)
		}
	}
	if len(project.Providers) > 0 {
		return Config{}, fmt.Errorf("workspace config must not contain providers; manage them in %s", DefaultModelLibraryPath())
	}
	project.Providers = cloneProviders(library.Providers)
	return project, nil
}

// loadOptionalJSON 读取一个可选的配置文件：
//   - 文件不存在 → (zero, false, nil)，由调用方决定用默认/上层值
//   - 文件存在但解析失败 → 返回错误（不再静默吞掉——否则用户的配置"配了不生效"
//     却无从排查，正是 issue #37 的根因）
func loadOptionalJSON(path string) (Config, bool, error) {
	cfg, err := loadJSONFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Config{}, false, nil
		}
		return Config{}, false, err
	}
	return cfg, true, nil
}

// LoadConfigFile 读取单个 JSON 配置文件，支持 // 行注释。
// 不做任何合并，仅返回该文件自身的配置。文件不存在时返回错误。
func LoadConfigFile(path string) (Config, error) {
	return loadJSONFile(path)
}

// loadJSONFile 读取 JSON 配置文件，支持 // 行注释。
// 文件不存在时返回错误（由调用方决定是否忽略）。
func loadJSONFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	cleaned := stripJSONComments(data)
	var cfg Config
	if err := json.Unmarshal(cleaned, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	return cfg, nil
}

func cloneMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	c := make(map[string]any, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func cloneProviders(providers map[string]ProviderConfig) map[string]ProviderConfig {
	cloned := make(map[string]ProviderConfig, len(providers))
	for name, provider := range providers {
		provider.Models = append([]ModelConfig(nil), provider.Models...)
		provider.Extra = cloneMap(provider.Extra)
		provider.ExtraBody = cloneMap(provider.ExtraBody)
		cloned[name] = provider
	}
	return cloned
}

// CloneConfig 深拷贝配置中会在运行时修改的 map/slice，避免候选配置污染当前配置。
func CloneConfig(cfg Config) Config {
	clone := cfg
	clone.Providers = make(map[string]ProviderConfig, len(cfg.Providers))
	for name, pc := range cfg.Providers {
		pc.Models = append([]ModelConfig(nil), pc.Models...)
		pc.Extra = cloneMap(pc.Extra)
		pc.ExtraBody = cloneMap(pc.ExtraBody)
		clone.Providers[name] = pc
	}
	clone.Roles = make(map[string]RoleConfig, len(cfg.Roles))
	for role, rc := range cfg.Roles {
		rc.Fallbacks = append([]ModelRef(nil), rc.Fallbacks...)
		clone.Roles[role] = rc
	}
	clone.Notify.Events = append([]string(nil), cfg.Notify.Events...)
	return clone
}

// stripJSONComments 去除 JSON 中的 // 行注释，跟踪引号状态避免误删字符串内容。
func stripJSONComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false

	for i := 0; i < len(data); i++ {
		b := data[i]

		if escaped {
			out = append(out, b)
			escaped = false
			continue
		}

		if inString {
			out = append(out, b)
			if b == '\\' {
				escaped = true
			} else if b == '"' {
				inString = false
			}
			continue
		}

		// 不在字符串内
		if b == '"' {
			inString = true
			out = append(out, b)
			continue
		}

		// 检测 // 注释
		if b == '/' && i+1 < len(data) && data[i+1] == '/' {
			// 跳到行尾
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i < len(data) {
				out = append(out, '\n')
			}
			continue
		}

		out = append(out, b)
	}

	return out
}

// WriteStartupError 把启动期致命错误追加写入 ~/.ainovel/last-error.log，并返回
// 该文件路径（best-effort，失败时返回空字符串）。双击启动时控制台窗口会随进程
// 退出立即关闭、错误一闪而过，落盘是这类用户事后追溯的唯一途径。
func WriteStartupError(msg string) string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(dir, "last-error.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return ""
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "[%s] %s\n", time.Now().Format(time.RFC3339), msg); err != nil {
		return ""
	}
	return path
}

// SaveConfig 将配置写入指定路径（JSON 格式，缩进美化）。
func SaveConfig(path string, cfg Config) error {
	return saveJSON(path, cfg)
}

// SaveWorkspaceConfig persists only workspace choices. Provider credentials
// and the model catalog always remain in the shared models.json.
func SaveWorkspaceConfig(path string, cfg Config) error {
	cfg.Providers = nil
	return SaveConfig(path, cfg)
}
