package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/bootstrap"
	"github.com/voocel/ainovel-cli/internal/host"
)

func (c *v2Controller) settingsModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		envelope(w, 200, 0, c.rt.ModelConfiguration(), "")
		return
	}
	if r.Method != http.MethodPut {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		Action          string                  `json:"action"`
		Provider        string                  `json:"provider"`
		Type            string                  `json:"type"`
		API             string                  `json:"api"`
		BaseURL         string                  `json:"base_url"`
		Models          []bootstrap.ModelConfig `json:"models"`
		APIKeyAction    host.APIKeyAction       `json:"api_key_action"`
		APIKey          string                  `json:"api_key"`
		Model           string                  `json:"model"`
		Role            string                  `json:"role"`
		InheritDefault  bool                    `json:"inherit_default"`
		ReasoningEffort string                  `json:"reasoning_effort"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	draft := host.ModelConfigurationDraft{
		Provider: req.Provider, Type: req.Type, API: req.API, BaseURL: req.BaseURL,
		Models: req.Models, APIKeyAction: req.APIKeyAction, APIKey: req.APIKey,
	}
	var err error
	switch req.Action {
	case "save_provider":
		err = c.rt.ConfigureModels(draft)
	case "test_provider":
		err = c.rt.TestModelConnection(r.Context(), draft, req.Model)
	case "select_model":
		if req.InheritDefault {
			err = c.rt.InheritDefaultModel(req.Role)
		} else {
			err = c.rt.SwitchModel(req.Role, req.Provider, req.Model)
		}
	case "set_reasoning":
		err = c.rt.SetRoleThinking(req.Role, req.ReasoningEffort)
	default:
		err = fmt.Errorf("unknown model settings action %q", req.Action)
	}
	if err != nil {
		envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, err)
		return
	}
	envelope(w, 200, 0, c.rt.ModelConfiguration(), "")
}

func (c *v2Controller) settingsDocument(w http.ResponseWriter, r *http.Request, name string) {
	if name == "prompts" {
		c.promptPresets(w, r)
		return
	}
	if name == "workflow" {
		c.workflowSettings(w, r)
		return
	}
	if r.Method == http.MethodGet {
		var value map[string]any
		if err := c.loadSettings(name, &value); err != nil && !os.IsNotExist(err) {
			envelopeErr(w, 500, codeConfigInvalid, err)
			return
		}
		if value == nil {
			value = map[string]any{}
		}
		envelope(w, 200, 0, value, "")
		return
	}
	if r.Method != http.MethodPut {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var value map[string]any
	if err := decodeBody(r, &value); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	if err := c.saveSettings(name, value); err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	envelope(w, 200, 0, map[string]any{"saved": true}, "")
}

const (
	defaultPromptPreset = "默认配置"
	defaultRulesPreset  = "默认要求"
)

type promptPreset struct {
	Name      string            `json:"name"`
	Prompts   map[string]string `json:"prompts"`
	UpdatedAt string            `json:"updated_at,omitempty"`
}

type promptPresetDocument struct {
	Version      int                     `json:"version"`
	ActivePreset string                  `json:"active_preset"`
	Presets      map[string]promptPreset `json:"presets"`
	Prompts      map[string]string       `json:"prompts"`
}

type writingRulePreset struct {
	Name      string `json:"name"`
	Text      string `json:"text"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type workflowSettingsDocument struct {
	Version                  int                          `json:"version"`
	ActiveWritingRulesPreset string                       `json:"active_writing_rules_preset"`
	WritingRulePresets       map[string]writingRulePreset `json:"writing_rule_presets"`
	WritingRules             string                       `json:"writing_rules"`
	ReplanFrom               int                          `json:"replan_from"`
}

func defaultPromptValues() map[string]string {
	bundle := assets.Load("default", assets.LoadOptions{})
	return map[string]string{
		"architect":       bundle.Prompts.ArchitectShort,
		"chapter_planner": bundle.Prompts.ChapterPlanner,
		"writer":          bundle.Prompts.Writer,
		"editor":          bundle.Prompts.Editor,
		"prompter":        bundle.Prompts.Prompter,
	}
}

func defaultWritingRules() string {
	bundle := assets.Load("default", assets.LoadOptions{})
	parts := []string{strings.TrimSpace(bundle.Voice), strings.TrimSpace(bundle.Styles["default"]), "Follow the chapter plan for the current unit; preserve continuity; output prose only."}
	return strings.Join(parts, "\n\n")
}

func clonePrompts(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func mergePrompts(base, overlay map[string]string) map[string]string {
	out := clonePrompts(base)
	for k, v := range overlay {
		// Empty values are treated as missing configuration. This is important
		// for legacy prompts.json files that were initialized with empty role
		// values; they must not erase the embedded defaults on migration.
		if strings.TrimSpace(v) == "" {
			continue
		}
		out[k] = v
	}
	return out
}

func validPresetName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && len([]rune(name)) <= 64
}

func (c *v2Controller) loadPromptPresets() (promptPresetDocument, bool, error) {
	defaults := defaultPromptValues()
	var raw map[string]any
	err := c.loadSettings("prompts", &raw)
	if os.IsNotExist(err) {
		doc := promptPresetDocument{Version: 2, ActivePreset: defaultPromptPreset, Presets: map[string]promptPreset{defaultPromptPreset: {Name: defaultPromptPreset, Prompts: defaults}}, Prompts: clonePrompts(defaults)}
		return doc, true, nil
	}
	if err != nil {
		return promptPresetDocument{}, false, err
	}
	version, _ := raw["version"].(float64)
	if version < 2 {
		legacy := stringMap(raw["prompts"])
		prompts := mergePrompts(defaults, legacy)
		doc := promptPresetDocument{Version: 2, ActivePreset: defaultPromptPreset, Presets: map[string]promptPreset{defaultPromptPreset: {Name: defaultPromptPreset, Prompts: prompts}}, Prompts: clonePrompts(prompts)}
		// Persist the migrated document on the next GET so an old empty or
		// flat prompts.json is upgraded to the preset format immediately.
		return doc, true, nil
	}
	b, _ := json.Marshal(raw)
	var doc promptPresetDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, false, err
	}
	if doc.Presets == nil {
		doc.Presets = map[string]promptPreset{}
	}
	if _, ok := doc.Presets[defaultPromptPreset]; !ok {
		doc.Presets[defaultPromptPreset] = promptPreset{Name: defaultPromptPreset, Prompts: defaults}
	}
	if doc.ActivePreset == "" || doc.Presets[doc.ActivePreset].Name == "" {
		doc.ActivePreset = defaultPromptPreset
	}
	active := doc.Presets[doc.ActivePreset]
	active.Prompts = mergePrompts(defaults, active.Prompts)
	doc.Presets[doc.ActivePreset] = active
	doc.Prompts = clonePrompts(active.Prompts)
	return doc, false, nil
}

