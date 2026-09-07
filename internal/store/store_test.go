package store

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/comfyui"
	"github.com/Leixx98/ai-write-stage/internal/imagejob"
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

func TestImageGenerationConfigurationIsMachineGlobal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AINOVEL_HOME", home)
	a := Open(filepath.Join(t.TempDir(), "novel-a"), "")
	b := Open(filepath.Join(t.TempDir(), "novel-b"), "")
	wf := comfyui.Workflow{ID: "shared", Name: "shared workflow", Workflow: map[string]any{"1": map[string]any{"class_type": "SaveImage"}}}
	if err := a.ComfyUI.SaveWorkflow(wf); err != nil {
		t.Fatal(err)
	}
	if err := a.ImageConfig.SaveProfile(imagejob.Profile{ID: "shared", Name: "Shared", Provider: "comfyui", ImageCount: 1, TimeoutMS: 600000, ProviderOptions: map[string]any{"workflow_id": "shared"}}); err != nil {
		t.Fatal(err)
	}
	settings := imagejob.DefaultSettings()
	settings.Novel = imagejob.SceneConfig{Enabled: true, AutoGenerate: true, DefaultProfileID: "shared"}
	if err := a.ImageConfig.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ComfyUI.LoadWorkflow("shared"); err != nil || got.Name != wf.Name {
		t.Fatalf("workflow=%#v err=%v", got, err)
	}
	if got, err := b.ImageConfig.LoadProfile("shared"); err != nil || got.Provider != "comfyui" {
		t.Fatalf("profile=%#v err=%v", got, err)
	}
	if got, err := b.ImageConfig.LoadSettings(); err != nil || got.Novel.DefaultProfileID != "shared" {
		t.Fatalf("settings=%#v err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(home, "image-generation", "providers", "comfyui", "workflows", "shared.json")); err != nil {
		t.Fatalf("global workflow path: %v", err)
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
