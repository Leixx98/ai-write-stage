package runlog

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

type memSink struct {
	mu    sync.Mutex
	jsonl map[string][]any
	text  map[string][]string
	raw   map[string]string
}

func newMemSink() *memSink {
	return &memSink{jsonl: map[string][]any{}, text: map[string][]string{}, raw: map[string]string{}}
}

func (s *memSink) AppendJSONL(rel string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		return err
	}
	s.jsonl[rel] = append(s.jsonl[rel], rec)
	return nil
}

func (s *memSink) AppendText(rel string, line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.text[rel] = append(s.text[rel], strings.TrimRight(line, "\n"))
	return nil
}

func (s *memSink) AppendRaw(rel string, data string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.raw[rel] += data
	return nil
}

func TestWriteRoutesChatAndPlay(t *testing.T) {
	sink := newMemSink()
	chat := Record{Mode: ModeChat, SessionID: "sess_1", Step: "reply", Event: EventStart, CallID: "c1"}
	Write(sink, &chat)
	play := Record{Mode: ModePlay, PlayID: "play_1", Step: "play_writer", Event: EventStart, CallID: "c2"}
	Write(sink, &play)
	if len(sink.text[IndexRel]) != 2 {
		t.Fatalf("index lines = %d", len(sink.text[IndexRel]))
	}
	if len(sink.text["sessions/sess_1/runtime.log"]) != 1 {
		t.Fatalf("chat runtime missing: %+v", sink.text)
	}
	if len(sink.jsonl["sessions/sess_1/calls.jsonl"]) != 1 {
		t.Fatalf("chat jsonl missing: %+v", sink.jsonl)
	}
	if len(sink.text["plays/play_1/runtime.log"]) != 1 {
		t.Fatalf("play runtime missing: %+v", sink.text)
	}
	if len(sink.jsonl["plays/play_1/calls.jsonl"]) != 1 {
		t.Fatalf("play jsonl missing: %+v", sink.jsonl)
	}
}

func TestUnsafeIDStaysOnIndex(t *testing.T) {
	sink := newMemSink()
	Write(sink, &Record{Mode: ModeChat, SessionID: "../escape", Event: EventStart, CallID: "x"})
	if len(sink.text[IndexRel]) != 1 {
		t.Fatalf("index = %v", sink.text[IndexRel])
	}
	if len(sink.text) != 1 {
		t.Fatalf("unsafe id should not create scoped files: %+v", sink.text)
	}
}

