package comfyadapter

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
)

// BuildPromptSchema projects ComfyUI canvas fields into the generic prompt
// validation contract consumed by the image gateway.
func BuildPromptSchema(workflowID string, canvas comfyui.CanvasDocument) (imagejob.PromptSchema, error) {
	fields := make([]imagejob.PromptField, 0, len(canvas.Fields))
	for _, field := range canvas.Fields {
		fields = append(fields, imagejob.PromptField{
			ID: field.ID, Name: field.Name, Note: field.Note, Control: field.Control,
			Source: field.Source, ValueType: field.ValueType, Options: field.Options,
			Default: field.Default, Min: field.Min, Max: field.Max, Exposed: field.Exposed,
		})
	}
	return imagejob.BuildPromptSchema(workflowID, fields, canvas.PrompterPreset, canvas.PrompterTemplate)
}

// MergeCanvasRuntime projects editable canvas fields into a transient ComfyUI
// workflow. The persisted API workflow is never mutated.
func MergeCanvasRuntime(wf *comfyui.Workflow, canvas comfyui.CanvasDocument, values map[string]any) error {
	if wf == nil {
		return fmt.Errorf("workflow is required")
	}
	bindings := make([]comfyui.Binding, 0, len(canvas.Fields))
	defaults := map[string]any{}
	owners := map[string][]string{}
	for _, field := range canvas.Fields {
		if !field.Exposed {
			continue
		}
		id := strings.TrimSpace(field.ID)
		nodeID := strings.TrimSpace(field.NodeID)
		input := strings.TrimSpace(field.Input)
		if id == "" {
			id = nodeID + "::" + input
		}
		if nodeID == "" || input == "" {
			return fmt.Errorf("canvas field %q requires node_id and input", id)
		}
		valueType := field.ValueType
		if valueType == "" {
			valueType = "string"
		}
		bindings = append(bindings, comfyui.Binding{Key: id, NodeID: nodeID, Path: "inputs." + input, Type: valueType})
		owners[input] = append(owners[input], id)
		if field.Default != nil {
			defaults[id] = field.Default
		}
	}
	for _, binding := range bindings {
		if _, ok := values[binding.Key]; ok {
			continue
		}
		input := strings.TrimPrefix(binding.Path, "inputs.")
		if ids := owners[input]; len(ids) == 1 {
			if value, ok := values[input]; ok {
				values[binding.Key] = value
				continue
			}
		} else if len(ids) > 1 {
			if _, ok := values[input]; ok {
				return fmt.Errorf("ambiguous canvas field alias %q", input)
			}
		}
		lower := strings.ToLower(input + " " + binding.Key)
		aliases := []string{}
		if strings.Contains(lower, "negative") {
			aliases = append(aliases, "negative_prompt")
		} else if strings.Contains(lower, "prompt") || strings.Contains(lower, "text") {
			aliases = append(aliases, "positive_prompt", "prompt", "text")
		}
		for _, alias := range aliases {
			if value, ok := values[alias]; ok {
				values[binding.Key] = value
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
