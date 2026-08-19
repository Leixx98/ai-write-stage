package play

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/galgame/runlog"
	"github.com/voocel/ainovel-cli/internal/store"
)

type playLogSink struct {
	mu   sync.Mutex
	recs []runlog.Record
}

func (s *playLogSink) AppendJSONL(_ string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var rec runlog.Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	s.mu.Lock()
	s.recs = append(s.recs, rec)
	s.mu.Unlock()
	return nil
}

func (s *playLogSink) AppendText(string, string) error { return nil }

type playStreamModel struct{}

func (playStreamModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, nil
}

func (playStreamModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	time.Sleep(2 * time.Millisecond)
	body := `{"segment_id":"meet","goal":"重逢","complete_after_segment":false,"notes":""}`
	ch := make(chan agentcore.StreamEvent, 2)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: body}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(body)},
	}}
	close(ch)
	return ch, nil
}

func (playStreamModel) SupportsTools() bool { return false }

func TestGeneratorArchitectUsesStreamAndMarksFirstToken(t *testing.T) {
	sink := &playLogSink{}
	gen := Generator{ArchitectModel: playStreamModel{}, ArchitectPrompt: "规划", PlayID: "rain", Sink: sink}
	out, err := gen.Architect(context.Background(), ArchitectInput{
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		Premise:   "雨夜",
	})
	if err != nil || out.SegmentID != "meet" {
		t.Fatalf("architect = %+v %v", out, err)
	}
	var start, first bool
	for _, rec := range sink.recs {
		if rec.Step != "play_architect" {
			continue
		}
		if rec.Event == runlog.EventStart && rec.Streaming {
			start = true
		}
		if rec.Event == runlog.EventFirstToken && rec.FirstTokenMS > 0 {
			first = true
		}
	}
	if !start || !first {
		t.Fatalf("missing stream markers: %#v", sink.recs)
	}
	joined, _ := json.Marshal(sink.recs)
	if !strings.Contains(string(joined), `"streaming":true`) {
		t.Fatalf("json missing streaming: %s", joined)
	}
}

type playThinkStreamModel struct{}

func (playThinkStreamModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, nil
}

func (playThinkStreamModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	body := `{"segment_id":"meet","goal":"重逢","complete_after_segment":false,"notes":""}`
	ch := make(chan agentcore.StreamEvent, 3)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventThinkingDelta, Delta: "先想场景"}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: body}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(body)},
	}}
	close(ch)
	return ch, nil
}

func (playThinkStreamModel) SupportsTools() bool { return false }

func TestGeneratorRepairsTrailingCommaJSON(t *testing.T) {
	sink := &playLogSink{}
	dirty := `{"segment_id":"meet","goal":"重逢","complete_after_segment":false,"notes":"",}`
	gen := Generator{ArchitectModel: playDirtyJSONModel{body: dirty}, ArchitectPrompt: "规划", PlayID: "rain", Sink: sink}
	out, err := gen.Architect(context.Background(), ArchitectInput{
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		Premise:   "雨夜",
	})
	if err != nil || out.SegmentID != "meet" {
		t.Fatalf("architect = %+v %v", out, err)
	}
	var repaired bool
	for _, rec := range sink.recs {
		if rec.Event == runlog.EventRepair && strings.Contains(rec.Message, "trailing_comma") {
			repaired = true
		}
		if rec.Event == runlog.EventCorrect {
			t.Fatalf("should not correct after repair: %+v", rec)
		}
	}
	if !repaired {
		t.Fatalf("missing repair event: %#v", sink.recs)
	}
}

type playDirtyJSONModel struct{ body string }

func (m playDirtyJSONModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, nil
}

func (m playDirtyJSONModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent, 2)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: m.body}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.body)},
	}}
	close(ch)
	return ch, nil
}

func (playDirtyJSONModel) SupportsTools() bool { return false }

type playCaptureModel struct {
	body     string
	messages []agentcore.Message
	config   agentcore.CallConfig
}

func (m *playCaptureModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.messages = messages
	m.config = agentcore.ResolveCallConfig(opts)
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(m.body)},
	}}, nil
}

func (m *playCaptureModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	resp, err := m.Generate(ctx, messages, tools, opts...)
	if err != nil {
		return nil, err
	}
	ch := make(chan agentcore.StreamEvent, 2)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: resp.Message.TextContent()}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: resp.Message}
	close(ch)
	return ch, nil
}

func (playCaptureModel) SupportsTools() bool { return false }

