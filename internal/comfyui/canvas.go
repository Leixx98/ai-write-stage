package comfyui

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CanvasDocument is the editable projection of a ComfyUI API workflow. It
// stores presentation state and exposed fields without changing the API JSON.
type CanvasDocument struct {
	Format           string         `json:"format"`
	Version          int            `json:"version"`
	ID               string         `json:"id"`
	WorkflowID       string         `json:"workflow_id"`
	Title            string         `json:"title,omitempty"`
	Nodes            []CanvasNode   `json:"nodes"`
	Edges            []CanvasEdge   `json:"edges"`
	Viewport         CanvasViewport `json:"viewport"`
	Fields           []CanvasField  `json:"fields"`
	PrompterPreset   string         `json:"prompter_preset,omitempty"`
	PrompterTemplate string         `json:"prompter_template,omitempty"`
	MiniTestCards    []MiniTestCard `json:"mini_test_cards"`
	UpdatedAt        time.Time      `json:"updated_at"`
}

type CanvasNode struct {
	ID              string         `json:"id"`
	Kind            string         `json:"kind"`
	SourceNodeID    string         `json:"source_node_id,omitempty"`
	ClassType       string         `json:"class_type,omitempty"`
	Label           string         `json:"label,omitempty"`
	X               float64        `json:"x"`
	Y               float64        `json:"y"`
	Width           float64        `json:"width,omitempty"`
	Height          float64        `json:"height,omitempty"`
	Collapsed       bool           `json:"collapsed,omitempty"`
	ExposedFieldIDs []string       `json:"exposed_field_ids,omitempty"`
	Locked          bool           `json:"locked,omitempty"`
	Metadata        map[string]any `json:"metadata,omitempty"`
}

