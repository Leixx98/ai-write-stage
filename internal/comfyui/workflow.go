package comfyui

import (
	"fmt"
	"strconv"
	"strings"
)

// ValidateWorkflow checks the API format and every binding path before persistence.
func ValidateWorkflow(w Workflow) []string {
	var errs []string
	if strings.TrimSpace(w.ID) == "" {
		errs = append(errs, "id is required")
	}
	if len(w.Workflow) == 0 {
		errs = append(errs, "workflow must be a non-empty API JSON object")
	}
	for id, raw := range w.Workflow {
		node, ok := raw.(map[string]any)
		if !ok {
			errs = append(errs, fmt.Sprintf("node %s must be an object", id))
			continue
		}
		if strings.TrimSpace(fmt.Sprint(node["class_type"])) == "" {
			errs = append(errs, fmt.Sprintf("node %s missing class_type", id))
		}
		if _, ok := node["inputs"].(map[string]any); !ok {
			errs = append(errs, fmt.Sprintf("node %s inputs must be an object", id))
		}
	}
	for i, b := range w.Bindings {
		if strings.TrimSpace(b.Key) == "" || strings.TrimSpace(b.NodeID) == "" {
			errs = append(errs, fmt.Sprintf("binding %d requires key and node_id", i))
			continue
		}
		if _, ok := w.Workflow[b.NodeID]; !ok {
			errs = append(errs, fmt.Sprintf("binding %s references missing node %s", b.Key, b.NodeID))
		}
		if err := validatePath(b.Path); err != nil {
			errs = append(errs, fmt.Sprintf("binding %s: %v", b.Key, err))
		}
		if !validType(b.Type) {
			errs = append(errs, fmt.Sprintf("binding %s has unsupported type %q", b.Key, b.Type))
		}
	}
	if w.Output.NodeID != "" {
		if _, ok := w.Workflow[w.Output.NodeID]; !ok {
			errs = append(errs, "output references missing node")
		}
	}
	return errs
}

func validType(t string) bool {
	switch t {
	case "", "string", "integer", "number", "boolean":
		return true
	}
	return false
}

func validatePath(path string) error {
	if path == "" {
		return fmt.Errorf("path is required")
	}
	for _, p := range strings.Split(path, ".") {
		if p == "" {
			return fmt.Errorf("path contains an empty segment")
		}
		if _, err := strconv.Atoi(p); err == nil && strings.HasPrefix(p, "-") {
			return fmt.Errorf("negative array index is not allowed")
		}
		if strings.ContainsAny(p, "[]/\\") {
			return fmt.Errorf("invalid path segment %q", p)
		}
	}
	return nil
}

// ApplyBindings deep-copies the workflow and sets typed values at binding paths.
func ApplyBindings(w Workflow, values map[string]any) (map[string]any, error) {
	if errs := ValidateWorkflow(w); len(errs) > 0 {
		return nil, fmt.Errorf("workflow invalid: %s", strings.Join(errs, "; "))
	}
	cloned, err := cloneJSON(w.Workflow)
	if err != nil {
		return nil, err
	}
	root := cloned.(map[string]any)
	bindings := w.Bindings
	defaults := w.Defaults
	if w.Config != nil {
		if len(bindings) == 0 {
			bindings = w.Config.Bindings
		}
		if defaults == nil {
			defaults = w.Config.Defaults
		}
	}
	for _, b := range bindings {
		v, present := values[b.Key]
		if !present {
			v, present = defaults[b.Key]
		}
		if !present {
			if b.Required {
				return nil, fmt.Errorf("missing required binding %q", b.Key)
			}
			continue
		}
		converted, err := convertValue(v, b.Type)
		if err != nil {
			return nil, fmt.Errorf("binding %s: %w", b.Key, err)
		}
		node := root[b.NodeID].(map[string]any)
		if err := setPath(node, strings.Split(b.Path, "."), converted); err != nil {
			return nil, fmt.Errorf("binding %s: %w", b.Key, err)
		}
	}
	return root, nil
}

func convertValue(v any, typ string) (any, error) {
	switch typ {
	case "", "string":
		return fmt.Sprint(v), nil
	case "integer":
		switch n := v.(type) {
		case int:
			return n, nil
		case int64:
			return n, nil
		case float64:
			if n == float64(int64(n)) {
				return int64(n), nil
			}
		}
		return nil, fmt.Errorf("expected integer")
	case "number":
		switch n := v.(type) {
		case int:
			return float64(n), nil
		case int64:
			return float64(n), nil
		case float64:
			return n, nil
		}
		return nil, fmt.Errorf("expected number")
	case "boolean":
		if b, ok := v.(bool); ok {
			return b, nil
		}
		return nil, fmt.Errorf("expected boolean")
	default:
		return v, nil
	}
}

func setPath(cur map[string]any, parts []string, value any) error {
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}
	return setPathAny(cur, parts, value)
}

func setPathAny(cur any, parts []string, value any) error {
	if len(parts) == 0 {
		return fmt.Errorf("empty path")
	}
	part := parts[0]
	switch node := cur.(type) {
	case map[string]any:
		if len(parts) == 1 {
			node[part] = value
			return nil
		}
		next, ok := node[part]
		if !ok {
			next = map[string]any{}
			node[part] = next
		}
		return setPathAny(next, parts[1:], value)
	case []any:
		idx, err := strconv.Atoi(part)
		if err != nil || idx < 0 || idx >= len(node) {
			return fmt.Errorf("array index %q out of range", part)
		}
		if len(parts) == 1 {
			node[idx] = value
			return nil
		}
		return setPathAny(node[idx], parts[1:], value)
	default:
		return fmt.Errorf("path segment %q traverses a non-object", part)
	}
}
