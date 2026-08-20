package comfyui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestOpenProgressPreservesBasePathAndParsesEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/comfy/ws" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("clientId"); got != "client-job-1" {
			t.Errorf("clientId = %q", got)
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept WebSocket: %v", err)
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		messages := []string{
			`{"type":"execution_cached","data":{"prompt_id":"prompt-1","nodes":["3"]}}`,
			`{"type":"progress","data":{"prompt_id":"prompt-1","node":"12","value":3,"max":20}}`,
			`{"type":"execution_success","data":{"prompt_id":"prompt-1"}}`,
		}
		for _, message := range messages {
			if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
				t.Errorf("write WebSocket event: %v", err)
				return
			}
		}
	}))
	defer server.Close()

	client := &HTTPClient{BaseURL: server.URL + "/comfy", HTTP: server.Client(), MaxResponseBytes: 1 << 20}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	events, err := client.OpenProgress(ctx, "client-job-1")
	if err != nil {
		t.Fatal(err)
	}
	cached := <-events
	if cached.Status != "execution_cached" || cached.PromptID != "prompt-1" {
		t.Fatalf("cached = %+v", cached)
	}
	progress := <-events
	if progress.Status != "progress" || progress.PromptID != "prompt-1" || progress.Node != "12" || progress.Current != 3 || progress.Total != 20 {
		t.Fatalf("progress = %+v", progress)
	}
	terminal := <-events
	if terminal.Status != "execution_success" || terminal.PromptID != "prompt-1" {
		t.Fatalf("terminal = %+v", terminal)
	}
	if _, open := <-events; open {
		t.Fatal("event stream must close after a terminal event")
	}
}

func TestSubmitIncludesClientID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prompt" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Prompt   map[string]any `json:"prompt"`
			ClientID string         `json:"client_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		if body.ClientID != "client-job-2" {
			t.Errorf("client_id = %q", body.ClientID)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"prompt_id":"prompt-2"}`))
	}))
	defer server.Close()
	client := &HTTPClient{BaseURL: server.URL, HTTP: server.Client(), MaxResponseBytes: 1 << 20}

	promptID, err := client.Submit(context.Background(), map[string]any{"1": map[string]any{"class_type": "Test"}}, "client-job-2")
	if err != nil {
		t.Fatal(err)
	}
	if promptID != "prompt-2" {
		t.Fatalf("prompt ID = %q", promptID)
	}
}
