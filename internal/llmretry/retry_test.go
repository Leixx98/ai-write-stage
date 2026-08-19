package llmretry

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voocel/agentcore"
)

func TestRetryDelayCapsWithoutOverflow(t *testing.T) {
	if got := retryDelay(nil, 10_000); got != maxRetryDelay {
		t.Fatalf("retryDelay = %s, want %s", got, maxRetryDelay)
	}
	if got := retryDelay(nil, 3); got != 8*time.Second {
		t.Fatalf("retryDelay = %s, want 8s", got)
	}
}

type retryableStreamErr struct{}

func (retryableStreamErr) Error() string             { return "provider unavailable" }
func (retryableStreamErr) Retryable() bool           { return true }
func (retryableStreamErr) RetryAfter() time.Duration { return time.Millisecond }

type countingStreamModel struct {
	failTimes int
	calls     int
}

func (m *countingStreamModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.calls++
	if m.calls <= m.failTimes {
		return nil, retryableStreamErr{}
	}
	ch := make(chan agentcore.StreamEvent, 2)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: `{"ok":true}`}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock(`{"ok":true}`)},
	}}
	close(ch)
	return ch, nil
}

func TestGenerateStreamSucceedsAfterRetryable(t *testing.T) {
	model := &countingStreamModel{failTimes: 2}
	var retries int
	resp, err := GenerateStream(context.Background(), model, Config{
		OnRetry: func(Event) { retries++ },
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.TextContent() != `{"ok":true}` {
		t.Fatalf("resp = %q", resp.Message.TextContent())
	}
	if model.calls != 3 || retries != 2 {
		t.Fatalf("calls=%d retries=%d", model.calls, retries)
	}
}

func TestGenerateStreamStopsAfterMaxRetries(t *testing.T) {
	model := &countingStreamModel{failTimes: 100}
	var retries int
	_, err := GenerateStream(context.Background(), model, Config{
		OnRetry: func(Event) { retries++ },
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "已达 5 次上限") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, retryableStreamErr{}) && !strings.Contains(err.Error(), "provider unavailable") {
		t.Fatalf("should wrap last retryable err: %v", err)
	}
	if model.calls != StreamMaxRetries+1 || retries != StreamMaxRetries {
		t.Fatalf("calls=%d retries=%d want calls=%d retries=%d", model.calls, retries, StreamMaxRetries+1, StreamMaxRetries)
	}
}
