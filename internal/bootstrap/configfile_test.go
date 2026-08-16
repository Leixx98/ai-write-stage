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

func TestLoadConfigCreatesWorkspaceFromFirstLibraryModel(t *testing.T) {
	setupLibrary(t)
	workspace := t.TempDir()
	t.Chdir(workspace)

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

	stored, err := LoadConfigFile(filepath.Join(workspace, ".ainovel", "config.json"))
	if err != nil {
		t.Fatalf("load stored workspace config: %v", err)
	}
	if len(stored.Providers) != 0 {
		t.Fatalf("workspace config leaked shared providers: %#v", stored.Providers)
	}
}

func TestLoadConfigUsesIndependentWorkspaceSelection(t *testing.T) {
	setupLibrary(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	if err := SaveWorkspaceConfig(ProjectConfigPath(), Config{Provider: "other", ModelName: "other-model", Style: "default"}); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Provider != "other" || cfg.ModelName != "other-model" {
		t.Fatalf("workspace selection = %s/%s", cfg.Provider, cfg.ModelName)
	}
}

func TestLoadConfigRejectsProvidersInWorkspace(t *testing.T) {
	setupLibrary(t)
	t.Chdir(t.TempDir())
	legacy := Config{
		Provider: "preferred", ModelName: "first-model",
		Providers: map[string]ProviderConfig{"preferred": {APIKey: "duplicate"}},
	}
	if err := SaveConfig(ProjectConfigPath(), legacy); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
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

func TestEffectiveConfigPathAlwaysUsesWorkspace(t *testing.T) {
	t.Chdir(t.TempDir())
	want, _ := filepath.Abs(filepath.Join(".ainovel", "config.json"))
	if got := EffectiveConfigPath(); got != want {
		t.Fatalf("effective config path = %q, want %q", got, want)
	}
}

func TestCorruptWorkspaceConfigFailsLoud(t *testing.T) {
	setupLibrary(t)
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(".ainovel", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(".ainovel", "config.json"), []byte(`{"model":,}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(); err == nil {
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
