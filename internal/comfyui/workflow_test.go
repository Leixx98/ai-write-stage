package comfyui

import "testing"

func TestValidateURL(t *testing.T) {
	for _, raw := range []string{"file:///tmp/x", "http://", "http://host:0", "http://host:65536", "http://user:pass@host"} {
		if err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) accepted invalid URL", raw)
		}
	}
	if err := ValidateURL("http://127.0.0.1:8188"); err != nil {
		t.Fatal(err)
	}
}

func TestApplyBindingsDeepCopyAndTypes(t *testing.T) {
	w := Workflow{ID: "wf", Workflow: map[string]any{"1": map[string]any{"class_type": "KSampler", "inputs": map[string]any{"steps": 1}}}, Bindings: []Binding{{Key: "steps", NodeID: "1", Path: "inputs.steps", Type: "integer", Required: true}}}
	out, err := ApplyBindings(w, map[string]any{"steps": float64(20)})
	if err != nil {
		t.Fatal(err)
	}
	if out["1"].(map[string]any)["inputs"].(map[string]any)["steps"] != int64(20) {
		t.Fatalf("binding was not applied: %#v", out)
	}
	if w.Workflow["1"].(map[string]any)["inputs"].(map[string]any)["steps"] != 1 {
		t.Fatal("binding mutated source workflow")
	}
}
