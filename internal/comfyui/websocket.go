package comfyui

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// ProgressEvent is a normalized ComfyUI WebSocket execution event.
type ProgressEvent struct {
	PromptID string `json:"prompt_id"`
	Current  int    `json:"current"`
	Total    int    `json:"total"`
	Node     string `json:"node,omitempty"`
	Status   string `json:"status"`
	Error    string `json:"error,omitempty"`
}

// OpenProgress connects before a prompt is submitted so early execution events
// cannot be missed. The caller must submit with the same clientID.
func (c *HTTPClient) OpenProgress(ctx context.Context, clientID string) (<-chan ProgressEvent, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base URL: %w", err)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws"
	query := u.Query()
	query.Set("clientId", clientID)
	u.RawQuery = query.Encode()

	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, resp, err := websocket.Dial(dialCtx, u.String(), &websocket.DialOptions{HTTPClient: c.HTTP})
	cancel()
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("dial ComfyUI WebSocket: %s: %w", resp.Status, err)
		}
		return nil, fmt.Errorf("dial ComfyUI WebSocket: %w", err)
	}
	limit := c.MaxResponseBytes
	if limit <= 0 {
		limit = 50 << 20
	}
	conn.SetReadLimit(limit)

	events := make(chan ProgressEvent, 64)
	go func() {
		defer close(events)
		defer func() { _ = conn.CloseNow() }()
		for {
			messageType, payload, readErr := conn.Read(ctx)
			if readErr != nil {
				if ctx.Err() == nil {
					slog.Debug("comfyui: WebSocket read stopped", "err", readErr)
				}
				return
			}
			if messageType != websocket.MessageText {
				continue
			}
			event, ok := parseProgressEvent(payload)
			if !ok {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
			if terminalProgressEvent(event.Status) {
				return
			}
		}
	}()
	return events, nil
}

func parseProgressEvent(payload []byte) (ProgressEvent, bool) {
	var raw struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil || raw.Type == "" {
		return ProgressEvent{}, false
	}
	var common struct {
		PromptID string `json:"prompt_id"`
		Node     string `json:"node"`
	}
	if err := json.Unmarshal(raw.Data, &common); err != nil || common.PromptID == "" {
		return ProgressEvent{}, false
	}
	event := ProgressEvent{PromptID: common.PromptID, Node: common.Node, Status: raw.Type}
	switch raw.Type {
	case "progress":
		var progress struct {
			Value int `json:"value"`
			Max   int `json:"max"`
		}
		if err := json.Unmarshal(raw.Data, &progress); err != nil {
			return ProgressEvent{}, false
		}
		event.Current = progress.Value
		event.Total = progress.Max
	case "execution_error":
		var failure struct {
			ExceptionMessage string `json:"exception_message"`
		}
		if json.Unmarshal(raw.Data, &failure) == nil {
			event.Error = failure.ExceptionMessage
		}
	}
	return event, true
}

func terminalProgressEvent(status string) bool {
	switch status {
	case "execution_success", "execution_error", "execution_interrupted":
		return true
	default:
		return false
	}
}
