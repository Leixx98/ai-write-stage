package host

import (
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestReplayStreamEventSupportsStructuredAndLegacyPayloads(t *testing.T) {
	tests := []struct {
		name string
		item domain.RuntimeQueueItem
		want StreamEvent
	}{
		{
			name: "structured thinking",
			item: domain.RuntimeQueueItem{Kind: domain.RuntimeQueueStreamDelta, Payload: map[string]any{"kind": "thinking", "text": "reason"}},
			want: StreamEvent{Kind: StreamEventThinking, Text: "reason"},
		},
		{
			name: "structured tool",
			item: domain.RuntimeQueueItem{Kind: domain.RuntimeQueueStreamDelta, Payload: map[string]any{"kind": "tool", "tool": "规划"}},
			want: StreamEvent{Kind: StreamEventTool, Tool: "规划"},
		},
		{
			name: "legacy delta",
			item: domain.RuntimeQueueItem{Kind: domain.RuntimeQueueStreamDelta, Payload: map[string]any{"delta": "body"}},
			want: StreamEvent{Kind: StreamEventText, Text: "body"},
		},
		{
			name: "clear",
			item: domain.RuntimeQueueItem{Kind: domain.RuntimeQueueStreamClear},
			want: StreamEvent{Kind: StreamEventClear},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := ReplayStreamEvent(test.item)
			if !ok {
				t.Fatal("expected replay event")
			}
			if got != test.want {
				t.Fatalf("got %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestEmitStreamPersistsReplayableStructuredEvent(t *testing.T) {
	st := store.NewStore(t.TempDir())
	h := &Host{store: st, streamCh: make(chan StreamEvent, 1)}
	want := StreamEvent{Kind: StreamEventTool, Tool: "规划"}
	h.emitStream(want)
	items, err := st.Runtime.LoadQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d queue items", len(items))
	}
	got, ok := ReplayStreamEvent(items[0])
	if !ok || got != want {
		t.Fatalf("got %#v, ok=%v", got, ok)
	}
}
