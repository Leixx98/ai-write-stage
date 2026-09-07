package host

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/Leixx98/ai-write-stage/internal/imagejob"
)

func TestGenerateImagePromptUsesConfiguredThinkingAndOutputLimit(t *testing.T) {
	var requestBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode request body: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id":"test","object":"chat.completion","created":1,"model":"deepseek-chat",
			"choices":[{"index":0,"message":{"role":"assistant","content":"{\"prompt\":\"ok\"}"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}
		}`))
	}))
	defer server.Close()

	cfg := bootstrap.Config{
		Provider:  "deepseek",
		ModelName: "deepseek-chat",
		Providers: map[string]bootstrap.ProviderConfig{
			"deepseek": {
				APIKey:  "test-key",
				BaseURL: server.URL,
				Models: []bootstrap.ModelConfig{{
					Name: "deepseek-chat", ReasoningEffort: "low",
				}},
			},
		},
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatalf("new model set: %v", err)
	}
	h := &Host{cfg: cfg, models: models}

	got, err := h.GenerateImagePrompt(context.Background(), imagejob.PromptRequest{
		UnitID:       "1-1",
		Chapter:      1,
		Ordinal:      1,
		UnitText:     "test unit",
		SystemPrompt: "Return one JSON object.",
	})
	if err != nil {
		t.Fatalf("generate image prompt: %v", err)
	}
	if got != `{"prompt":"ok"}` {
		t.Fatalf("response = %q", got)
	}
	if got := requestBody["max_tokens"]; got != float64(16384) {
		t.Fatalf("max_tokens = %#v, want 16384", got)
	}
	thinking, ok := requestBody["thinking"].(map[string]any)
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("thinking = %#v, want enabled", requestBody["thinking"])
	}
	if got := requestBody["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort = %#v, want low", got)
	}
}