func stringMap(v any) map[string]string {
	out := map[string]string{}
	if m, ok := v.(map[string]any); ok {
		for k, value := range m {
			out[k] = fmt.Sprint(value)
		}
	}
	return out
}

func (c *v2Controller) promptPresets(w http.ResponseWriter, r *http.Request) {
	doc, created, err := c.loadPromptPresets()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	if r.Method == http.MethodGet {
		if created {
			_ = c.saveSettingsValue("prompts", doc)
		}
		envelope(w, 200, 0, doc, "")
		return
	}
	if r.Method != http.MethodPut {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req map[string]any
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	action := strings.TrimSpace(fmt.Sprint(req["action"]))
	if action == "<nil>" {
		action = ""
	}
	if action == "" {
		action = "save"
		req["name"] = doc.ActivePreset
		req["overwrite"] = true
	}
	name := strings.TrimSpace(fmt.Sprint(req["name"]))
	if !validPresetName(name) {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("preset name must be 1-64 characters"))
		return
	}
	switch action {
	case "activate":
		if _, ok := doc.Presets[name]; !ok {
			envelopeErr(w, 404, codeNotFound, fmt.Errorf("提示词组合不存在"))
			return
		}
	case "save", "save_as":
		_, exists := doc.Presets[name]
		overwrite, _ := req["overwrite"].(bool)
		if exists && !overwrite {
			envelopeErr(w, 409, codeConflict, fmt.Errorf("同名提示词组合已存在"))
			return
		}
		source := doc.ActivePreset
		if value := strings.TrimSpace(fmt.Sprint(req["source"])); value != "" && value != "<nil>" {
			source = value
		}
		base := defaultPromptValues()
		if preset, ok := doc.Presets[source]; ok {
			base = preset.Prompts
		}
		prompts := mergePrompts(base, stringMap(req["prompts"]))
		doc.Presets[name] = promptPreset{Name: name, Prompts: prompts, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	default:
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("unsupported prompt preset operation"))
		return
	}
	doc.Version = 2
	doc.ActivePreset = name
	doc.Prompts = clonePrompts(doc.Presets[name].Prompts)
	if err := c.saveSettingsValue("prompts", doc); err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	envelope(w, 200, 0, doc, "")
}

