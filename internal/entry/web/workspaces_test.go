package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/workspace"
)

func testProviderConfig() bootstrap.ProviderConfig {
	return bootstrap.ProviderConfig{
		Type: "openai", APIKey: "test-key", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "default-model"}},
	}
}

func testWorkbench(t *testing.T) (*workbench, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := t.TempDir()
	t.Setenv(workspace.EnvRoot, root)
	t.Chdir(root)
	provider := testProviderConfig()
	if err := bootstrap.SaveModelLibrary(bootstrap.ModelLibrary{
		Version: 1, ProviderOrder: []string{"proxy"},
		Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "default-model",
		Providers:  map[string]bootstrap.ProviderConfig{"proxy": provider},
		ProjectDir: root, Style: "default",
	}
	cfg.FillDefaults()
	return newWorkbench(cfg, root), root
}

func decodeEnvelope(t *testing.T, recorder *httptest.ResponseRecorder) apiEnvelope {
	t.Helper()
	var env apiEnvelope
	if err := json.Unmarshal(recorder.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, recorder.Body.String())
	}
	return env
}

func TestWorkspaceBusyError(t *testing.T) {
	if err := workspaceBusyError(true, ""); !errors.Is(err, errWorkspaceBusy) {
		t.Fatalf("running = %v", err)
	}
	if err := workspaceBusyError(false, "导入"); !errors.Is(err, errWorkspaceBusy) {
		t.Fatalf("exclusive = %v", err)
	}
	if err := workspaceBusyError(false, ""); err != nil {
		t.Fatalf("idle = %v", err)
	}
}

func TestWorkspacesListCreateAndOpen(t *testing.T) {
	wb, root := testWorkbench(t)
	defer wb.close()
	handler := newHandler(wb)

	list := httptest.NewRecorder()
	handler.ServeHTTP(list, httptest.NewRequest(http.MethodGet, "/api/v2/workspaces", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("list empty returned %d: %s", list.Code, list.Body.String())
	}
	env := decodeEnvelope(t, list)
	data := env.Data.(map[string]any)
	if items, _ := data["items"].([]any); len(items) != 0 {
		t.Fatalf("expected empty list, got %#v", data["items"])
	}

	create := httptest.NewRecorder()
	handler.ServeHTTP(create, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces", bytes.NewBufferString(`{"name":"边城"}`)))
	if create.Code != http.StatusOK {
		t.Fatalf("create returned %d: %s", create.Code, create.Body.String())
	}
	if _, err := os.Stat(filepath.Join(root, workspace.DirName, "边城")); err != nil {
		t.Fatal(err)
	}

	dup := httptest.NewRecorder()
	handler.ServeHTTP(dup, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces", bytes.NewBufferString(`{"name":"边城"}`)))
	if dup.Code != http.StatusConflict {
		t.Fatalf("duplicate create returned %d: %s", dup.Code, dup.Body.String())
	}

	bad := httptest.NewRecorder()
	handler.ServeHTTP(bad, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces", bytes.NewBufferString(`{"name":".."}`)))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid name returned %d: %s", bad.Code, bad.Body.String())
	}

	open := httptest.NewRecorder()
	handler.ServeHTTP(open, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces/open", bytes.NewBufferString(`{"name":"边城"}`)))
	if open.Code != http.StatusOK {
		t.Fatalf("open returned %d: %s", open.Code, open.Body.String())
	}
	opened := decodeEnvelope(t, open).Data.(map[string]any)
	if opened["workspace"] != "边城" {
		t.Fatalf("open payload = %#v", opened)
	}
	if workspace.LoadLast(root) != "边城" {
		t.Fatalf("last workspace = %q", workspace.LoadLast(root))
	}

	state := httptest.NewRecorder()
	handler.ServeHTTP(state, httptest.NewRequest(http.MethodGet, "/api/v2/state", nil))
	if state.Code != http.StatusOK {
		t.Fatalf("state returned %d: %s", state.Code, state.Body.String())
	}
	got := decodeEnvelope(t, state).Data.(map[string]any)
	if got["workspace"] != "边城" || got["workspace_id"] == "" {
		t.Fatalf("state payload = %#v", got)
	}

	second, err := workspace.Create(root, "悬疑短篇")
	if err != nil {
		t.Fatal(err)
	}
	_ = second
	switchRec := httptest.NewRecorder()
	handler.ServeHTTP(switchRec, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces/open", bytes.NewBufferString(`{"name":"悬疑短篇"}`)))
	if switchRec.Code != http.StatusOK {
		t.Fatalf("switch returned %d: %s", switchRec.Code, switchRec.Body.String())
	}
	if decodeEnvelope(t, switchRec).Data.(map[string]any)["workspace"] != "悬疑短篇" {
		t.Fatal("idle switch did not change workspace")
	}
	if wb.rt == nil || filepath.Base(wb.rt.Dir()) != "悬疑短篇" {
		t.Fatalf("host dir = %q", wb.rt.Dir())
	}
}

func TestOpenWorkspaceRejectedWhenBusy(t *testing.T) {
	wb, _ := testWorkbench(t)
	defer wb.close()
	if _, err := workspace.Create(wb.root, "边城"); err != nil {
		t.Fatal(err)
	}
	if _, err := workspace.Create(wb.root, "另一本"); err != nil {
		t.Fatal(err)
	}
	if err := wb.open("边城"); err != nil {
		t.Fatal(err)
	}
	wb.mu.Lock()
	if err := workspaceBusyError(true, ""); err == nil {
		wb.mu.Unlock()
		t.Fatal("expected busy error")
	}
	wb.mu.Unlock()
	missing := httptest.NewRecorder()
	newHandler(wb).ServeHTTP(missing, httptest.NewRequest(http.MethodPost, "/api/v2/workspaces/open", bytes.NewBufferString(`{"name":"没有"}`)))
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing workspace returned %d: %s", missing.Code, missing.Body.String())
	}
}

func TestOpenWorkspaceFollowsBookConfig(t *testing.T) {
	wb, root := testWorkbench(t)
	defer wb.close()
	a, err := workspace.Create(root, "甲")
	if err != nil {
		t.Fatal(err)
	}
	b, err := workspace.Create(root, "乙")
	if err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.SaveWorkspaceConfig(bootstrap.WorkspaceConfigPath(a.Path), bootstrap.Config{Provider: "proxy", ModelName: "default-model", Style: "suspense"}); err != nil {
		t.Fatal(err)
	}
	if err := bootstrap.SaveWorkspaceConfig(bootstrap.WorkspaceConfigPath(b.Path), bootstrap.Config{Provider: "proxy", ModelName: "default-model", Style: "fantasy"}); err != nil {
		t.Fatal(err)
	}
	if err := wb.open("甲"); err != nil {
		t.Fatal(err)
	}
	if got := wb.rt.Snapshot().Style; got != "suspense" {
		t.Fatalf("workspace 甲 style = %q", got)
	}
	if err := wb.open("乙"); err != nil {
		t.Fatal(err)
	}
	if got := wb.rt.Snapshot().Style; got != "fantasy" {
		t.Fatalf("workspace 乙 style = %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, ".ainovel")); !os.IsNotExist(err) {
		t.Fatal("opening a book should not create a cwd .ainovel directory")
	}
}

func TestBookAPIsRequireWorkspace(t *testing.T) {
	wb, _ := testWorkbench(t)
	defer wb.close()
	handler := newHandler(wb)
	for _, path := range []string{"/api/v2/chapters", "/api/v2/image-generation/providers"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s without workspace returned %d: %s", path, rec.Code, rec.Body.String())
		}
	}
}
