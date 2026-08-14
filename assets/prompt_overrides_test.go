package assets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writePromptSettings(t *testing.T, dir string, value any) {
	t.Helper()
	path := filepath.Join(dir, "meta", "web", "prompts.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPromptOverridesUsesActivePreset(t *testing.T) {
	dir := t.TempDir()
	writePromptSettings(t, dir, map[string]any{
		"version":       2,
		"active_preset": "冷峻",
		"presets": map[string]any{
			"默认配置": map[string]any{"prompts": map[string]string{"writer": "default"}},
			"冷峻":   map[string]any{"prompts": map[string]string{"writer": "custom", "editor": "review"}},
		},
		"prompts": map[string]string{"writer": "stale"},
	})
	got, err := LoadPromptOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["writer"] != "custom" || got["editor"] != "review" {
		t.Fatalf("active preset not selected: %#v", got)
	}
}

func TestLoadPromptOverridesSupportsLegacyDocument(t *testing.T) {
	dir := t.TempDir()
	writePromptSettings(t, dir, map[string]any{
		"prompts": map[string]string{"writer": "legacy"},
	})
	got, err := LoadPromptOverrides(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got["writer"] != "legacy" {
		t.Fatalf("legacy prompt not loaded: %#v", got)
	}
}

func TestApplyPromptOverridesMapsArchitectAndCoreRoles(t *testing.T) {
	bundle := Bundle{Prompts: Prompts{
		ArchitectShort: "short",
		ArchitectLong:  "long",
		Writer:         "writer",
	}}
	ApplyPromptOverrides(&bundle, map[string]string{
		"architect":       "architect-custom",
		"chapter_planner": "planner-custom",
		"writer":          "writer-custom",
		"editor":          "editor-custom",
		"prompter":        "ignored-by-runtime",
	})
	if bundle.Prompts.ArchitectShort != "architect-custom" || bundle.Prompts.ArchitectLong != "architect-custom" {
		t.Fatalf("architect override not shared: %#v", bundle.Prompts)
	}
	if bundle.Prompts.ChapterPlanner != "planner-custom" || bundle.Prompts.Writer != "writer-custom" || bundle.Prompts.Editor != "editor-custom" {
		t.Fatalf("core role overrides not applied: %#v", bundle.Prompts)
	}
}
