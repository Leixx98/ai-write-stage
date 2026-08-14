package comfyui

import (
	"fmt"
	"sort"
	"strings"
)

type FieldSchema struct {
	ID           string `json:"id"`
	NodeID       string `json:"node_id"`
	Input        string `json:"input"`
	Label        string `json:"label"`
	Control      string `json:"control"`
	ValueType    string `json:"value_type"`
	Default      any    `json:"default,omitempty"`
	Min          any    `json:"min,omitempty"`
	Max          any    `json:"max,omitempty"`
	Step         any    `json:"step,omitempty"`
	Options      []any  `json:"options,omitempty"`
	Required     bool   `json:"required"`
	Randomizable bool   `json:"randomizable"`
	Source       string `json:"source"`
	Editable     bool   `json:"editable"`
	Note         string `json:"note,omitempty"`
}
type WorkflowConfig struct {
	Format     string           `json:"format"`
	Version    int              `json:"version"`
	Fields     []FieldSchema    `json:"fields"`
	Bindings   []Binding        `json:"bindings"`
	Defaults   map[string]any   `json:"defaults"`
	Outputs    []OutputSelector `json:"outputs"`
	InstanceID string           `json:"instance_id,omitempty"`
}
type OutputSelector struct {
	NodeID    string `json:"node_id"`
	OutputKey string `json:"output_key"`
	Kind      string `json:"kind,omitempty"`
	Primary   bool   `json:"primary,omitempty"`
}

// NormalizeWorkflowConfig keeps the legacy single output selector and the
// newer output selector list interoperable.
func NormalizeWorkflowConfig(w *Workflow) {
	if w.Config == nil {
		return
	}
	if len(w.Config.Outputs) == 0 && w.Output.NodeID != "" {
		w.Config.Outputs = []OutputSelector{{NodeID: w.Output.NodeID, OutputKey: w.Output.Path, Kind: "image", Primary: true}}
	}
	if w.Output.NodeID == "" && len(w.Config.Outputs) > 0 {
		for _, o := range w.Config.Outputs {
			if o.Primary || w.Output.NodeID == "" {
				w.Output = OutputSpec{NodeID: o.NodeID, Path: o.OutputKey}
				if o.Primary {
					break
				}
			}
		}
	}
	if len(w.Bindings) == 0 {
		w.Bindings = w.Config.Bindings
	}
	if w.Defaults == nil {
		w.Defaults = w.Config.Defaults
	}
}

func InferBindings(api map[string]any) (WorkflowConfig, []string) {
	cfg := WorkflowConfig{Format: "ainovel_workflow_config_v1", Version: 1, Defaults: map[string]any{}}
	var warnings []string
	ids := make([]string, 0, len(api))
	for id := range api {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		raw := api[id]
		node, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		class := fmt.Sprint(node["class_type"])
		inputs, _ := node["inputs"].(map[string]any)
		keys := make([]string, 0, len(inputs))
		for key := range inputs {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			val := inputs[key]
			valueType, control := inferInputType(class, key, val)
			if valueType == "" {
				continue
			}
			field := FieldSchema{ID: id + "::" + key, NodeID: id, Input: key, Label: key, Control: control, ValueType: valueType, Default: val, Source: "inferred", Editable: true}
			cfg.Fields = append(cfg.Fields, field)
			cfg.Bindings = append(cfg.Bindings, Binding{Key: field.ID, NodeID: id, Path: "inputs." + key, Type: valueType, Required: false})
		}
	}
	return cfg, warnings
}
func inferInputType(class, key string, val any) (string, string) {
	switch key {
	case "text", "prompt":
		return "string", "textarea"
	case "seed", "steps", "width", "height":
		return "integer", "number"
	case "cfg":
		return "number", "number"
	case "sampler_name", "scheduler":
		return "string", "dropdown"
	case "image", "image_path":
		return "image", "image"
	}
	if strings.Contains(strings.ToLower(class), "textencode") && key == "text" {
		return "string", "textarea"
	}
	return "", ""
}

func ValidateWorkflowConfig(c WorkflowConfig, api map[string]any) []string {
	var errs []string
	if c.Format != "" && c.Format != "ainovel_workflow_config_v1" {
		errs = append(errs, "unsupported config format")
	}
	seen := map[string]bool{}
	for _, f := range c.Fields {
		if f.ID == "" || seen[f.ID] {
			errs = append(errs, "duplicate or empty field id")
			continue
		}
		seen[f.ID] = true
		if _, ok := api[f.NodeID]; !ok {
			errs = append(errs, "field references missing node "+f.NodeID)
		}
		if f.Min != nil && f.Max != nil {
			if min, ok := f.Min.(float64); ok {
				if max, ok := f.Max.(float64); ok && min > max {
					errs = append(errs, "field min exceeds max: "+f.ID)
				}
			}
		}
	}
	return errs
}
