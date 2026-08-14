package imagejob

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
)

const maxPromptResponseBytes = 1 << 20

type ValidationError struct {
	Field   string `json:"field,omitempty"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

type ParseResult struct {
	Valid      bool              `json:"valid"`
	SchemaHash string            `json:"schema_hash,omitempty"`
	Values     map[string]any    `json:"values,omitempty"`
	Warnings   []string          `json:"warnings"`
	Errors     []ValidationError `json:"errors,omitempty"`
}

type ParseError struct{ Errors []ValidationError }

func (e *ParseError) Error() string {
	if len(e.Errors) == 0 {
		return "image prompt JSON is invalid"
	}
	return e.Errors[0].Message
}

// ParseAndValidate accepts one JSON object (optionally fenced), rejects
// duplicate keys, and copies only schema-whitelisted values into the result.
func ParseAndValidate(raw string, promptSchema PromptSchema, strict bool) (ParseResult, error) {
	result := ParseResult{SchemaHash: promptSchema.SchemaHash, Values: map[string]any{}, Warnings: []string{}}
	raw = stripJSONFence(strings.TrimSpace(raw))
	if raw == "" {
		return invalidResult(result, ValidationError{Rule: "json", Message: "image prompt JSON is empty"})
	}
	if len(raw) > maxPromptResponseBytes {
		return invalidResult(result, ValidationError{Rule: "max_bytes", Message: "image prompt JSON exceeds 1 MiB"})
	}
	decoder := json.NewDecoder(io.LimitReader(strings.NewReader(raw), maxPromptResponseBytes+1))
	decoder.UseNumber()
	decoded, err := decodeUniqueJSON(decoder)
	if err != nil {
		return invalidResult(result, ValidationError{Rule: "json", Message: err.Error()})
	}
	if token, tokenErr := decoder.Token(); tokenErr != io.EOF {
		if tokenErr == nil {
			tokenErr = fmt.Errorf("unexpected trailing token %v", token)
		}
		return invalidResult(result, ValidationError{Rule: "trailing_token", Message: tokenErr.Error()})
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return invalidResult(result, ValidationError{Rule: "type", Message: "image prompt must be a JSON object"})
	}

	fields := make(map[string]fieldRule, len(promptSchema.Fields))
	for _, field := range promptSchema.Fields {
		fields[field.ID] = fieldRule{typ: field.ValueType, options: field.Options, min: field.Min, max: field.Max}
	}
	var validationErrors []ValidationError
	for key := range object {
		if _, allowed := fields[key]; allowed {
			continue
		}
		if strict {
			validationErrors = append(validationErrors, ValidationError{Field: key, Rule: "additionalProperties", Message: "unknown field " + key})
		} else {
			result.Warnings = append(result.Warnings, "ignored unknown field "+key)
		}
	}
	for _, field := range promptSchema.Fields {
		value, present := object[field.ID]
		if !present {
			if !strict && field.Default != nil {
				value, present = field.Default, true
				result.Warnings = append(result.Warnings, "field "+field.ID+" uses workflow default")
			}
			if !present {
				validationErrors = append(validationErrors, ValidationError{Field: field.ID, Rule: "required", Message: "missing field " + field.ID})
				continue
			}
		}
		converted, validationError := validateValue(field.ID, value, fields[field.ID])
		if validationError != nil {
			validationErrors = append(validationErrors, *validationError)
			continue
		}
		result.Values[field.ID] = converted
	}
	if len(validationErrors) > 0 {
		result.Errors = validationErrors
		return result, &ParseError{Errors: validationErrors}
	}
	result.Valid = true
	return result, nil
}

type fieldRule struct {
	typ     string
	options []any
	min     any
	max     any
}

func validateValue(field string, value any, rule fieldRule) (any, *ValidationError) {
	var converted any
	switch rule.typ {
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, typeError(field, "string")
		}
		converted = text
	case "boolean":
		boolean, ok := value.(bool)
		if !ok {
			return nil, typeError(field, "boolean")
		}
		converted = boolean
	case "integer":
		if number, ok := value.(json.Number); ok {
			integer, err := number.Int64()
			if err != nil {
				return nil, typeError(field, "integer")
			}
			converted = integer
		} else if integer, ok := integerValue(value); ok {
			converted = integer
		} else {
			return nil, typeError(field, "integer")
		}
	case "number":
		number, ok := finiteNumber(value)
		if !ok {
			return nil, typeError(field, "number")
		}
		converted = number
	default:
		return nil, &ValidationError{Field: field, Rule: "type", Message: "field " + field + " has unsupported type"}
	}
	if len(rule.options) > 0 && !containsJSONValue(rule.options, converted) {
		return nil, &ValidationError{Field: field, Rule: "enum", Message: "field " + field + " is not one of the allowed options"}
	}
	if number, ok := finiteNumber(converted); ok {
		if min, exists := finiteNumber(rule.min); exists && number < min {
			return nil, &ValidationError{Field: field, Rule: "minimum", Message: fmt.Sprintf("field %s is below %v", field, min)}
		}
		if max, exists := finiteNumber(rule.max); exists && number > max {
			return nil, &ValidationError{Field: field, Rule: "maximum", Message: fmt.Sprintf("field %s is above %v", field, max)}
		}
	}
	return converted, nil
}

func typeError(field, expected string) *ValidationError {
	return &ValidationError{Field: field, Rule: "type", Message: "field " + field + " must be " + expected}
}
func integerValue(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || value != math.Trunc(value) {
			return 0, false
		}
		return int64(value), true
	default:
		return 0, false
	}
}
func containsJSONValue(options []any, wanted any) bool {
	wantedJSON, _ := json.Marshal(wanted)
	for _, option := range options {
		optionJSON, _ := json.Marshal(option)
		if bytes.Equal(optionJSON, wantedJSON) {
			return true
		}
	}
	return false
}
func invalidResult(result ParseResult, validationError ValidationError) (ParseResult, error) {
	result.Errors = []ValidationError{validationError}
	return result, &ParseError{Errors: result.Errors}
}
func stripJSONFence(raw string) string {
	if !strings.HasPrefix(raw, "```") || !strings.HasSuffix(raw, "```") {
		return raw
	}
	firstLine := strings.IndexByte(raw, '\n')
	if firstLine < 0 {
		return raw
	}
	language := strings.TrimSpace(raw[3:firstLine])
	if language != "" && !strings.EqualFold(language, "json") {
		return raw
	}
	return strings.TrimSpace(raw[firstLine+1 : len(raw)-3])
}
func decodeUniqueJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("image prompt JSON parse failed: %w", err)
	}
	if delimiter, ok := token.(json.Delim); ok {
		switch delimiter {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("JSON object key must be a string")
				}
				if _, duplicate := object[key]; duplicate {
					return nil, fmt.Errorf("JSON contains duplicate key %q", key)
				}
				value, err := decodeUniqueJSON(decoder)
				if err != nil {
					return nil, err
				}
				object[key] = value
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return object, nil
		case '[':
			var array []any
			for decoder.More() {
				value, err := decodeUniqueJSON(decoder)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, err
			}
			return array, nil
		}
	}
	return token, nil
}
