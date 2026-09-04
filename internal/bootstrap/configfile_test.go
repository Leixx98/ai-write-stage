package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func setupLibrary(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	library := ModelLibrary{
		Version:       1,
		ProviderOrder: []string{"preferred", "other"},
		Providers: map[string]ProviderConfig{
			"preferred": {Type: "openai", APIKey: "secret", Models: []ModelConfig{{Name: "first-model"}, {Name: "second-model"}}},
			"other":     {Type: "openai", APIKey: "secret-2", Models: []ModelConfig{{Name: "other-model"}}},
		},
	}
	if err := SaveModelLibrary(library); err != nil {
		t.Fatalf("save model library: %v", err)
	}
	return home
}

func TestLoadConfigDoesNotCreateCwdDotDir(t *testing.T) {
	setupLibrary(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Provider != "preferred" || cfg.ModelName != "first-model" {
		t.Fatalf("selection = %s/%s", cfg.Provider, cfg.ModelName)
	}
	if len(cfg.Providers) != 2 || cfg.Providers["preferred"].APIKey != "secret" {
		t.Fatalf("runtime providers not populated from library: %#v", cfg.Providers)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ainovel")); !os.IsNotExist(err) {
		t.Fatalf("LoadConfig should not create cwd .ainovel, err=%v", err)
	}
}

func TestApplyWorkspaceDirCreatesBookConfig(t *testing.T) {
	setupLibrary(t)
	cwd := t.TempDir()
	t.Chdir(cwd)
	book := t.TempDir()
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = ApplyWorkspaceDir(cfg, book)
	if err != nil {
		t.Fatal(err)
	}
	abs, err := filepath.Abs(book)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OutputDir != abs || cfg.ProjectDir != abs {
		t.Fatalf("dirs = %q / %q, want %q", cfg.OutputDir, cfg.ProjectDir, abs)
	}
	stored, err := LoadConfigFile(WorkspaceConfigPath(book))
	if err != nil {
		t.Fatalf("load stored workspace config: %v", err)
	}
	if len(stored.Providers) != 0 {
		t.Fatalf("workspace config leaked shared providers: %#v", stored.Providers)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".ainovel")); !os.IsNotExist(err) {
		t.Fatal("book config should not be written to cwd")
	}
}

func TestApplyWorkspaceDirUsesSavedSelection(t *testing.T) {
	setupLibrary(t)
	book := t.TempDir()
	if err := SaveWorkspaceConfig(WorkspaceConfigPath(book), Config{Provider: "other", ModelName: "other-model", Style: "default"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err = ApplyWorkspaceDir(cfg, book)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "other" || cfg.ModelName != "other-model" {
		t.Fatalf("workspace selection = %s/%s", cfg.Provider, cfg.ModelName)
	}
}

func TestApplyWorkspaceDirRejectsProviders(t *testing.T) {
	setupLibrary(t)
	book := t.TempDir()
	legacy := Config{
		Provider: "preferred", ModelName: "first-model",
		Providers: map[string]ProviderConfig{"preferred": {APIKey: "duplicate"}},
	}
	if err := SaveConfig(WorkspaceConfigPath(book), legacy); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyWorkspaceDir(cfg, book); err == nil {
		t.Fatal("workspace providers should be rejected")
	}
}

func TestLoadConfigRequiresModelLibrary(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())
	if _, err := LoadConfig(); err == nil {
		t.Fatal("missing model library should fail")
	}
}

func TestWorkspaceConfigPathUsesBookDir(t *testing.T) {
	book := t.TempDir()
	want := filepath.Join(book, ".ainovel", "config.json")
	if got := WorkspaceConfigPath(book); got != want {
		t.Fatalf("workspace config path = %q, want %q", got, want)
	}
}

func TestCorruptWorkspaceConfigFailsLoud(t *testing.T) {
	setupLibrary(t)
	book := t.TempDir()
	if err := os.MkdirAll(filepath.Join(book, ".ainovel"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(book, ".ainovel", "config.json"), []byte(`{"model":,}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyWorkspaceDir(cfg, book); err == nil {
		t.Fatal("corrupt workspace config should fail")
	}
}

func TestExampleConfigIsValidJSONC(t *testing.T) {
	rootExample, err := os.ReadFile(filepath.Join("..", "..", "config.example.jsonc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(rootExample) != exampleConfig {
		t.Fatal("embedded and root examples differ")
	}
	var cfg Config
	if err := json.Unmarshal(stripJSONComments(rootExample), &cfg); err != nil {
		t.Fatalf("example is invalid: %v", err)
	}
}

func TestWriteStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path := WriteStartupError("boom")
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		t.Fatalf("startup error was not written: %v", err)
	}
}