type CanvasEdge struct {
	ID           string `json:"id"`
	Source       string `json:"source"`
	SourceHandle string `json:"source_handle,omitempty"`
	Target       string `json:"target"`
	TargetHandle string `json:"target_handle,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Label        string `json:"label,omitempty"`
}

type CanvasViewport struct {
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Scale    float64 `json:"scale"`
	MinScale float64 `json:"min_scale,omitempty"`
	MaxScale float64 `json:"max_scale,omitempty"`
}

type CanvasField struct {
	ID            string `json:"id"`
	NodeID        string `json:"node_id"`
	Input         string `json:"input"`
	Name          string `json:"name"`
	Control       string `json:"control"`
	ValueType     string `json:"value_type"`
	Default       any    `json:"default,omitempty"`
	Min           any    `json:"min,omitempty"`
	Max           any    `json:"max,omitempty"`
	Step          any    `json:"step,omitempty"`
	Options       []any  `json:"options,omitempty"`
	Required      bool   `json:"required"`
	RandomEnabled bool   `json:"random_enabled"`
	Exposed       bool   `json:"exposed"`
	Source        string `json:"source,omitempty"`
	Note          string `json:"note,omitempty"`
}

type MiniTestCard struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	X           float64        `json:"x"`
	Y           float64        `json:"y"`
	Text        string         `json:"text,omitempty"`
	MediaType   string         `json:"media_type,omitempty"`
	MediaRef    map[string]any `json:"media_ref,omitempty"`
	Value       any            `json:"value,omitempty"`
	WorkflowID  string         `json:"workflow_id,omitempty"`
	InstanceID  string         `json:"instance_id,omitempty"`
	JobID       string         `json:"job_id,omitempty"`
	OutputIndex int            `json:"output_index,omitempty"`
}

// DefaultCanvas creates a deterministic layout from an API workflow.
func DefaultCanvas(w Workflow) CanvasDocument {
	ids := make([]string, 0, len(w.Workflow))
	for id := range w.Workflow {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	d := CanvasDocument{Format: "ainovel_comfy_canvas_v1", Version: 1, ID: w.ID, WorkflowID: w.ID, Title: w.Name, Viewport: CanvasViewport{Scale: 1, MinScale: .2, MaxScale: 3}, Fields: []CanvasField{}, MiniTestCards: []MiniTestCard{}}
	for i, id := range ids {
		node, _ := w.Workflow[id].(map[string]any)
		class := fmt.Sprint(node["class_type"])
		d.Nodes = append(d.Nodes, CanvasNode{ID: "node-" + id, Kind: "workflow", SourceNodeID: id, ClassType: class, Label: class, X: float64((i%4)*250 + 40), Y: float64((i/4)*150 + 40), Width: 180, Height: 100})
	}
	NormalizeWorkflowConfig(&w)
	if w.Config != nil {
		for _, f := range w.Config.Fields {
			d.Fields = append(d.Fields, CanvasField{ID: f.ID, NodeID: f.NodeID, Input: f.Input, Name: f.Label, Control: f.Control, ValueType: f.ValueType, Default: f.Default, Min: f.Min, Max: f.Max, Step: f.Step, Options: f.Options, Required: f.Required, RandomEnabled: f.Randomizable, Exposed: true, Source: f.Source, Note: f.Note})
		}
	}
	for _, id := range ids {
		node, _ := w.Workflow[id].(map[string]any)
		inputs, _ := node["inputs"].(map[string]any)
		for key, raw := range inputs {
			ref, ok := raw.([]any)
			if !ok || len(ref) < 2 {
				continue
			}
			src := fmt.Sprint(ref[0])
			out := fmt.Sprint(ref[1])
			d.Edges = append(d.Edges, CanvasEdge{ID: "edge-" + src + "-" + id + "-" + key, Source: "node-" + src, SourceHandle: "output-" + out, Target: "node-" + id, TargetHandle: key, Kind: "workflow", Label: key})
		}
	}
	d.MiniTestCards = append(d.MiniTestCards, MiniTestCard{ID: "prompt_1", Kind: "prompt", X: 36, Y: 96}, MiniTestCard{ID: "comfy_1", Kind: "comfy", X: 330, Y: 150, WorkflowID: w.ID}, MiniTestCard{ID: "output_1", Kind: "output", X: 670, Y: 190})
	d.UpdatedAt = time.Now().UTC()
	return d
}

// ValidateCanvas checks references and bounded presentation values.
func ValidateCanvas(c CanvasDocument, w Workflow) []string {
	var errs []string
	if c.Format != "" && c.Format != "ainovel_comfy_canvas_v1" {
		errs = append(errs, "unsupported canvas format")
	}
	if c.WorkflowID != "" && c.WorkflowID != w.ID {
		errs = append(errs, "canvas workflow_id does not match workflow")
	}
	if c.Viewport.Scale == 0 {
		c.Viewport.Scale = 1
	}
	if c.Viewport.Scale < .2 || c.Viewport.Scale > 3 {
		errs = append(errs, "viewport scale must be between 0.2 and 3")
	}
	nodes := map[string]bool{}
	for _, n := range c.Nodes {
		if n.ID == "" || nodes[n.ID] {
			errs = append(errs, "canvas node id must be unique and non-empty")
		}
		nodes[n.ID] = true
		if n.Kind == "workflow" {
			if n.SourceNodeID == "" {
				errs = append(errs, "canvas workflow node requires source_node_id")
			} else if _, ok := w.Workflow[n.SourceNodeID]; !ok {
				errs = append(errs, "canvas workflow node references missing source node: "+n.SourceNodeID)
			}
		}
	}
	for _, e := range c.Edges {
		if !nodes[e.Source] || !nodes[e.Target] {
			errs = append(errs, "canvas edge references missing node")
		}
	}
	seenFields := map[string]bool{}
	for _, f := range c.Fields {
		if f.ID == "" || seenFields[f.ID] {
			errs = append(errs, "canvas field id must be unique and non-empty")
		}
		seenFields[f.ID] = true
		if _, ok := w.Workflow[f.NodeID]; !ok {
			errs = append(errs, "canvas field references missing node "+f.NodeID)
			continue
		}
		node, _ := w.Workflow[f.NodeID].(map[string]any)
		inputs, _ := node["inputs"].(map[string]any)
		if _, ok := inputs[f.Input]; !ok {
			errs = append(errs, "canvas field references missing input "+f.NodeID+"."+f.Input)
		}
	}
	for _, card := range c.MiniTestCards {
		switch card.Kind {
		case "prompt", "media", "comfy", "output", "note":
		default:
			errs = append(errs, "unsupported mini test card kind "+card.Kind)
		}
	}
	return errs
}

// CanvasValues converts test-card values into the logical field map consumed
// by ApplyBindings. Explicit field values take precedence over cards.
func CanvasValues(c CanvasDocument, fieldValues, miniValues map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range miniValues {
		out[k] = v
	}
	for _, card := range c.MiniTestCards {
		if card.Kind == "prompt" && card.Text != "" {
			out["text"] = card.Text
		}
		if card.Value != nil {
			out[card.ID] = card.Value
		}
	}
	for k, v := range fieldValues {
		out[k] = v
	}
	return out
}

func (c CanvasDocument) Normalize() CanvasDocument {
	if c.Format == "" {
		c.Format = "ainovel_comfy_canvas_v1"
	}
	if c.Version == 0 {
		c.Version = 1
	}
	if c.Viewport.Scale == 0 {
		c.Viewport.Scale = 1
	}
	if c.Viewport.MinScale == 0 {
		c.Viewport.MinScale = .2
	}
	if c.Viewport.MaxScale == 0 {
		c.Viewport.MaxScale = 3
	}
	if strings.TrimSpace(c.ID) == "" {
		c.ID = c.WorkflowID
	}
	return c
}
