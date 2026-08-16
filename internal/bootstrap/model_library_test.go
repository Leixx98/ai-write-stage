package bootstrap

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestModelLibraryRoundTripAndFirstModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	library := ModelLibrary{
		Version:       1,
		ProviderOrder: []string{"second", "first"},
		Providers: map[string]ProviderConfig{
			"first":  {Models: []ModelConfig{{Name: "first-model"}}},
			"second": {Type: "openai", Models: []ModelConfig{{Name: "chosen", ContextWindow: 128000}}},
		},
	}
	if err := SaveModelLibrary(library); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadModelLibrary()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	provider, model, err := loaded.FirstModel()
	if err != nil {
		t.Fatalf("first model: %v", err)
	}
	if provider != "second" || model.Name != "chosen" {
		t.Fatalf("first model = %s/%s", provider, model.Name)
	}
	info, err := os.Stat(filepath.Join(home, ".ainovel", "models.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("model library permissions = %o", info.Mode().Perm())
	}
}

func TestModelLibraryRejectsUnorderedProvider(t *testing.T) {
	library := ModelLibrary{
		Version:       1,
		ProviderOrder: []string{"one"},
		Providers: map[string]ProviderConfig{
			"one": {Models: []ModelConfig{{Name: "a"}}},
			"two": {Models: []ModelConfig{{Name: "b"}}},
		},
	}
	if err := library.Validate(); err == nil {
		t.Fatal("provider missing from provider_order should fail")
	}
}
