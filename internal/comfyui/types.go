package comfyui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Config controls the ComfyUI API client and is intentionally serializable.
type Config struct {
	Enabled          bool   `json:"enabled"`
	BaseURL          string `json:"base_url"`
	ClientID         string `json:"client_id"`
	TimeoutMS        int    `json:"timeout_ms"`
	PollIntervalMS   int    `json:"poll_interval_ms"`
	Strict           bool   `json:"strict"`
	MaxResponseBytes int64  `json:"max_response_bytes"`
	WorkflowID       string `json:"workflow_id"`
}

// DefaultConfig is the canonical local ComfyUI configuration. It is returned
// when a project has not created its config file yet.
func DefaultConfig() Config {
	return Config{
		Enabled:          true,
		BaseURL:          "http://127.0.0.1:8188",
		ClientID:         "ainovel-web",
		TimeoutMS:        600000,
		PollIntervalMS:   1000,
		Strict:           true,
		MaxResponseBytes: 50 << 20,
	}
}

// NormalizeConfig fills omitted scalar fields while preserving explicit values.
// Boolean fields intentionally remain unchanged because JSON cannot distinguish
// an omitted bool from an explicit false value once decoded into Config.
func NormalizeConfig(c Config) Config {
	d := DefaultConfig()
	if strings.TrimSpace(c.BaseURL) == "" {
		c.BaseURL = d.BaseURL
	}
	if strings.TrimSpace(c.ClientID) == "" {
		c.ClientID = d.ClientID
	}
	if c.TimeoutMS <= 0 {
		c.TimeoutMS = d.TimeoutMS
	}
	if c.PollIntervalMS <= 0 {
		c.PollIntervalMS = d.PollIntervalMS
	}
	if c.MaxResponseBytes <= 0 {
		c.MaxResponseBytes = d.MaxResponseBytes
	}
	return c
}

func (c Config) Timeout() time.Duration {
	if c.TimeoutMS <= 0 {
		return 10 * time.Minute
	}
	return time.Duration(c.TimeoutMS) * time.Millisecond
}
func (c Config) PollInterval() time.Duration {
	if c.PollIntervalMS <= 0 {
		return time.Second
	}
	return time.Duration(c.PollIntervalMS) * time.Millisecond
}

// Binding maps a logical parameter to a node input path.
type Binding struct {
	Key      string `json:"key"`
	NodeID   string `json:"node_id"`
	Path     string `json:"path"`
	Type     string `json:"type"`
	Required bool   `json:"required"`
}

type OutputSpec struct {
	NodeID string `json:"node_id"`
	Path   string `json:"path,omitempty"`
	Index  int    `json:"index"`
	MIME   string `json:"mime,omitempty"`
}

type Workflow struct {
	Format     string          `json:"format,omitempty"`
	ID         string          `json:"id"`
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Enabled    bool            `json:"enabled"`
	Workflow   map[string]any  `json:"workflow"`
	Bindings   []Binding       `json:"bindings,omitempty"`
	Defaults   map[string]any  `json:"defaults,omitempty"`
	Output     OutputSpec      `json:"output"`
	InstanceID string          `json:"instance_id,omitempty"`
	Config     *WorkflowConfig `json:"config,omitempty"`
	CreatedAt  time.Time       `json:"created_at,omitempty"`
	UpdatedAt  time.Time       `json:"updated_at,omitempty"`
}

type OutputRef struct {
	Filename  string
	Subfolder string
	Type      string
}

type DownloadedImage struct {
	Data        []byte
	ContentType string
	Ref         OutputRef
}
type MediaOutput struct {
	Kind        string `json:"kind"`
	NodeID      string `json:"node_id"`
	OutputKey   string `json:"output_key"`
	ClassType   string `json:"class_type"`
	MIME        string `json:"mime"`
	Previewable bool   `json:"previewable"`
	URL         string `json:"url,omitempty"`
	StorageKey  string `json:"storage_key,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type History struct {
	PromptID string
	Status   string
	Outputs  map[string]any
	Error    error
}

// ValidateURL rejects unsupported schemes, missing hosts, credentials and malformed ports.
func ValidateURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid ComfyUI URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}
	if u.User != nil {
		return fmt.Errorf("URL credentials are not allowed")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("URL query and fragment are not allowed")
	}
	if strings.ContainsAny(u.Host, "\r\n") {
		return fmt.Errorf("invalid URL host")
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return fmt.Errorf("invalid URL port")
		}
	}
	return nil
}

// Client is the minimal ComfyUI API contract used by image jobs and HTTP handlers.
type Client interface {
	TestConnection(context.Context) error
	Submit(context.Context, map[string]any, string) (string, error)
	Wait(context.Context, string, time.Duration) (History, error)
	Download(context.Context, OutputRef, int64) (DownloadedImage, error)
	Interrupt(context.Context) error
}

func cloneJSON(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
