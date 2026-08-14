package imagejob

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/comfyui"
)

func testPromptSchema() PromptSchema {
	return PromptSchema{SchemaHash: "sha256:test", Fields: []comfyui.CanvasField{
		{ID: "positive_prompt", ValueType: "string", Exposed: true, Source: "prompter"},
		{ID: "steps", ValueType: "integer", Exposed: true, Source: "prompter", Default: 20, Min: 1, Max: 50},
	}}
}

func TestParseAndValidateStrictRejectsUnknownAndMissing(t *testing.T) {
	result, err := ParseAndValidate(`{"positive_prompt":"rain","unknown":"x"}`, testPromptSchema(), true)
	if err == nil || result.Valid || len(result.Errors) != 2 {
		t.Fatalf("expected strict validation errors, result=%#v err=%v", result, err)
	}
}

func TestParseAndValidateNonStrictUsesDefaultAndFencedJSON(t *testing.T) {
	result, err := ParseAndValidate("```json\n{\"positive_prompt\":\"rain\",\"unknown\":true}\n```", testPromptSchema(), false)
	if err != nil || !result.Valid {
		t.Fatalf("expected valid non-strict result: %#v %v", result, err)
	}
	if result.Values["steps"] != int64(20) || len(result.Warnings) != 2 {
		t.Fatalf("unexpected defaults/warnings: %#v", result)
	}
}

func TestParseAndValidateRejectsDuplicateKeys(t *testing.T) {
	_, err := ParseAndValidate(`{"positive_prompt":"a","positive_prompt":"b","steps":2}`, testPromptSchema(), true)
	if err == nil {
		t.Fatal("expected duplicate key error")
	}
}