func TestGeneratorWriterPinsCacheIdentity(t *testing.T) {
	model := &playCaptureModel{body: `{"speaker":"林晚","text":"在。"}`}
	gen := Generator{WriterModel: model, WriterPrompt: "写", PlayID: "rain"}
	if _, err := gen.Writer(context.Background(), WriterInput{
		Card:      store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚"},
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员", Extensions: map[string]any{"x": 1}},
		Premise:   "雨夜",
	}); err != nil {
		t.Fatal(err)
	}
	if model.config.PromptCacheKey != "play-rain-writer" {
		t.Fatalf("cache key = %q", model.config.PromptCacheKey)
	}
	if len(model.messages) < 2 {
		t.Fatalf("messages = %#v", model.messages)
	}
	if model.messages[0].Metadata["cache_control"] != "ephemeral" || model.messages[1].Metadata["cache_control"] != "ephemeral" {
		t.Fatalf("cache metadata = %#v %#v", model.messages[0].Metadata, model.messages[1].Metadata)
	}
	if strings.Contains(model.messages[1].TextContent(), "extensions") {
		t.Fatalf("user payload leaked extensions: %s", model.messages[1].TextContent())
	}
	if !strings.Contains(model.messages[0].TextContent(), "情报员") {
		t.Fatalf("system missing character: %s", model.messages[0].TextContent())
	}
	if strings.Contains(model.messages[1].TextContent(), "recent_beats") {
		t.Fatalf("user still carries recent_beats: %s", model.messages[1].TextContent())
	}
}

func TestGeneratorWriterAppendsSessionHistory(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	model := &playCaptureModel{body: `{"speaker":"林晚","text":"在。"}`}
	gen := Generator{WriterModel: model, WriterPrompt: "写", PlayID: "rain", Store: tavern}
	first := WriterInput{
		Card:      store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "码头"},
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		Premise:   "雨夜",
	}
	if _, err := gen.Writer(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if len(model.messages) != 2 {
		t.Fatalf("first call messages = %#v", model.messages)
	}
	second := first
	second.Card.Location = "仓库"
	model.body = `{"speaker":"林晚","text":"跟我来。"}`
	if _, err := gen.Writer(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if len(model.messages) != 4 {
		t.Fatalf("second call messages = %#v", model.messages)
	}
	if !strings.Contains(model.messages[1].TextContent(), "码头") || !strings.Contains(model.messages[2].TextContent(), "在。") {
		t.Fatalf("history missing first beat: %#v", model.messages)
	}
	if !strings.Contains(model.messages[3].TextContent(), "仓库") || strings.Contains(model.messages[3].TextContent(), "码头") {
		t.Fatalf("current user is not the new card: %s", model.messages[3].TextContent())
	}
	session, err := tavern.LoadWriterSession("rain")
	if err != nil || len(session.Turns) != 2 || session.Turns[1].Text != "跟我来。" {
		t.Fatalf("session = %+v %v", session, err)
	}
}

func TestGeneratorWriterCommitsCompactBeforeCall(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	if err := tavern.SaveWriterSession("rain", store.PlayWriterSession{Turns: []store.PlayWriterTurn{
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Location: "一"}, Speaker: "林晚", Text: strings.Repeat("旧一屏", 400)},
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Location: "二"}, Speaker: "林晚", Text: strings.Repeat("旧二屏", 400)},
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Location: "三"}, Speaker: "林晚", Text: strings.Repeat("近一屏", 400)},
	}}); err != nil {
		t.Fatal(err)
	}
	model := &playFailModel{}
	gen := Generator{WriterModel: model, WriterPrompt: "写", PlayID: "rain", Store: tavern, ContextWindow: 20000}
	_, err := gen.Writer(context.Background(), WriterInput{
		Card:      store.PlayBeatCard{Kind: store.BeatDialogue, Location: "四"},
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		Premise:   "雨夜",
	})
	if err == nil {
		t.Fatal("writer should fail")
	}
	session, err := tavern.LoadWriterSession("rain")
	if err != nil || len(session.Turns) == 0 || session.Turns[0].Card.Location == "一" {
		t.Fatalf("compact should commit before call: %+v %v", session, err)
	}
}

type playFailModel struct{}

func (playFailModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, context.Canceled
}

func (playFailModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, context.Canceled
}

func (playFailModel) SupportsTools() bool { return false }

func TestGeneratorWritesStreamTranscript(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	gen := Generator{ArchitectModel: playThinkStreamModel{}, ArchitectPrompt: "规划", PlayID: "rain", Sink: tavern}
	if _, err := gen.Architect(context.Background(), ArchitectInput{
		Character: store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		Premise:   "雨夜",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := tavern.ReadText("galgame/plays/rain/stream.log")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "[thinking]\n先想场景") || !strings.Contains(got, `"segment_id":"meet"`) {
		t.Fatalf("stream = %s", got)
	}
}
