package web

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Leixx98/ai-write-stage/assets"
	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/Leixx98/ai-write-stage/internal/host"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

func newPlayLogTestHost(t *testing.T) *host.Host {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	workspace := t.TempDir()
	t.Chdir(workspace)
	provider := bootstrap.ProviderConfig{
		Type: "openai", APIKey: "test-key", BaseURL: "https://example.com/v1",
		Models: []bootstrap.ModelConfig{{Name: "default-model"}},
	}
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "default-model", Providers: map[string]bootstrap.ProviderConfig{"proxy": provider},
		OutputDir: filepath.Join(workspace, "output"), ProjectDir: workspace, Style: "default",
	}
	rt, err := host.New(cfg, assets.Load("default", assets.DefaultLoadOptions(cfg.OutputDir)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rt.Close)
	return rt
}

func readSSEData(t *testing.T, r *bufio.Reader) map[string]any {
	t.Helper()
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read sse frame: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}
		var payload map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &payload); err != nil {
			t.Fatalf("unmarshal sse frame: %v", err)
		}
		return payload
	}
}

func TestPlayLogStreamSendsSnapshotThenLiveIncrements(t *testing.T) {
	rt := newPlayLogTestHost(t)
	controller := newV2Controller(rt)
	tavern := rt.Roots().Tavern

	now := time.Now().UTC()
	play := store.PlayMeta{ID: "play_1", Name: "测试剧场", CharacterID: "char", Premise: "测试剧情", Status: store.PlayIdle, CreatedAt: now, UpdatedAt: now}
	if err := tavern.SavePlay(play); err != nil {
		t.Fatal(err)
	}
	// 请求前写入的行只应出现在快照里，不得作为增量重放。
	if err := tavern.AppendText("plays/play_1/runtime.log", "boot line"); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		controller.playLogStream(w, r, "play_1")
	}))
	defer server.Close()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if ct := response.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type = %q", ct)
	}
	reader := bufio.NewReader(response.Body)

	snapshot := readSSEData(t, reader)
	if snapshot["type"] != "snapshot" {
		t.Fatalf("first frame = %#v", snapshot)
	}
	if events, _ := snapshot["events"].(string); !strings.Contains(events, "boot line") {
		t.Fatalf("snapshot events = %q", events)
	}

	if err := tavern.AppendText("plays/play_1/runtime.log", "live line"); err != nil {
		t.Fatal(err)
	}
	live := readSSEData(t, reader)
	if live["type"] != "events" || live["text"] != "live line\n" {
		t.Fatalf("live frame = %#v", live)
	}

	if err := tavern.AppendRaw("plays/play_1/stream.log", "delta"); err != nil {
		t.Fatal(err)
	}
	stream := readSSEData(t, reader)
	if stream["type"] != "stream" || stream["text"] != "delta" {
		t.Fatalf("stream frame = %#v", stream)
	}
}

func TestPlayLogStreamUnknownPlayReturns404(t *testing.T) {
	rt := newPlayLogTestHost(t)
	controller := newV2Controller(rt)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/log/stream", nil)
	controller.playLogStream(recorder, request, "missing")
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("code = %d body=%s", recorder.Code, recorder.Body.String())
	}
}
