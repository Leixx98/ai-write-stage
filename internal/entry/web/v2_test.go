package web

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestMergeCanvasRuntimeBindsPromptAlias(t *testing.T) {
	wf := comfyui.Workflow{
		ID:       "wf",
		Workflow: map[string]any{"2": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "old"}}},
		Config:   &comfyui.WorkflowConfig{},
	}
	canvas := comfyui.CanvasDocument{Fields: []comfyui.CanvasField{{ID: "2::text", NodeID: "2", Input: "text", ValueType: "string", Exposed: true, Default: "default"}}}
	values := map[string]any{"positive_prompt": "new prompt"}
	if err := mergeCanvasRuntime(&wf, canvas, values); err != nil {
		t.Fatal(err)
	}
	bound, err := comfyui.ApplyBindings(wf, values)
	if err != nil {
		t.Fatal(err)
	}
	got := bound["2"].(map[string]any)["inputs"].(map[string]any)["text"]
	if got != "new prompt" {
		t.Fatalf("prompt alias was not bound: %#v", got)
	}
}

func TestMergeCanvasRuntimeRejectsAmbiguousInputAlias(t *testing.T) {
	wf := comfyui.Workflow{ID: "wf", Workflow: map[string]any{
		"2": map[string]any{"class_type": "A", "inputs": map[string]any{"text": "a"}},
		"3": map[string]any{"class_type": "B", "inputs": map[string]any{"text": "b"}},
	}}
	canvas := comfyui.CanvasDocument{Fields: []comfyui.CanvasField{
		{ID: "2::text", NodeID: "2", Input: "text", ValueType: "string", Exposed: true},
		{ID: "3::text", NodeID: "3", Input: "text", ValueType: "string", Exposed: true},
	}}
	if err := mergeCanvasRuntime(&wf, canvas, map[string]any{"text": "ambiguous"}); err == nil {
		t.Fatal("expected ambiguous input alias error")
	}
}

func TestClassifyOutputsForcesImageFromMIME(t *testing.T) {
	job := store.ImageJob{JobID: "img_test"}
	outputs := map[string]any{"7": map[string]any{"images": []any{map[string]any{"filename": "preview.bin", "mime": "image/png"}}}}
	items := classifyOutputs(outputs, job)
	if len(items) != 1 {
		t.Fatalf("expected one output, got %#v", items)
	}
	if items[0].Kind != "image" || !items[0].Previewable || items[0].MIME != "image/png" {
		t.Fatalf("expected previewable image, got %#v", items[0])
	}
}

func TestOutputKindUsesImageExtensionWithoutClassType(t *testing.T) {
	if got := outputKind("render.webp", "", ""); got != "image" {
		t.Fatalf("expected image output, got %q", got)
	}
}
