package runlog

import (
	"fmt"
	"sync"

	"github.com/voocel/agentcore"
)

const StreamFile = "stream.log"

type RawAppender interface {
	AppendRaw(rel string, data string) error
}

type Transcript struct {
	sink RawAppender
	rel  string
	mu   sync.Mutex
	kind string
}

func PlayFile(playID, name string) (string, bool) {
	return scopedRel(Record{Mode: ModePlay, PlayID: playID}, name)
}

func NewTranscript(sink Sink, playID string) *Transcript {
	raw, ok := sink.(RawAppender)
	if !ok {
		return nil
	}
	rel, ok := PlayFile(playID, StreamFile)
	if !ok {
		return nil
	}
	return &Transcript{sink: raw, rel: rel}
}

func (t *Transcript) Begin(step, callID string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.kind = ""
	t.mu.Unlock()
	t.write(fmt.Sprintf("\n----- %s %s -----\n", step, callID))
}

func (t *Transcript) Note(message string) {
	if t == nil || message == "" {
		return
	}
	t.mu.Lock()
	t.kind = ""
	t.mu.Unlock()
	t.write("\n----- " + message + " -----\n")
}

func (t *Transcript) Feed(ev agentcore.StreamEvent) {
	if t == nil || ev.Delta == "" {
		return
	}
	section := ""
	switch ev.Type {
	case agentcore.StreamEventThinkingDelta:
		section = "thinking"
	case agentcore.StreamEventTextDelta:
		section = "output"
	default:
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.kind != section {
		t.kind = section
		_ = t.sink.AppendRaw(t.rel, "\n["+section+"]\n")
	}
	_ = t.sink.AppendRaw(t.rel, ev.Delta)
}

func (t *Transcript) FillMissing(resp *agentcore.LLMResponse, needThinking, needText bool) {
	if t == nil || resp == nil {
		return
	}
	if needThinking {
		if text := resp.Message.ThinkingContent(); text != "" {
			t.write("\n[thinking]\n" + text)
		}
	}
	if needText {
		if text := resp.Message.TextContent(); text != "" {
			t.write("\n[output]\n" + text)
		}
	}
}

func (t *Transcript) write(data string) {
	if t == nil || data == "" {
		return
	}
	_ = t.sink.AppendRaw(t.rel, data)
}
