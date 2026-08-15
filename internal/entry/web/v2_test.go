package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
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

func TestSavePrompterPresetPersistsPresetAndWorkflowTemplate(t *testing.T) {
	st := store.NewStore(t.TempDir())
	wf := comfyui.Workflow{ID: "wf", Name: "test", Workflow: map[string]any{}}
	if err := st.ComfyUI.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	controller := &v2Controller{st: st, running: map[string]context.CancelFunc{}}
	body, _ := json.Marshal(map[string]any{
		"action": "save_as", "name": "双人构图", "workflow_id": "wf",
		"template": "return exact json for two characters", "overwrite": false,
	})
	req := httptest.NewRequest(http.MethodPut, "/api/v2/comfyui/prompter-presets", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	controller.prompterPresets(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("save preset returned %d: %s", recorder.Code, recorder.Body.String())
	}
	doc, err := st.ComfyUI.LoadPrompterPresets()
	if err != nil || doc.Presets["双人构图"].Template != "return exact json for two characters" {
		t.Fatalf("preset was not persisted: %#v, %v", doc, err)
	}
	canvas, err := st.ComfyUI.LoadWorkflowCanvas("wf")
	if err != nil || canvas.PrompterPreset != "双人构图" || canvas.PrompterTemplate != "return exact json for two characters" {
		t.Fatalf("workflow prompt was not updated: %#v, %v", canvas, err)
	}
	var response apiEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(response.Data)
	var schema imagejob.PromptSchema
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Preset != "双人构图" || len(schema.Presets) != len(imagejob.PrompterPresets())+1 {
		t.Fatalf("saved preset was not returned with built-ins: %#v", schema.Presets)
	}
}
