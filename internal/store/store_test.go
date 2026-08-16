package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
)

func TestFoundationMissingReturnsReadError(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outline.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.FoundationMissing(); err == nil {
		t.Fatal("损坏的大纲必须返回读取错误，不能降级成缺失项")
	}
}

func TestProjectComfyUIDefinitionsAreSharedAcrossWorkspaces(t *testing.T) {
	project := t.TempDir()
	workspaceA := filepath.Join(t.TempDir(), "novel-a")
	workspaceB := filepath.Join(t.TempDir(), "novel-b")
	a := NewStoreForProject(workspaceA, project)
	b := NewStoreForProject(workspaceB, project)
	wf := comfyui.Workflow{ID: "shared", Name: "共享工作流", Workflow: map[string]any{"1": map[string]any{"class_type": "SaveImage"}}}
	if err := a.ComfyUI.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	if err := a.ComfyUI.SaveBridgeConfig(imagejob.BridgeConfig{WorkflowID: "shared"}); err != nil {
		t.Fatal(err)
	}
	if err := a.ComfyUI.SavePrompterPresets(imagejob.PrompterPresetDocument{Version: 1, Presets: map[string]imagejob.PrompterPreset{
		"shared": {ID: "shared", Label: "共享", Template: "custom template"},
	}}); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ComfyUI.LoadWorkflow("shared"); err != nil || got.Name != wf.Name {
		t.Fatalf("workflow was not shared: %#v, %v", got, err)
	}
	if got, err := b.ComfyUI.LoadBridgeConfig(); err != nil || got.WorkflowID != "shared" {
		t.Fatalf("bridge config was not shared: %#v, %v", got, err)
	}
	if got, err := b.ComfyUI.LoadPrompterPresets(); err != nil || got.Presets["shared"].Template != "custom template" {
		t.Fatalf("prompter presets were not shared: %#v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(project, ".ainovel", "comfyui", "workflows", "shared.json")); err != nil {
		t.Fatalf("shared workflow was not written to project directory: %v", err)
	}
}

func TestClearHandledSteerKeepsIntentWhenProgressReadFails(t *testing.T) {
	dir := t.TempDir()
	st := NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.RunMeta.Init("default", "test", "model"); err != nil {
		t.Fatalf("RunMeta.Init: %v", err)
	}
	if err := st.RunMeta.SetPendingSteer("保留这条干预"); err != nil {
		t.Fatalf("SetPendingSteer: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.ClearHandledSteer(); err == nil {
		t.Fatal("corrupt progress should make ClearHandledSteer fail")
	}
	meta, err := st.RunMeta.Load()
	if err != nil {
		t.Fatalf("RunMeta.Load: %v", err)
	}
	if meta == nil || meta.PendingSteer != "保留这条干预" {
		t.Fatalf("recovery intent was lost after partial clear: %+v", meta)
	}
}
