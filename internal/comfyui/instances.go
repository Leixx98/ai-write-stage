package comfyui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

type Instance struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	BaseURL         string     `json:"base_url"`
	Enabled         bool       `json:"enabled"`
	Priority        int        `json:"priority"`
	MaxConcurrency  int        `json:"max_concurrency"`
	SelectionWeight int        `json:"selection_weight"`
	Health          string     `json:"health"`
	LastCheckedAt   *time.Time `json:"last_checked_at,omitempty"`
	AuthRef         string     `json:"auth_ref,omitempty"`
	Tags            []string   `json:"tags,omitempty"`
}
type InstanceSettings struct {
	DefaultInstanceID   string   `json:"default_instance_id"`
	Strategy            string   `json:"strategy"`
	FallbackInstanceIDs []string `json:"fallback_instance_ids,omitempty"`
	HealthTTLMS         int      `json:"health_ttl_ms"`
	QueueProbe          bool     `json:"queue_probe"`
	StickyUnit          bool     `json:"sticky_unit"`
}
type Health struct {
	InstanceID  string    `json:"instance_id"`
	Status      string    `json:"status"`
	QueueLength int       `json:"queue_length"`
	Running     int       `json:"running"`
	CheckedAt   time.Time `json:"checked_at"`
	Error       string    `json:"error,omitempty"`
}
type SelectionHint struct {
	InstanceID         string
	WorkflowInstanceID string
	DefaultInstanceID  string
	Strategy           string
}

func DefaultInstance() Instance {
	return Instance{ID: "local-8188", Name: "Local ComfyUI", BaseURL: "http://127.0.0.1:8188", Enabled: true, Priority: 100, MaxConcurrency: 1, SelectionWeight: 1, Health: "unknown"}
}
func DefaultInstanceSettings() InstanceSettings {
	return InstanceSettings{DefaultInstanceID: "local-8188", Strategy: "least_queue", HealthTTLMS: 10000, QueueProbe: true, StickyUnit: true}
}
func NormalizeInstance(i Instance) Instance {
	d := DefaultInstance()
	if strings.TrimSpace(i.ID) == "" {
		i.ID = d.ID
	}
	if strings.TrimSpace(i.Name) == "" {
		i.Name = i.ID
	}
	if strings.TrimSpace(i.BaseURL) == "" {
		i.BaseURL = d.BaseURL
	}
	if i.Priority == 0 {
		i.Priority = d.Priority
	}
	if i.MaxConcurrency <= 0 {
		i.MaxConcurrency = d.MaxConcurrency
	}
	if i.SelectionWeight <= 0 {
		i.SelectionWeight = d.SelectionWeight
	}
	if i.Health == "" {
		i.Health = "unknown"
	}
	return i
}
func NormalizeInstanceSettings(s InstanceSettings) InstanceSettings {
	d := DefaultInstanceSettings()
	if s.DefaultInstanceID == "" {
		s.DefaultInstanceID = d.DefaultInstanceID
	}
	if s.Strategy == "" {
		s.Strategy = d.Strategy
	}
	if s.HealthTTLMS <= 0 {
		s.HealthTTLMS = d.HealthTTLMS
	}
	return s
}
func ValidateInstance(i Instance) error {
	if err := ValidateURL(i.BaseURL); err != nil {
		return err
	}
	if i.ID == "" || strings.ContainsAny(i.ID, "/\\") {
		return fmt.Errorf("invalid instance id")
	}
	return nil
}

// SelectLeastQueue deterministically selects a healthy enabled instance.
func SelectLeastQueue(instances []Instance, queues map[string]int, hint SelectionHint) (Instance, error) {
	if hint.InstanceID != "" {
		for _, i := range instances {
			if i.ID == hint.InstanceID && i.Enabled {
				return i, nil
			}
		}
		return Instance{}, fmt.Errorf("instance %q unavailable", hint.InstanceID)
	}
	if hint.WorkflowInstanceID != "" {
		for _, i := range instances {
			if i.ID == hint.WorkflowInstanceID && i.Enabled {
				return i, nil
			}
		}
	}
	if hint.DefaultInstanceID != "" {
		for _, i := range instances {
			if i.ID == hint.DefaultInstanceID && i.Enabled {
				return i, nil
			}
		}
	}
	eligible := make([]Instance, 0, len(instances))
	for _, i := range instances {
		if i.Enabled && strings.EqualFold(i.Health, "healthy") {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 {
		for _, i := range instances {
			if i.Enabled {
				eligible = append(eligible, i)
			}
		}
	}
	strategy := strings.ToLower(hint.Strategy)
	if strategy != "" && strategy != "least_queue" && strategy != "explicit" {
		sort.SliceStable(eligible, func(a, b int) bool {
			if eligible[a].Priority != eligible[b].Priority {
				return eligible[a].Priority > eligible[b].Priority
			}
			return eligible[a].ID < eligible[b].ID
		})
		return eligible[0], nil
	}
	if len(eligible) == 0 {
		return Instance{}, fmt.Errorf("no enabled ComfyUI instance")
	}
	sort.SliceStable(eligible, func(a, b int) bool {
		qa, qb := queues[eligible[a].ID], queues[eligible[b].ID]
		if qa != qb {
			return qa < qb
		}
		if eligible[a].Priority != eligible[b].Priority {
			return eligible[a].Priority > eligible[b].Priority
		}
		return eligible[a].ID < eligible[b].ID
	})
	return eligible[0], nil
}

func (i Instance) Client(cfg Config) (*HTTPClient, error) {
	cfg.BaseURL = i.BaseURL
	return NewHTTPClient(cfg)
}
func (i Instance) Test(ctx context.Context, cfg Config) (Health, error) {
	h := Health{InstanceID: i.ID, CheckedAt: time.Now().UTC()}
	cl, err := i.Client(cfg)
	if err != nil {
		h.Status = "invalid"
		h.Error = err.Error()
		return h, err
	}
	if err := cl.TestConnection(ctx); err != nil {
		h.Status = "unreachable"
		h.Error = err.Error()
		return h, err
	}
	h.Status = "healthy"
	return h, nil
}
