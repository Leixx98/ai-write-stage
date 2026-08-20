package imagejob

import (
	"strings"
	"testing"
)

func TestComposeSystemPromptIncludesFieldsAndNotes(t *testing.T) {
	fields := []PromptField{
		{ID: "text", ValueType: "string", Note: "正向画面描述"},
		{ID: "seed", ValueType: "integer"},
	}
	got := ComposeSystemPrompt("", fields)
	if !strings.Contains(got, DefaultPrompterTemplate) {
		t.Fatal("composed prompt missing default template")
	}
	if !strings.Contains(got, `"text": ""`) || !strings.Contains(got, `"seed": 0`) {
		t.Fatalf("composed prompt missing field JSON: %s", got)
	}
	if !strings.Contains(got, "text: 正向画面描述") || !strings.Contains(got, "seed: ") {
		t.Fatalf("composed prompt missing field notes: %s", got)
	}
}

func TestResolvePrompterTemplateUsesPresetWhenCustomEmpty(t *testing.T) {
	got := ResolvePrompterTemplate(PresetZImage, "")
	if !strings.Contains(got, "Z-Image") || strings.Contains(got, "Danbooru tag") {
		t.Fatalf("zimage preset not selected: %s", got)
	}
	custom := "custom template for this workflow"
	if ResolvePrompterTemplate(PresetZImage, custom) != custom {
		t.Fatal("custom template should win over preset")
	}
	if ResolvePrompterTemplate("unknown", "") != DefaultPrompterTemplate {
		t.Fatal("unknown preset should fall back to default template")
	}
}

func TestBuildPromptSchemaIncludesPresetsAndConfiguredFields(t *testing.T) {
	schema, err := BuildPromptSchema("wf", []PromptField{
		{ID: "text", ValueType: "string", Control: "textarea", Exposed: true, Source: "default"},
	}, PresetDanbooru, "")
	if err != nil {
		t.Fatal(err)
	}
	if schema.Preset != PresetDanbooru || !strings.Contains(schema.Template, "Danbooru") {
		t.Fatalf("danbooru preset not applied: %#v", schema)
	}
	if len(schema.Presets) != 4 {
		t.Fatalf("expected 4 presets, got %d", len(schema.Presets))
	}
	if !strings.Contains(schema.Composed, `"text": ""`) {
		t.Fatalf("composed prompt missing field JSON: %s", schema.Composed)
	}
}