func TestHeartbeatThenFinish(t *testing.T) {
	sink := newMemSink()
	call := NewCall(Record{Mode: ModePlay, PlayID: "rain", Step: "play_architect", Streaming: false})
	Start(sink, call)
	stop := Heartbeat(sink, call, 20*time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	stop()
	Finish(sink, call, nil)
	var heartbeats int
	for _, rec := range sink.jsonl["plays/rain/calls.jsonl"] {
		got := rec.(Record)
		if got.Event == EventHeartbeat {
			heartbeats++
		}
	}
	if heartbeats < 1 {
		t.Fatalf("expected heartbeat while waiting, jsonl=%+v text=%+v", sink.jsonl, sink.text)
	}
	joined := strings.Join(sink.text[IndexRel], "\n")
	if !strings.Contains(joined, "仍在等待最终结果") || !strings.Contains(joined, "非流式调用结束") {
		t.Fatalf("index missing wait/finish: %s", joined)
	}
}

func TestApplyResponseKeepsThinkingSnippetShort(t *testing.T) {
	rec := Record{}
	thinking := strings.Repeat("思考过程很长 ", 40)
	ApplyResponse(&rec, &agentcore.LLMResponse{Message: agentcore.Message{
		StopReason: agentcore.StopReasonStop,
		Content: []agentcore.ContentBlock{
			agentcore.ThinkingBlock(thinking),
			agentcore.TextBlock("你好。"),
		},
		Usage: &agentcore.Usage{Input: 100, Output: 20},
	}})
	if rec.ThinkingChars < 100 || rec.OutputChars != 3 {
		t.Fatalf("chars thinking=%d output=%d", rec.ThinkingChars, rec.OutputChars)
	}
	if strings.Contains(rec.ThinkingSnippet, thinking) || len([]rune(rec.ThinkingSnippet)) > 90 {
		t.Fatalf("snippet too long: %q", rec.ThinkingSnippet)
	}
	if rec.UsageInput != 100 || rec.StopReason != "stop" {
		t.Fatalf("usage/stop = %+v", rec)
	}
}

func TestFormatLineIncludesCacheHit(t *testing.T) {
	line := formatLine(Record{
		TS:    time.Date(2026, 8, 19, 8, 0, 0, 0, time.UTC),
		Event: EventFinish, Mode: ModePlay, Step: "play_writer", PlayID: "rain",
		UsageInput: 1000, UsageOutput: 80, UsageCacheRead: 750, Streaming: true,
		Message: "流式调用结束",
	})
	if !strings.Contains(line, "cache_hit=750") || !strings.Contains(line, "cache_miss=250") || !strings.Contains(line, "cache_rate=75.0%") {
		t.Fatalf("line = %s", line)
	}
}

func TestApplyResponseCopiesCacheRead(t *testing.T) {
	rec := Record{}
	ApplyResponse(&rec, &agentcore.LLMResponse{Message: agentcore.Message{
		Content: []agentcore.ContentBlock{agentcore.TextBlock("在。")},
		Usage:   &agentcore.Usage{Input: 800, Output: 20, CacheRead: 640, CacheWrite: 0},
	}})
	if rec.UsageCacheRead != 640 || rec.UsageInput != 800 {
		t.Fatalf("usage = %+v", rec)
	}
	hit, miss, rate := cacheHit(rec)
	if hit != 640 || miss != 160 || rate < 79.9 || rate > 80.1 {
		t.Fatalf("hit=%d miss=%d rate=%f", hit, miss, rate)
	}
}

func TestMarkFirstTokenWritesOnce(t *testing.T) {
	sink := newMemSink()
	call := NewCall(Record{Mode: ModeChat, SessionID: "sess_1", Step: "reply", Streaming: true})
	Start(sink, call)
	time.Sleep(2 * time.Millisecond)
	MarkFirstToken(sink, call)
	MarkFirstToken(sink, call)
	Finish(sink, call, nil)
	var first int
	for _, rec := range sink.jsonl["sessions/sess_1/calls.jsonl"] {
		got := rec.(Record)
		if got.Event == EventFirstToken {
			first++
			if got.FirstTokenMS <= 0 {
				t.Fatalf("first_token_ms missing: %+v", got)
			}
		}
	}
	if first != 1 {
		t.Fatalf("first_token events = %d jsonl=%+v", first, sink.jsonl)
	}
	joined := strings.Join(sink.text[IndexRel], "\n")
	if !strings.Contains(joined, "streaming=true") || !strings.Contains(joined, "开始流式生成") || !strings.Contains(joined, "流式调用结束") {
		t.Fatalf("index missing stream markers: %s", joined)
	}
}

func TestRepairedWritesRuntimeLine(t *testing.T) {
	sink := newMemSink()
	call := NewCall(Record{Mode: ModePlay, PlayID: "rain", Step: "play_architect", Streaming: true})
	Start(sink, call)
	Repaired(sink, call, []string{"trailing_comma"}, 12, 11)
	Finish(sink, call, nil)
	joined := strings.Join(sink.text["plays/rain/runtime.log"], "\n")
	if !strings.Contains(joined, "REPAIR") || !strings.Contains(joined, "trailing_comma") {
		t.Fatalf("runtime = %s", joined)
	}
}

func TestTranscriptWritesThinkingAndOutput(t *testing.T) {
	sink := newMemSink()
	tr := NewTranscript(sink, "rain")
	tr.Begin("play_planner", "c1")
	tr.Feed(agentcore.StreamEvent{Type: agentcore.StreamEventThinkingDelta, Delta: "先想"})
	tr.Feed(agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: `{"ok":true}`})
	got := sink.raw["plays/rain/stream.log"]
	if !strings.Contains(got, "play_planner") || !strings.Contains(got, "[thinking]\n先想") || !strings.Contains(got, "[output]\n{\"ok\":true}") {
		t.Fatalf("transcript = %q", got)
	}
}

func TestTranscriptFillsMissingThinking(t *testing.T) {
	sink := newMemSink()
	tr := NewTranscript(sink, "rain")
	tr.Begin("play_planner", "c1")
	tr.FillMissing(&agentcore.LLMResponse{Message: agentcore.Message{
		Content: []agentcore.ContentBlock{
			agentcore.ThinkingBlock(`{"segment_id":"meet"}`),
		},
	}}, true, true)
	got := sink.raw["plays/rain/stream.log"]
	if !strings.Contains(got, "[thinking]\n{\"segment_id\":\"meet\"}") {
		t.Fatalf("transcript = %q", got)
	}
}
