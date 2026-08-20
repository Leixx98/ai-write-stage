package comfyui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPClient struct {
	BaseURL          string
	HTTP             *http.Client
	MaxResponseBytes int64
}

func (c *HTTPClient) Queue(ctx context.Context) (int, int, error) {
	data, _, err := c.request(ctx, http.MethodGet, "/queue", nil)
	if err != nil {
		return 0, 0, err
	}
	var q struct {
		QueueRunning []any `json:"queue_running"`
		QueuePending []any `json:"queue_pending"`
	}
	if err := json.Unmarshal(data, &q); err != nil {
		return 0, 0, fmt.Errorf("invalid /queue response: %w", err)
	}
	return len(q.QueuePending), len(q.QueueRunning), nil
}

func NewHTTPClient(cfg Config) (*HTTPClient, error) {
	if err := ValidateURL(cfg.BaseURL); err != nil {
		return nil, err
	}
	// Request cancellation and total timeout are supplied by the caller's context.
	client := &http.Client{}
	max := cfg.MaxResponseBytes
	if max <= 0 {
		max = 50 << 20
	}
	return &HTTPClient{BaseURL: strings.TrimRight(cfg.BaseURL, "/"), HTTP: client, MaxResponseBytes: max}, nil
}

func (c *HTTPClient) request(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		rd = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rd)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	max := c.MaxResponseBytes
	if max <= 0 {
		max = 50 << 20
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > max {
		return nil, resp.Header, fmt.Errorf("ComfyUI response exceeds %d bytes", max)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp.Header, fmt.Errorf("ComfyUI %s %s: %s", method, path, strings.TrimSpace(string(data)))
	}
	return data, resp.Header, nil
}

func (c *HTTPClient) TestConnection(ctx context.Context) error {
	_, _, err := c.request(ctx, http.MethodGet, "/system_stats", nil)
	return err
}

func (c *HTTPClient) Submit(ctx context.Context, workflow map[string]any, clientID string) (string, error) {
	data, _, err := c.request(ctx, http.MethodPost, "/prompt", map[string]any{"prompt": workflow, "client_id": clientID})
	if err != nil {
		return "", err
	}
	var out struct {
		PromptID   string `json:"prompt_id"`
		Error      any    `json:"error"`
		NodeErrors any    `json:"node_errors"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("invalid /prompt response: %w", err)
	}
	if out.PromptID == "" {
		return "", fmt.Errorf("ComfyUI did not return prompt_id: %v", out.Error)
	}
	return out.PromptID, nil
}

func (c *HTTPClient) Wait(ctx context.Context, promptID string, pollInterval time.Duration) (History, error) {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	t := time.NewTicker(pollInterval)
	defer t.Stop()
	for {
		h, done, err := c.history(ctx, promptID)
		if err != nil {
			return h, err
		}
		if done {
			return h, h.Error
		}
		select {
		case <-ctx.Done():
			return History{PromptID: promptID}, ctx.Err()
		case <-t.C:
		}
	}
}

// CheckHistory performs one history lookup. Callers that also consume
// WebSocket events can use it as the authoritative completion check.
func (c *HTTPClient) CheckHistory(ctx context.Context, promptID string) (History, bool, error) {
	return c.history(ctx, promptID)
}

func (c *HTTPClient) history(ctx context.Context, promptID string) (History, bool, error) {
	data, _, err := c.request(ctx, http.MethodGet, "/history/"+url.PathEscape(promptID), nil)
	h := History{PromptID: promptID}
	if err != nil {
		return h, false, err
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return h, false, fmt.Errorf("invalid /history response: %w", err)
	}
	raw := all[promptID]
	if raw == nil {
		return h, false, nil
	}
	var entry struct {
		Status struct {
			StatusStr string  `json:"status_str"`
			Completed bool    `json:"completed"`
			Messages  [][]any `json:"messages"`
		} `json:"status"`
		Outputs    map[string]any `json:"outputs"`
		Error      any            `json:"error"`
		NodeErrors map[string]any `json:"node_errors"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return h, false, err
	}
	h.Status = entry.Status.StatusStr
	h.Outputs = entry.Outputs
	messageError := ""
	for _, message := range entry.Status.Messages {
		if len(message) < 2 || fmt.Sprint(message[0]) != "execution_error" {
			continue
		}
		if detail, ok := message[1].(map[string]any); ok {
			messageError = fmt.Sprintf("node_id=%v node_type=%v exception=%s", detail["node_id"], detail["node_type"], truncate(fmt.Sprint(detail["exception_message"]), 2048))
		} else {
			messageError = fmt.Sprint(message[1])
		}
		break
	}
	if entry.Error != nil || len(entry.NodeErrors) > 0 || messageError != "" || strings.EqualFold(entry.Status.StatusStr, "error") || strings.EqualFold(entry.Status.StatusStr, "failed") {
		h.Error = fmt.Errorf("ComfyUI execution failed: node_errors=%v error=%v", entry.NodeErrors, entry.Error)
		if messageError != "" {
			h.Error = fmt.Errorf("ComfyUI execution failed: %s", messageError)
		}
		return h, true, nil
	}
	return h, entry.Status.Completed || len(entry.Outputs) > 0, nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func (c *HTTPClient) Download(ctx context.Context, ref OutputRef, maxBytes int64) (DownloadedImage, error) {
	if strings.TrimSpace(ref.Filename) == "" {
		return DownloadedImage{}, fmt.Errorf("empty output filename")
	}
	if strings.ContainsAny(ref.Filename, "/\\") || strings.Contains(ref.Filename, "..") || strings.ContainsAny(ref.Subfolder, "\\\r\n") || strings.HasPrefix(ref.Subfolder, "/") || strings.Contains(ref.Subfolder, "..") {
		return DownloadedImage{}, fmt.Errorf("invalid output path")
	}
	q := url.Values{}
	q.Set("filename", ref.Filename)
	q.Set("subfolder", ref.Subfolder)
	q.Set("type", ref.Type)
	data, headers, err := c.request(ctx, http.MethodGet, "/view?"+q.Encode(), nil)
	if err != nil {
		return DownloadedImage{}, err
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return DownloadedImage{}, fmt.Errorf("image exceeds %d bytes", maxBytes)
	}
	ct := headers.Get("Content-Type")
	if ct == "" {
		ct = mime.TypeByExtension(ref.Filename)
	}
	if !strings.HasPrefix(ct, "image/") {
		return DownloadedImage{}, fmt.Errorf("ComfyUI returned non-image content type %q", ct)
	}
	return DownloadedImage{Data: data, ContentType: ct, Ref: ref}, nil
}

func (c *HTTPClient) Interrupt(ctx context.Context) error {
	_, _, err := c.request(ctx, http.MethodPost, "/interrupt", map[string]any{})
	return err
}
