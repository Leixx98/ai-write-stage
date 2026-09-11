package workspace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

const (
	DirName      = "workspaces"
	LastFileName = ".ainovel-last-workspace"
	EnvRoot      = "AINOVEL_ROOT"
)

var (
	ErrInvalidName = errors.New("invalid workspace name")
	ErrExists      = errors.New("workspace exists")
	ErrNotFound    = errors.New("workspace not found")
)

type Info struct {
	Name      string `json:"name"`
	Display   string `json:"display"`
	Path      string `json:"path"`
	NovelName string `json:"novel_name"`
	Phase     string `json:"phase"`
	Completed int    `json:"completed"`
}

var winReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func ResolveRoot() (string, error) {
	if env := strings.TrimSpace(os.Getenv(EnvRoot)); env != "" {
		return filepath.Abs(env)
	}
	exe, err := os.Executable()
	if err != nil {
		return os.Getwd()
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		resolved = exe
	}
	dir := filepath.Dir(resolved)
	if isGoBuildDir(dir) {
		return os.Getwd()
	}
	return dir, nil
}

func isGoBuildDir(dir string) bool {
	return strings.Contains(filepath.ToSlash(dir), "/go-build")
}

func WorkspacesDir(root string) string {
	return filepath.Join(root, DirName)
}

func ValidateName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("工作区名不能为空: %w", ErrInvalidName)
	}
	if name == "." || name == ".." {
		return fmt.Errorf("工作区名不合法: %w", ErrInvalidName)
	}
	if name != filepath.Base(name) {
		return fmt.Errorf("工作区名不能包含路径: %w", ErrInvalidName)
	}
	if strings.ContainsAny(name, `<>:"/\|?*`) {
		return fmt.Errorf("工作区名包含非法字符: %w", ErrInvalidName)
	}
	for _, r := range name {
		if r < 32 || r == 127 || unicode.IsControl(r) {
			return fmt.Errorf("工作区名包含非法字符: %w", ErrInvalidName)
		}
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return fmt.Errorf("工作区名不能以空格或句点结尾: %w", ErrInvalidName)
	}
	base := strings.ToUpper(name)
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if winReserved[base] {
		return fmt.Errorf("工作区名不能使用系统保留名: %w", ErrInvalidName)
	}
	if len([]rune(name)) > 100 {
		return fmt.Errorf("工作区名过长: %w", ErrInvalidName)
	}
	return nil
}

func NamesEqual(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func Scan(root string) ([]Info, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("software root is required")
	}
	var items []Info
	wsDir := WorkspacesDir(root)
	entries, err := os.ReadDir(wsDir)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		dir := filepath.Join(wsDir, name)
		if !recognized(dir) {
			continue
		}
		items = append(items, inspect(name, dir))
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func Create(root, name string) (Info, error) {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return Info{}, err
	}
	if _, err := Lookup(root, name); err == nil {
		return Info{}, fmt.Errorf("工作区 %q 已存在: %w", name, ErrExists)
	}
	dir := filepath.Join(WorkspacesDir(root), name)
	if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
		return Info{}, fmt.Errorf("工作区 %q 已存在: %w", name, ErrExists)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Info{}, err
	}
	return inspect(name, dir), nil
}

func Lookup(root, name string) (Info, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Info{}, fmt.Errorf("工作区名不能为空: %w", ErrInvalidName)
	}
	items, err := Scan(root)
	if err != nil {
		return Info{}, err
	}
	for _, item := range items {
		if NamesEqual(item.Name, name) {
			return item, nil
		}
	}
	return Info{}, fmt.Errorf("工作区 %q 不存在: %w", name, ErrNotFound)
}

func LoadLast(root string) string {
	data, err := os.ReadFile(lastPath(root))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func SaveLast(root, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("工作区名不能为空: %w", ErrInvalidName)
	}
	return os.WriteFile(lastPath(root), []byte(name+"\n"), 0o600)
}

func lastPath(root string) string {
	return filepath.Join(root, LastFileName)
}

func recognized(dir string) bool {
	if hasProgress(dir) {
		return true
	}
	if _, err := os.Stat(filepath.Join(store.NovelDir(dir), "meta")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".ainovel")); err == nil {
		return true
	}
	return isEmptyDir(dir)
}

func hasProgress(dir string) bool {
	_, err := os.Stat(filepath.Join(store.NovelDir(dir), "meta", "progress.json"))
	return err == nil
}

func inspect(name, dir string) Info {
	abs, err := filepath.Abs(dir)
	if err != nil {
		abs = filepath.Clean(dir)
	}
	info := Info{Name: name, Display: name, Path: abs}
	if novelName, phase, completed, ok := readProgress(abs); ok {
		info.NovelName = novelName
		info.Phase = phase
		info.Completed = completed
	}
	return info
}

func readProgress(dir string) (novelName, phase string, completed int, ok bool) {
	data, err := os.ReadFile(filepath.Join(store.NovelDir(dir), "meta", "progress.json"))
	if err != nil {
		return "", "", 0, false
	}
	var progress struct {
		NovelName         string `json:"novel_name"`
		Phase             string `json:"phase"`
		CompletedChapters []int  `json:"completed_chapters"`
	}
	if json.Unmarshal(data, &progress) != nil {
		return "", "", 0, false
	}
	return strings.TrimSpace(progress.NovelName), strings.TrimSpace(progress.Phase), len(progress.CompletedChapters), true
}

func isEmptyDir(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	names, err := f.Readdirnames(1)
	if err == io.EOF {
		return true
	}
	return err == nil && len(names) == 0
}
