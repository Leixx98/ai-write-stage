package imagejob

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/comfyui"
)

// MergeCanvasRuntime projects the editable canvas fields into the transient
// workflow used for this run. The persisted API workflow is never mutated.
// Canvas fields are authoritative when present, including an empty list after
// a user un-exposes every input.
func MergeCanvasRuntime(wf *comfyui.Workflow, canvas comfyui.CanvasDocument, values map[string]any) error {
	if wf == nil {
		return fmt.Errorf("workflow is required")
	}
	bindings := make([]comfyui.Binding, 0, len(canvas.Fields))
	defaults := map[string]any{}
	owners := map[string][]string{}
	for _, f := range canvas.Fields {
		if !f.Exposed {
			continue
		}
		id := strings.TrimSpace(f.ID)
		nodeID := strings.TrimSpace(f.NodeID)
		input := strings.TrimSpace(f.Input)
		if id == "" {
			id = nodeID + "::" + input
		}
		if nodeID == "" || input == "" {
			return fmt.Errorf("canvas field %q requires node_id and input", id)
		}
		typ := f.ValueType
		if typ == "" {
			typ = "string"
		}
		bindings = append(bindings, comfyui.Binding{Key: id, NodeID: nodeID, Path: "inputs." + input, Type: typ})
		owners[input] = append(owners[input], id)
		if f.Default != nil {
			defaults[id] = f.Default
		}
	}
	for _, b := range bindings {
		if _, ok := values[b.Key]; ok {
			continue
		}
		input := strings.TrimPrefix(b.Path, "inputs.")
		if ids := owners[input]; len(ids) == 1 {
			if v, ok := values[input]; ok {
				values[b.Key] = v
				continue
			}
		}
		if ids := owners[input]; len(ids) > 1 {
			if _, ok := values[input]; ok {
				return fmt.Errorf("ambiguous canvas field alias %q", input)
			}
		}
		lower := strings.ToLower(input + " " + b.Key)
		aliases := []string{}
		if strings.Contains(lower, "negative") {
			aliases = append(aliases, "negative_prompt")
		} else if strings.Contains(lower, "prompt") || strings.Contains(lower, "text") {
			aliases = append(aliases, "positive_prompt", "prompt", "text")
		}
		for _, alias := range aliases {
			if v, ok := values[alias]; ok {
				values[b.Key] = v
				break
			}
		}
	}
	wf.Bindings = bindings
	wf.Defaults = defaults
	if wf.Config == nil {
		wf.Config = &comfyui.WorkflowConfig{Format: "ainovel_workflow_config_v1", Version: 1}
	}
	wf.Config.Bindings = bindings
	wf.Config.Defaults = defaults
	return nil
}
