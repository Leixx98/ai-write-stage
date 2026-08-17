package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestV2ControllerUsesHostStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	t.Chdir(workspace)
	provider := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "test-key", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "default-model"}},
	}
	if err := bootstrap.SaveModelLibrary(bootstrap.ModelLibrary{
		Version: 1, ProviderOrder: []string{"proxy"},
		Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "default-model", Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
		OutputDir: filepath.Join(workspace, "output", "novel"), ProjectDir: workspace, Style: "default",
	}
	rt, err := host.New(cfg, assets.Load("default", assets.DefaultLoadOptions(cfg.OutputDir)))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	controller := newV2Controller(rt)
	if controller.st != rt.Store() || controller.media != rt.Roots().Media || controller.tavern != rt.Roots().Tavern {
		t.Fatal("web controller must reuse the Host workspace roots")
	}
}

func TestModelSettingsSelectRolePersistsWorkspaceWithoutSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	t.Chdir(workspace)
	provider := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "super-secret", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "default-model"}, {Name: "writer-model"}},
	}
	if err := bootstrap.SaveModelLibrary(bootstrap.ModelLibrary{
		Version: 1, ProviderOrder: []string{"proxy"},
		Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "default-model", Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
		OutputDir: filepath.Join(workspace, "output", "novel"), ProjectDir: workspace, Style: "default",
	}
	rt, err := host.New(cfg, assets.Load("default", assets.DefaultLoadOptions(cfg.OutputDir)))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	controller := newV2Controller(rt)

	body := bytes.NewBufferString(`{"action":"select_model","role":"writer","provider":"proxy","model":"writer-model"}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v2/settings/models", body)
	recorder := httptest.NewRecorder()
	controller.settingsModels(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("select role returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if bytes.Contains(recorder.Body.Bytes(), []byte("super-secret")) {
		t.Fatal("API response exposed API key")
	}
	stored, err := bootstrap.LoadConfigFile(bootstrap.ProjectConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.Providers) != 0 || stored.Roles["writer"].Model != "writer-model" {
		t.Fatalf("workspace config = %#v", stored)
	}
}

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

func TestWebWorkspaceIDIsStableAndScopedToOutputDirectory(t *testing.T) {
	first := webWorkspaceID(t.TempDir())
	if first == "" {
		t.Fatal("workspace ID is empty")
	}
	dir := t.TempDir()
	if webWorkspaceID(dir) != webWorkspaceID(dir) {
		t.Fatal("workspace ID changed for the same output directory")
	}
	if first == webWorkspaceID(dir) {
		t.Fatal("different output directories shared a workspace ID")
	}
}

func TestSavePrompterPresetPersistsPresetAndWorkflowTemplate(t *testing.T) {
	roots := store.Open(t.TempDir(), "")
	st := roots.Facts
	media := roots.Media
	wf := comfyui.Workflow{ID: "wf", Name: "test", Workflow: map[string]any{}}
	if err := media.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	controller := &v2Controller{st: st, media: media}
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
	doc, err := media.LoadPrompterPresets()
	if err != nil || doc.Presets["双人构图"].Template != "return exact json for two characters" {
		t.Fatalf("preset was not persisted: %#v, %v", doc, err)
	}
	canvas, err := media.LoadWorkflowCanvas("wf")
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
