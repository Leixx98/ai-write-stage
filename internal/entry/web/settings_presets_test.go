package web

import "testing"

func TestDefaultPromptValuesAreComplete(t *testing.T) {
	values := defaultPromptValues()
	for _, role := range []string{"architect", "chapter_planner", "writer", "editor", "prompter"} {
		if values[role] == "" {
			t.Fatalf("default prompt %q is empty", role)
		}
	}
}

func TestDefaultWritingRulesAreNonEmpty(t *testing.T) {
	if got := defaultWritingRules(); got == "" {
		t.Fatal("default writing rules must not be empty")
	}
}

func TestPresetNameValidation(t *testing.T) {
	if validPresetName("") || validPresetName("   ") || validPresetName(string(make([]rune, 65))) {
		t.Fatal("invalid preset name accepted")
	}
	if !validPresetName("悬疑风格") {
		t.Fatal("valid unicode preset name rejected")
	}
}

func TestMergePromptsDoesNotEraseDefaultsWithEmptyLegacyValues(t *testing.T) {
	got := mergePrompts(map[string]string{"writer": "embedded", "editor": "embedded-editor"}, map[string]string{"writer": "", "editor": "  "})
	if got["writer"] != "embedded" || got["editor"] != "embedded-editor" {
		t.Fatalf("empty legacy values erased defaults: %#v", got)
	}
}