func (c *v2Controller) loadWorkflowSettings() (workflowSettingsDocument, bool, error) {
	var raw map[string]any
	err := c.loadSettings("workflow", &raw)
	if os.IsNotExist(err) {
		text := defaultWritingRules()
		return workflowSettingsDocument{Version: 2, ActiveWritingRulesPreset: defaultRulesPreset, WritingRulePresets: map[string]writingRulePreset{defaultRulesPreset: {Name: defaultRulesPreset, Text: text}}, WritingRules: text}, true, nil
	}
	if err != nil {
		return workflowSettingsDocument{}, false, err
	}
	b, _ := json.Marshal(raw)
	var doc workflowSettingsDocument
	if err := json.Unmarshal(b, &doc); err != nil {
		return doc, false, err
	}
	if doc.Version < 2 || doc.WritingRulePresets == nil {
		text := strings.TrimSpace(doc.WritingRules)
		if text == "" {
			text = defaultWritingRules()
		}
		doc.Version = 2
		doc.ActiveWritingRulesPreset = defaultRulesPreset
		doc.WritingRulePresets = map[string]writingRulePreset{defaultRulesPreset: {Name: defaultRulesPreset, Text: text}}
	}
	if _, ok := doc.WritingRulePresets[defaultRulesPreset]; !ok {
		doc.WritingRulePresets[defaultRulesPreset] = writingRulePreset{Name: defaultRulesPreset, Text: defaultWritingRules()}
	}
	if doc.ActiveWritingRulesPreset == "" || doc.WritingRulePresets[doc.ActiveWritingRulesPreset].Name == "" {
		doc.ActiveWritingRulesPreset = defaultRulesPreset
	}
	doc.WritingRules = doc.WritingRulePresets[doc.ActiveWritingRulesPreset].Text
	return doc, false, nil
}

func (c *v2Controller) workflowSettings(w http.ResponseWriter, r *http.Request) {
	doc, created, err := c.loadWorkflowSettings()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	if r.Method == http.MethodGet {
		if created {
			_ = c.saveSettingsValue("workflow", doc)
		}
		envelope(w, 200, 0, doc, "")
		return
	}
	if r.Method != http.MethodPut {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req map[string]any
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	if value, ok := req["replan_from"].(float64); ok {
		doc.ReplanFrom = int(value)
	}
	action := strings.TrimSpace(fmt.Sprint(req["action"]))
	if action == "<nil>" {
		action = ""
	}
	if action == "" {
		action = "save_writing_rules"
		req["name"] = doc.ActiveWritingRulesPreset
		req["text"] = req["writing_rules"]
		req["overwrite"] = true
	}
	name := strings.TrimSpace(fmt.Sprint(req["name"]))
	if !validPresetName(name) {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("preset name must be 1-64 characters"))
		return
	}
	switch action {
	case "activate_writing_rules":
		if _, ok := doc.WritingRulePresets[name]; !ok {
			envelopeErr(w, 404, codeNotFound, fmt.Errorf("writing requirement preset not found"))
			return
		}
	case "save_writing_rules", "save_writing_rules_as":
		_, exists := doc.WritingRulePresets[name]
		overwrite, _ := req["overwrite"].(bool)
		if exists && !overwrite {
			envelopeErr(w, 409, codeConflict, fmt.Errorf("writing requirement preset already exists"))
			return
		}
		text := fmt.Sprint(req["text"])
		if text == "<nil>" {
			text = ""
		}
		doc.WritingRulePresets[name] = writingRulePreset{Name: name, Text: text, UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	default:
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("不支持的写作要求预设操作"))
		return
	}
	doc.Version = 2
	doc.ActiveWritingRulesPreset = name
	doc.WritingRules = doc.WritingRulePresets[name].Text
	if err := c.saveSettingsValue("workflow", doc); err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	envelope(w, 200, 0, doc, "")
}

func (c *v2Controller) settingsPath(name string) string {
	return filepath.Join(c.rt.Dir(), "meta", "web", name+".json")
}
func (c *v2Controller) saveSettings(name string, value map[string]any) error {
	return c.saveSettingsValue(name, value)
}
func (c *v2Controller) saveSettingsValue(name string, value any) error {
	path := c.settingsPath(name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0600)
}
func (c *v2Controller) loadSettings(name string, value any) error {
	b, err := os.ReadFile(c.settingsPath(name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, value)
}
