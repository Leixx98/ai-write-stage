package comfyui

import "testing"

func canvasFixture() Workflow {
	return Workflow{ID: "wf-1", Name: "demo", Workflow: map[string]any{
		"1": map[string]any{"class_type": "CLIPTextEncode", "inputs": map[string]any{"text": "hello"}},
		"2": map[string]any{"class_type": "SaveImage", "inputs": map[string]any{"images": []any{"1", 0}}},
	}}
}

func TestDefaultCanvasBuildsNodesEdgesAndCards(t *testing.T) {
	c := DefaultCanvas(canvasFixture())
	if c.Format != "ainovel_comfy_canvas_v1" || len(c.Nodes) != 2 || len(c.Edges) != 1 {
		t.Fatalf("unexpected canvas: %#v", c)
	}
	if len(c.MiniTestCards) != 3 || c.Viewport.Scale != 1 {
		t.Fatalf("missing default test canvas state: %#v", c)
	}
	if errs := ValidateCanvas(c, canvasFixture()); len(errs) != 0 {
		t.Fatalf("default canvas should validate: %v", errs)
	}
}

func TestValidateCanvasRejectsUnknownReferences(t *testing.T) {
	w := canvasFixture()
	c := DefaultCanvas(w)
	c.Nodes[0].SourceNodeID = "missing"
	c.Viewport.Scale = 10
	if errs := ValidateCanvas(c, w); len(errs) < 2 {
		t.Fatalf("expected source and viewport errors, got %v", errs)
	}
}

func TestCanvasValuesFieldValuesOverrideCards(t *testing.T) {
	c := CanvasDocument{MiniTestCards: []MiniTestCard{{ID: "prompt_1", Kind: "prompt", Text: "card"}}}
	got := CanvasValues(c, map[string]any{"text": "field"}, nil)
	if got["text"] != "field" {
		t.Fatalf("field value did not override card: %#v", got)
	}
}
