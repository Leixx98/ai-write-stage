package bootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateSetupRejectsIncompleteRequest(t *testing.T) {
	cases := []SetupRequest{
		{Preset: "missing", APIKey: "key", Model: "model"},
		{Preset: "openai", Model: "model"},
		{Preset: "custom", Provider: "proxy", Model: "model"},
		{Preset: "custom", Type: "openai", Model: "model"},
		{Preset: "openai", APIKey: "key"},
		{Preset: "openai", APIKey: "key", Model: "model", BaseURL: "file:///tmp/model"},
	}
	for _, request := range cases {
		if err := ValidateSetup(request); err == nil {
			t.Fatalf("request should fail validation: %#v", request)
		}
	}
}

func TestSaveSetupWritesSharedAndWorkspaceFiles(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(workspace)
	request := SetupRequest{Preset: "openrouter", APIKey: "secret-key", Model: "test-model"}
	cfg, err := SaveSetup(request)
	if err != nil {
		t.Fatalf("save setup: %v", err)
	}
	if cfg.Provider != "openrouter" || cfg.ModelName != "test-model" {
		t.Fatalf("saved config selection = %s/%s", cfg.Provider, cfg.ModelName)
	}
	library, err := LoadModelLibrary()
	if err != nil {
		t.Fatalf("load model library: %v", err)
	}
	provider := library.Providers["openrouter"]
	if provider.APIKey != "secret-key" || provider.BaseURL != "https://openrouter.ai/api/v1" {
		t.Fatalf("saved provider = %#v", provider)
	}
	project, err := LoadConfigFile(ProjectConfigPath())
	if err != nil {
		t.Fatalf("load workspace config: %v", err)
	}
	if len(project.Providers) != 0 || project.Provider != "openrouter" || project.ModelName != "test-model" {
		t.Fatalf("workspace config = %#v", project)
	}
	if _, err := os.Stat(filepath.Join(home, ".ainovel", "config.example.jsonc")); err != nil {
		t.Fatalf("example config: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".ainovel", "config.json")); err != nil {
		t.Fatalf("workspace config location: %v", err)
	}
}

func TestProviderPresetsReturnsIndependentSlice(t *testing.T) {
	first := ProviderPresets()
	first[0].Name = "changed"
	second := ProviderPresets()
	if second[0].Name == "changed" {
		t.Fatal("provider presets returned shared mutable data")
	}
}
