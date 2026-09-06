package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveRootUsesEnv(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvRoot, root)
	got, err := ResolveRoot()
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(root)
	if got != want {
		t.Fatalf("ResolveRoot = %q, want %q", got, want)
	}
}

func TestIsGoBuildDir(t *testing.T) {
	if !isGoBuildDir(filepath.Join(os.TempDir(), "go-build123", "b001", "exe")) {
		t.Fatal("go-build temp dir should fall back to cwd")
	}
	if isGoBuildDir(filepath.Join(t.TempDir(), "ainovel")) {
		t.Fatal("normal install dir should not look like go-build")
	}
}

func TestValidateName(t *testing.T) {
	cases := []struct {
		name string
		ok   bool
	}{
		{"边城", true},
		{"悬疑短篇", true},
		{"", false},
		{"..", false},
		{`a\b`, false},
		{"foo/bar", false},
		{"CON", false},
		{"output", true},
		{"bad:name", false},
		{"ends.", false},
	}
	for _, tc := range cases {
		err := ValidateName(tc.name)
		if tc.ok && err != nil {
			t.Fatalf("ValidateName(%q) = %v, want nil", tc.name, err)
		}
		if !tc.ok && !errors.Is(err, ErrInvalidName) {
			t.Fatalf("ValidateName(%q) = %v, want ErrInvalidName", tc.name, err)
		}
	}
}

func TestScanEmptyRoot(t *testing.T) {
	root := t.TempDir()
	items, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("empty root items = %#v", items)
	}
}

func TestScanNamedWorkspaces(t *testing.T) {
	root := t.TempDir()
	emptyName := "空书"
	if err := os.MkdirAll(filepath.Join(root, DirName, emptyName), 0o755); err != nil {
		t.Fatal(err)
	}
	named := filepath.Join(root, DirName, "边城")
	if err := os.MkdirAll(filepath.Join(named, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	progress := `{"novel_name":"边城往事","phase":"writing","completed_chapters":[1,2]}`
	if err := os.WriteFile(filepath.Join(named, "meta", "progress.json"), []byte(progress), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "output", "novel", "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "output", "novel", "meta", "progress.json"), []byte(`{"novel_name":"旧书"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, DirName, "noise"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DirName, "noise", "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	items, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %#v", items)
	}
	byName := map[string]Info{}
	for _, item := range items {
		byName[item.Name] = item
	}
	if byName["边城"].NovelName != "边城往事" || byName["边城"].Completed != 2 || byName["边城"].Phase != "writing" {
		t.Fatalf("named workspace = %#v", byName["边城"])
	}
	if byName[emptyName].Path == "" {
		t.Fatalf("empty workspace = %#v", byName[emptyName])
	}
	if _, ok := byName["output"]; ok {
		t.Fatal("old output/novel should not appear as a workspace")
	}
}

func TestScanRecognizesBookConfigDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, DirName, "只配了模型")
	if err := os.MkdirAll(filepath.Join(dir, ".ainovel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ainovel", "config.json"), []byte(`{"style":"fantasy"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := Lookup(root, "只配了模型")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "只配了模型" {
		t.Fatalf("lookup = %#v", got)
	}
}

func TestCreateLookupAndLastWorkspace(t *testing.T) {
	root := t.TempDir()
	info, err := Create(root, "悬疑短篇")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "悬疑短篇" {
		t.Fatalf("create = %#v", info)
	}
	if _, err := os.Stat(info.Path); err != nil {
		t.Fatal(err)
	}
	got, err := Lookup(root, "悬疑短篇")
	if err != nil {
		t.Fatal(err)
	}
	if got.Path != info.Path {
		t.Fatalf("lookup path = %q, want %q", got.Path, info.Path)
	}
	if _, err := Create(root, "悬疑短篇"); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate create = %v", err)
	}
	if err := SaveLast(root, "悬疑短篇"); err != nil {
		t.Fatal(err)
	}
	if LoadLast(root) != "悬疑短篇" {
		t.Fatalf("last = %q", LoadLast(root))
	}
	if _, err := Lookup(root, "没有"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing lookup = %v", err)
	}
}

func TestWindowsReservedName(t *testing.T) {
	if runtime.GOOS != "windows" && ValidateName("aux") != nil {
		// AUX is reserved on all platforms for portable folder names.
	}
	if err := ValidateName("aux"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("aux should be rejected, got %v", err)
	}
}
