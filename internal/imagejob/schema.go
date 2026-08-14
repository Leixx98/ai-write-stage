package imagejob

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/voocel/ainovel-cli/internal/comfyui"
)

const SchemaVersion = 1

var fieldIDPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

// PromptSchema is the deterministic contract sent to the image prompt model.
type PromptSchema struct {
	WorkflowID      string                `json:"workflow_id"`
	SchemaVersion   int                   `json:"schema_version"`
	SchemaHash      string                `json:"schema_hash"`
	Schema          map[string]any        `json:"schema"`
	Fields          []comfyui.CanvasField `json:"fields"`
	Template        string                `json:"template"`
	DefaultTemplate string                `json:"default_template"`
	Preset          string                `json:"preset"`
	Presets         []PrompterPreset      `json:"presets"`
	FieldJSON       map[string]any        `json:"field_json"`
	Composed        string                `json:"composed"`
}

// NormalizeFieldSource preserves legacy canvas data while making the runtime
// source decision explicit and deterministic.
func NormalizeFieldSource(field comfyui.CanvasField) string {
	source := strings.ToLower(strings.TrimSpace(field.Source))
	switch source {
	case "prompter", "default", "runtime":
		return source
	case "", "canvas", "inferred":
		control := strings.ToLower(strings.TrimSpace(field.Control))
		if normalizedValueType(field.ValueType) == "string" && (control == "text" || control == "textarea") {
			return "prompter"
		}
		return "default"
	default:
		return source
	}
}

// BuildPromptSchema projects every exposed configured field into JSON Schema.
func BuildPromptSchema(workflowID string, canvas comfyui.CanvasDocument) (PromptSchema, error) {
	fields := make([]comfyui.CanvasField, 0, len(canvas.Fields))
	seen := make(map[string]struct{}, len(canvas.Fields))
	for _, field := range canvas.Fields {
		if !field.Exposed {
			continue
		}
		field.ID = strings.TrimSpace(field.ID)
		if !fieldIDPattern.MatchString(field.ID) {
			return PromptSchema{}, fmt.Errorf("invalid field id %q", field.ID)
		}
		if _, exists := seen[field.ID]; exists {
			return PromptSchema{}, fmt.Errorf("duplicate field id %q", field.ID)
		}
		seen[field.ID] = struct{}{}
		field.Source = NormalizeFieldSource(field)
		field.ValueType = normalizedValueType(field.ValueType)
		if !supportedPromptType(field.ValueType) {
			continue
		}
		fields = append(fields, field)
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].ID < fields[j].ID })

	properties := make(map[string]any, len(fields))
	required := make([]string, 0, len(fields))
	for _, field := range fields {
		property := map[string]any{
			"type":        field.ValueType,
			"description": strings.TrimSpace(field.Name),
		}
		if property["description"] == "" {
			property["description"] = field.ID
		}
		if len(field.Options) > 0 {
			property["enum"] = field.Options
		}
		if min, ok := finiteNumber(field.Min); ok {
			property["minimum"] = min
		}
		if max, ok := finiteNumber(field.Max); ok {
			property["maximum"] = max
		}
		properties[field.ID] = property
		required = append(required, field.ID)
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  fmt.Sprintf("ainovel://comfyui/workflows/%s/image-prompt/v1", workflowID),
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
		"required":             required,
	}
	canonical, err := json.Marshal(schema)
	if err != nil {
		return PromptSchema{}, fmt.Errorf("marshal prompt schema: %w", err)
	}
	sum := sha256.Sum256(canonical)
	template := ResolvePrompterTemplate(canvas.PrompterPreset, canvas.PrompterTemplate)
	preset := NormalizePrompterPreset(canvas.PrompterPreset)
	return PromptSchema{
		WorkflowID: workflowID, SchemaVersion: SchemaVersion,
		SchemaHash: "sha256:" + hex.EncodeToString(sum[:]), Schema: schema, Fields: fields,
		Template: template, DefaultTemplate: DefaultPrompterTemplate,
		Preset: preset, Presets: PrompterPresets(),
		FieldJSON: FieldJSONObject(fields), Composed: ComposeSystemPrompt(template, fields),
	}, nil
}

func normalizedValueType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "string"
	}
	return value
}

func supportedPromptType(value string) bool {
	switch value {
	case "string", "integer", "number", "boolean":
		return true
	default:
		return false
	}
}

func finiteNumber(value any) (float64, bool) {
	var result float64
	switch value := value.(type) {
	case int:
		result = float64(value)
	case int64:
		result = float64(value)
	case float64:
		result = value
	case json.Number:
		parsed, err := value.Float64()
		if err != nil {
			return 0, false
		}
		result = parsed
	default:
		return 0, false
	}
	return result, !math.IsNaN(result) && !math.IsInf(result, 0)
}
