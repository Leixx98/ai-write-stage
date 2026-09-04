package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/voocel/ainovel-cli/internal/bootstrap"
)

func TestSetupHandlerServesPageAndPresets(t *testing.T) {
	handler := newSetupHandler(bootstrap.SaveSetup, nil)
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/", nil))
	if page.Code != http.StatusOK || !bytes.Contains(page.Body.Bytes(), []byte("setup-form")) || !bytes.Contains(page.Body.Bytes(), []byte(`id="toast"`)) {
		t.Fatalf("setup page returned %d: %s", page.Code, page.Body.String())
	}
	presets := httptest.NewRecorder()
	handler.ServeHTTP(presets, httptest.NewRequest(http.MethodGet, "/api/setup/providers", nil))
	if presets.Code != http.StatusOK || !bytes.Contains(presets.Body.Bytes(), []byte("openrouter")) {
		t.Fatalf("presets returned %d: %s", presets.Code, presets.Body.String())
	}
}

func TestSetupHandlerValidatesAndPersistsRequest(t *testing.T) {
	home := t.TempDir()
	workspace := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(workspace)
	var completed bootstrap.Config
	handler := newSetupHandler(bootstrap.SaveSetup, func(cfg bootstrap.Config) {
		completed = cfg
	})
	request := bootstrap.SetupRequest{Preset: "custom", Provider: "proxy", Type: "openai", BaseURL: "https://proxy.example/v1", Model: "writer-model"}
	data, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewReader(data))
	httpRequest.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, httpRequest)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("setup returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if completed.Provider != "proxy" || completed.ModelName != "writer-model" {
		t.Fatalf("completion config = %#v", completed)
	}
	if _, err := os.Stat(filepath.Join(home, ".ainovel", "models.json")); err != nil {
		t.Fatalf("shared model library: %v", err)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".ainovel")); !os.IsNotExist(err) {
		t.Fatal("setup should not create a cwd .ainovel directory")
	}
}

func TestSetupHandlerDoesNotCompleteInvalidRequest(t *testing.T) {
	called := false
	handler := newSetupHandler(func(request bootstrap.SetupRequest) (bootstrap.Config, error) {
		return bootstrap.Config{}, bootstrap.ValidateSetup(request)
	}, func(bootstrap.Config) {
		called = true
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/setup", bytes.NewBufferString(`{"preset":"openai"}`)))
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid setup returned %d: %s", recorder.Code, recorder.Body.String())
	}
	if called {
		t.Fatal("invalid setup completed")
	}
	testRecorder := httptest.NewRecorder()
	handler.ServeHTTP(testRecorder, httptest.NewRequest(http.MethodPost, "/api/setup/test", bytes.NewBufferString(`{"preset":"openai"}`)))
	if testRecorder.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid connection test returned %d: %s", testRecorder.Code, testRecorder.Body.String())
	}
}
