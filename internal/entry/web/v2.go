package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/voocel/ainovel-cli/assets"
	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/entry/startup"
	"github.com/voocel/ainovel-cli/internal/host"
	"github.com/voocel/ainovel-cli/internal/host/imp"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	codeInvalidRequest  = 1001
	codeNotFound        = 1002
	codeConflict        = 1003
	codeConfigInvalid   = 2001
	codeUnreachable     = 3001
	codeWorkflowInvalid = 3002
	codeJobFailed       = 3003
	codeJobTimeout      = 3004
	codeJobCancelled    = 3005
	codePromptSchema    = 3101
	codePrompterJSON    = 3102
	codePrompterCall    = 3103
	codePrompterTimeout = 3104
	codeUnitJobConflict = 3105
)

type apiEnvelope struct {
	Code int    `json:"code"`
	Data any    `json:"data"`
	Msg  string `json:"msg"`
}

func envelope(w http.ResponseWriter, status, code int, data any, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(apiEnvelope{code, data, msg})
}
func envelopeErr(w http.ResponseWriter, status, code int, err error) {
	envelope(w, status, code, map[string]any{}, err.Error())
}

func comfyBaseURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath()
}
func comfyErrorData(cfg comfyui.Config, phase string, retryable bool) map[string]any {
	return map[string]any{"phase": phase, "retryable": retryable, "base_url": comfyBaseURL(cfg.BaseURL)}
}
func comfyError(w http.ResponseWriter, status, code int, msg string, cfg comfyui.Config, phase string, retryable bool) {
	envelope(w, status, code, comfyErrorData(cfg, phase, retryable), msg)
}

type v2Controller struct {
	rt      *host.Host
	st      *store.Store
	mu      sync.Mutex
	running map[string]context.CancelFunc
}

func newV2Controller(rt *host.Host) *v2Controller {
	c := &v2Controller{rt: rt, st: store.NewStore(rt.Dir()), running: map[string]context.CancelFunc{}}
	go c.watchCompletedUnits()
	return c
}

// watchCompletedUnits bridges completed writing units without coupling the
// writing tools to the web controller. It is intentionally conservative:
// existing jobs are left for the normal retry controls, and the watcher stops
// with the host runtime.
func (c *v2Controller) watchCompletedUnits() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.rt.Done():
			return
		case <-ticker.C:
			bridge, err := c.st.ComfyUI.LoadBridgeConfig()
			if err != nil || !bridge.Enabled || !bridge.AutoGenerate {
				continue
			}
			outline, err := c.st.Outline.LoadOutline()
			if err != nil {
				continue
			}
			jobs, err := c.st.ComfyUI.ListJobs()
			if err != nil {
				continue
			}
			for _, chapter := range outline {
				progress, progressErr := c.st.Drafts.LoadWritingProgress(chapter.Chapter)
				if progressErr != nil || progress == nil {
					continue
				}
				for ordinal := 1; ordinal <= progress.CompletedUnits; ordinal++ {
					found := false
					for _, job := range jobs {
						if job.Chapter == chapter.Chapter && job.Ordinal == ordinal {
							found = true
							break
						}
					}
					if found {
						continue
					}
					r := httptest.NewRequest(http.MethodPost, "/api/v2/units/image/generate", strings.NewReader(`{"workflow_id":""}`))
					recorder := httptest.NewRecorder()
					c.generateUnitImage(recorder, r, chapter.Chapter, ordinal)
				}
			}
		}
	}
}

func registerV2(mux *http.ServeMux, rt *host.Host) {
	c := newV2Controller(rt)
	mux.HandleFunc("/api/v2/", c.dispatch)
}

func (c *v2Controller) dispatch(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/api/v2/")
	switch {
	case p == "comfyui/config":
		c.config(w, r)
	case p == "comfyui/test-connection":
		c.testConnection(w, r)
	case p == "comfyui/bridge":
		c.bridgeConfig(w, r)
	case p == "comfyui/prompter/parse" && r.Method == http.MethodPost:
		c.parsePrompterJSON(w, r)
	case p == "comfyui/instances" && r.Method == http.MethodGet:
		c.instances(w, r)
	case p == "comfyui/instances" && r.Method == http.MethodPut:
		c.saveInstances(w, r)
	case strings.HasPrefix(p, "comfyui/instances/") && strings.HasSuffix(p, "/test"):
		c.testInstance(w, r, strings.TrimSuffix(strings.TrimPrefix(p, "comfyui/instances/"), "/test"))
	case p == "comfyui/media/upload" && r.Method == http.MethodPost:
		c.uploadMedia(w, r)
	case strings.HasPrefix(p, "comfyui/media/"):
		c.getMedia(w, r, strings.TrimPrefix(p, "comfyui/media/"))
	case p == "comfyui/workflows" && r.Method == http.MethodGet:
		c.listWorkflows(w, r)
	case p == "comfyui/workflows/import" && r.Method == http.MethodPost:
		c.importWorkflow(w, r)
	case strings.HasPrefix(p, "comfyui/workflows/"):
		c.workflow(w, r, strings.TrimPrefix(p, "comfyui/workflows/"))
	case p == "comfyui/jobs/test" && r.Method == http.MethodPost:
		c.testJob(w, r)
	case strings.Contains(p, "/outputs/"):
		c.jobOutput(w, r, strings.TrimPrefix(strings.TrimPrefix(p, "comfyui/jobs/"), ""))
	case strings.HasPrefix(p, "comfyui/jobs/"):
		c.job(w, r, strings.TrimPrefix(p, "comfyui/jobs/"))
	case strings.HasPrefix(p, "units/"):
		c.unit(w, r, strings.TrimPrefix(p, "units/"))
	case p == "state" && r.Method == http.MethodGet:
		envelope(w, http.StatusOK, 0, map[string]any{"snapshot": c.rt.Snapshot()}, "")
	case p == "replay" && r.Method == http.MethodGet:
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		items, err := c.rt.ReplayQueue(after)
		if err != nil {
			envelopeErr(w, 500, codeConflict, err)
			return
		}
		envelope(w, http.StatusOK, 0, items, "")
	case p == "settings/models":
		c.settingsModels(w, r)
	case p == "settings/workflow":
		c.settingsDocument(w, r, "workflow")
	case p == "settings/prompts":
		c.settingsDocument(w, r, "prompts")
	case strings.HasPrefix(p, "commands/"):
		c.command(w, r, strings.TrimPrefix(p, "commands/"))
	default:
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("API route not found"))
	}
}

func (c *v2Controller) settingsModels(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		envelope(w, 200, 0, c.rt.ModelConfiguration(), "")
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
	if err := c.saveSettings("models", value); err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	envelope(w, 200, 0, map[string]any{"saved": true}, "")
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
	ImportSource             string                       `json:"import_source"`
	ImitateReference         string                       `json:"imitate_reference"`
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
	if value, ok := req["import_source"]; ok {
		doc.ImportSource = fmt.Sprint(value)
	}
	if value, ok := req["imitate_reference"]; ok {
		doc.ImitateReference = fmt.Sprint(value)
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

func (c *v2Controller) command(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		Prompt      string `json:"prompt"`
		Text        string `json:"text"`
		Source      string `json:"source"`
		Reference   string `json:"reference"`
		Preferences string `json:"preferences"`
	}
	if r.Body != nil {
		_ = decodeBody(r, &req)
	}
	var err error
	switch name {
	case "start":
		plan, e := startup.PrepareQuick(startup.Request{Mode: startup.ModeQuick, UserPrompt: req.Prompt, OutputDir: c.rt.Dir(), Interactive: true})
		if e == nil {
			e = c.rt.PrepareUserRules(plan.RawPrompt)
		}
		if e == nil {
			e = c.rt.StartPrepared(plan.RawPrompt)
		}
		err = e
	case "continue":
		if strings.TrimSpace(req.Text) == "" {
			_, err = c.rt.Resume()
		} else {
			err = c.rt.Continue(req.Text)
		}
	case "steer":
		err = c.rt.Steer(req.Text)
	case "import":
		source := strings.TrimSpace(req.Source)
		if source == "" {
			err = fmt.Errorf("import source is required")
			break
		}
		err = c.rt.StartImport(imp.Options{SourcePath: source, AutoConfirm: true, ContinueAfter: true})
	case "imitate":
		reference := strings.TrimSpace(req.Reference)
		if reference == "" {
			err = fmt.Errorf("imitation reference is required")
			break
		}
		if info, statErr := os.Stat(reference); statErr == nil && !info.IsDir() {
			reference = filepath.Dir(reference)
		}
		err = c.rt.StartSimulation(reference)
	case "writing-rules":
		preferences := req.Preferences
		if strings.TrimSpace(preferences) == "" {
			preferences = req.Text
		}
		err = c.rt.ApplyWritingRules(preferences)
	case "abort", "pause":
		c.rt.Abort()
	default:
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("command %q not found", name))
		return
	}
	if err != nil {
		envelopeErr(w, 409, codeConflict, err)
		return
	}
	envelope(w, 202, 0, map[string]any{"accepted": true}, "")
}

func (c *v2Controller) bridgeConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config, err := c.st.ComfyUI.LoadBridgeConfig()
		if err != nil {
			envelopeErr(w, 500, codeConfigInvalid, fmt.Errorf("图片生成桥接配置无法读取"))
			return
		}
		envelope(w, 200, 0, config, "")
	case http.MethodPut:
		var config imagejob.BridgeConfig
		if err := decodeBody(r, &config); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		config = imagejob.NormalizeBridgeConfig(config)
		if err := imagejob.ValidateBridgeConfig(config); err != nil {
			envelopeErr(w, 422, codePromptSchema, err)
			return
		}
		if !config.Enabled {
			if err := c.st.ComfyUI.SaveBridgeConfig(config); err != nil {
				envelopeErr(w, 500, codeConfigInvalid, err)
				return
			}
			envelope(w, 200, 0, config, "")
			return
		}
		workflow, err := c.st.ComfyUI.LoadWorkflow(config.WorkflowID)
		if err != nil {
			envelopeErr(w, 422, codePromptSchema, fmt.Errorf("workflow_id must reference a saved workflow"))
			return
		}
		canvas, err := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(workflow.ID)
		if err != nil {
			envelopeErr(w, 422, codePromptSchema, err)
			return
		}
		schema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
		if err != nil {
			envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
			return
		}
		if len(schema.Fields) == 0 {
			envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, "请先在画布中勾选要发给提示词模型的字段")
			return
		}
		if err := c.st.ComfyUI.SaveBridgeConfig(config); err != nil {
			envelopeErr(w, 500, codeConfigInvalid, fmt.Errorf("图片生成桥接配置无法保存"))
			return
		}
		envelope(w, 200, 0, config, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) loadPromptSchema(workflowID string) (imagejob.PromptSchema, error) {
	workflow, err := c.st.ComfyUI.LoadWorkflow(workflowID)
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	canvas, err := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	return imagejob.BuildPromptSchema(workflow.ID, canvas)
}

func (c *v2Controller) parsePrompterJSON(w http.ResponseWriter, r *http.Request) {
	var request struct {
		WorkflowID string `json:"workflow_id"`
		Raw        string `json:"raw"`
		Strict     *bool  `json:"strict"`
	}
	if err := decodeBody(r, &request); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	promptSchema, err := c.loadPromptSchema(request.WorkflowID)
	if err != nil {
		envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
		return
	}
	strict := true
	if bridge, bridgeErr := c.st.ComfyUI.LoadBridgeConfig(); bridgeErr == nil {
		strict = bridge.Strict
	}
	if request.Strict != nil {
		strict = *request.Strict
	}
	result, err := imagejob.ParseAndValidate(request.Raw, promptSchema, strict)
	if err != nil {
		envelope(w, 422, codePrompterJSON, map[string]any{
			"valid": false, "stage": "validating", "errors": result.Errors,
			"schema_hash": result.SchemaHash, "warnings": result.Warnings,
		}, "图片提示词 JSON 校验失败")
		return
	}
	envelope(w, 200, 0, result, "")
}

func (c *v2Controller) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := c.st.ComfyUI.LoadConfig()
		if err != nil {
			comfyError(w, 500, codeConfigInvalid, "ComfyUI configuration is unreadable", comfyui.DefaultConfig(), "config_load", false)
			return
		}
		cfg = comfyui.NormalizeConfig(cfg)
		if err := comfyui.ValidateURL(cfg.BaseURL); err != nil {
			comfyError(w, 500, codeConfigInvalid, "ComfyUI configuration has an invalid base URL", cfg, "config_validate", false)
			return
		}
		envelope(w, 200, 0, cfg, "")
	case http.MethodPut:
		var cfg comfyui.Config
		if err := decodeBody(r, &cfg); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		cfg = comfyui.NormalizeConfig(cfg)
		if err := comfyui.ValidateURL(cfg.BaseURL); err != nil {
			comfyError(w, 400, codeConfigInvalid, "ComfyUI base URL is invalid", cfg, "config_validate", false)
			return
		}
		if err := c.st.ComfyUI.SaveConfig(cfg); err != nil {
			comfyError(w, 500, codeConfigInvalid, "ComfyUI configuration could not be saved", cfg, "config_save", false)
			return
		}
		envelope(w, 200, 0, cfg, "")
	default:
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}
func (c *v2Controller) testConnection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	cfg, draft, err := c.connectionConfigDraft(r)
	if err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	if !draft {
		cfg, err = c.st.ComfyUI.LoadConfig()
		if err != nil {
			comfyError(w, 500, codeConfigInvalid, "ComfyUI configuration is unreadable", comfyui.DefaultConfig(), "config_load", false)
			return
		}
	}
	cfg = comfyui.NormalizeConfig(cfg)
	if err := comfyui.ValidateURL(cfg.BaseURL); err != nil {
		comfyError(w, 400, codeConfigInvalid, "ComfyUI base URL is invalid", cfg, "config_validate", false)
		return
	}
	cl, err := comfyui.NewHTTPClient(cfg)
	if err != nil {
		comfyError(w, 400, codeConfigInvalid, "ComfyUI base URL is invalid", cfg, "config_validate", false)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeout())
	defer cancel()
	if err := cl.TestConnection(ctx); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("comfyui connection test timed out", "phase", "connect", "base_url", comfyBaseURL(cfg.BaseURL), "draft", draft)
			comfyError(w, 504, codeJobTimeout, "ComfyUI connection timed out", cfg, "connect", true)
		} else {
			slog.Warn("comfyui connection test failed", "phase", "connect", "base_url", comfyBaseURL(cfg.BaseURL), "draft", draft)
			comfyError(w, 502, codeUnreachable, "ComfyUI is unreachable", cfg, "connect", true)
		}
		return
	}
	slog.Info("comfyui connection test succeeded", "phase", "connect", "base_url", comfyBaseURL(cfg.BaseURL), "draft", draft)
	envelope(w, 200, 0, map[string]any{
		"connected":  true,
		"base_url":   comfyBaseURL(cfg.BaseURL),
		"checked_at": time.Now().UTC(),
	}, "")
}

func (c *v2Controller) instances(w http.ResponseWriter, r *http.Request) {
	items, settings, err := c.st.ComfyUI.LoadInstances()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	data := map[string]any{"instances": items, "settings": settings, "default_instance_id": settings.DefaultInstanceID, "strategy": settings.Strategy, "fallback_instance_ids": settings.FallbackInstanceIDs, "health_ttl_ms": settings.HealthTTLMS, "queue_probe": settings.QueueProbe, "sticky_unit": settings.StickyUnit}
	envelope(w, 200, 0, data, "")
}
func (c *v2Controller) saveInstances(w http.ResponseWriter, r *http.Request) {
	var doc struct {
		Instances           []comfyui.Instance        `json:"instances"`
		Settings            *comfyui.InstanceSettings `json:"settings"`
		DefaultInstanceID   string                    `json:"default_instance_id"`
		Strategy            string                    `json:"strategy"`
		FallbackInstanceIDs []string                  `json:"fallback_instance_ids"`
		HealthTTLMS         int                       `json:"health_ttl_ms"`
		QueueProbe          *bool                     `json:"queue_probe"`
		StickyUnit          *bool                     `json:"sticky_unit"`
	}
	if err := decodeBody(r, &doc); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	settings := comfyui.DefaultInstanceSettings()
	if _, loaded, e := c.st.ComfyUI.LoadInstances(); e == nil {
		settings = loaded
	}
	if doc.Settings != nil {
		settings = *doc.Settings
	}
	if !settings.QueueProbe {
		settings.QueueProbe = true
	}
	if !settings.StickyUnit {
		settings.StickyUnit = true
	}
	if doc.DefaultInstanceID != "" {
		settings.DefaultInstanceID = doc.DefaultInstanceID
	}
	if doc.Strategy != "" {
		settings.Strategy = doc.Strategy
	}
	if doc.FallbackInstanceIDs != nil {
		settings.FallbackInstanceIDs = doc.FallbackInstanceIDs
	}
	if doc.HealthTTLMS > 0 {
		settings.HealthTTLMS = doc.HealthTTLMS
	}
	if doc.QueueProbe != nil {
		settings.QueueProbe = *doc.QueueProbe
	}
	if doc.StickyUnit != nil {
		settings.StickyUnit = *doc.StickyUnit
	}
	if err := c.st.ComfyUI.SaveInstances(doc.Instances, settings); err != nil {
		envelopeErr(w, 400, codeConfigInvalid, err)
		return
	}
	doc.Settings = &settings
	envelope(w, 200, 0, doc, "")
}
func (c *v2Controller) testInstance(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	items, _, err := c.st.ComfyUI.LoadInstances()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	var inst comfyui.Instance
	for _, i := range items {
		if i.ID == id {
			inst = i
		}
	}
	if inst.ID == "" {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("instance not found"))
		return
	}
	cfg, _ := c.st.ComfyUI.LoadConfig()
	ctx, cancel := context.WithTimeout(r.Context(), cfg.Timeout())
	defer cancel()
	h, e := inst.Test(ctx, cfg)
	if e != nil {
		code := codeUnreachable
		status := 502
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = codeJobTimeout
			status = 504
		}
		comfyError(w, status, code, "ComfyUI instance test failed", cfg, "connect", true)
		return
	}
	envelope(w, 200, 0, h, "")
}

// connectionConfigDraft accepts a draft without persisting it. An empty body
// is deliberately treated as a request to use the saved project config.
func (c *v2Controller) connectionConfigDraft(r *http.Request) (comfyui.Config, bool, error) {
	if r.Body == nil {
		return comfyui.Config{}, false, nil
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return comfyui.Config{}, false, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return comfyui.Config{}, false, nil
	}
	var cfg comfyui.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return comfyui.Config{}, true, fmt.Errorf("invalid ComfyUI config JSON")
	}
	return cfg, true, nil
}

func (c *v2Controller) uploadMedia(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		envelopeErr(w, 400, 3007, fmt.Errorf("invalid media upload"))
		return
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		envelopeErr(w, 400, 3007, fmt.Errorf("file is required"))
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 50<<20+1))
	if err != nil || len(data) > 50<<20 {
		envelopeErr(w, 413, 3007, fmt.Errorf("media exceeds size limit"))
		return
	}
	ref, err := c.st.ComfyUI.SaveMedia(data, h.Filename, h.Header.Get("Content-Type"), r.FormValue("instance_id"))
	if err != nil {
		envelopeErr(w, 500, 3007, fmt.Errorf("media could not be stored"))
		return
	}
	envelope(w, 201, 0, ref, "")
}
func (c *v2Controller) getMedia(w http.ResponseWriter, r *http.Request, id string) {
	ref, path, err := c.st.ComfyUI.LoadMedia(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("media not found"))
		return
	}
	if _, err := os.Stat(path); err != nil {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("media not found"))
		return
	}
	w.Header().Set("Content-Type", ref.MIME)
	w.Header().Set("X-API-Code", "0")
	http.ServeFile(w, r, path)
}

type workflowRequest struct {
	Format     string                  `json:"format"`
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Version    int                     `json:"version"`
	Enabled    *bool                   `json:"enabled"`
	Workflow   map[string]any          `json:"workflow"`
	Bindings   []comfyui.Binding       `json:"bindings"`
	Defaults   map[string]any          `json:"defaults"`
	Output     comfyui.OutputSpec      `json:"output"`
	APIJSON    map[string]any          `json:"api_json"`
	Config     *comfyui.WorkflowConfig `json:"config"`
	InstanceID string                  `json:"instance_id"`
	Canvas     *comfyui.CanvasDocument `json:"canvas"`
}

func (c *v2Controller) listWorkflows(w http.ResponseWriter, r *http.Request) {
	items, err := c.st.ComfyUI.ListWorkflows()
	if err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	envelope(w, 200, 0, items, "")
}
func (c *v2Controller) importWorkflow(w http.ResponseWriter, r *http.Request) {
	var req workflowRequest
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	wflow := toWorkflow(req)
	if len(wflow.Workflow) == 0 && len(req.APIJSON) > 0 {
		wflow.Workflow = req.APIJSON
	}
	if req.Format != "" && req.Format != "comfyui_api_v1" {
		envelope(w, 400, codeWorkflowInvalid, map[string]any{"detected_format": req.Format}, "unsupported workflow format")
		return
	}
	if _, ok := req.Workflow["nodes"]; ok {
		envelope(w, 400, codeWorkflowInvalid, map[string]any{"detected_format": "canvas_or_ui"}, "export the workflow as ComfyUI API JSON before importing")
		return
	}
	if wflow.ID == "" {
		wflow.ID = "wf-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if wflow.Version == 0 {
		wflow.Version = 1
	}
	wflow.Enabled = true
	if req.Config == nil {
		inferred, _ := comfyui.InferBindings(wflow.Workflow)
		req.Config = &inferred
	}
	wflow.Config = req.Config
	wflow.Format = "comfyui_api_v1"
	wflow.InstanceID = req.InstanceID
	comfyui.NormalizeWorkflowConfig(&wflow)
	if errs := comfyui.ValidateWorkflow(wflow); len(errs) > 0 {
		envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": errs}, "workflow validation failed")
		return
	}
	now := time.Now().UTC()
	wflow.CreatedAt = now
	wflow.UpdatedAt = now
	canvas := comfyui.DefaultCanvas(wflow)
	if req.Canvas != nil {
		canvas = req.Canvas.Normalize()
		canvas.WorkflowID = wflow.ID
	}
	if es := comfyui.ValidateCanvas(canvas, wflow); len(es) > 0 {
		envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "canvas validation failed")
		return
	}
	if err := c.st.ComfyUI.SaveWorkflow(wflow); err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	if err := c.st.ComfyUI.SaveWorkflowCanvas(wflow.ID, canvas); err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	envelope(w, 201, 0, map[string]any{"workflow": wflow, "config": wflow.Config, "canvas": canvas}, "")
}
func toWorkflow(req workflowRequest) comfyui.Workflow {
	w := comfyui.Workflow{Format: req.Format, ID: req.ID, Name: req.Name, Version: req.Version, Enabled: true, Workflow: req.Workflow, Bindings: req.Bindings, Defaults: req.Defaults, Output: req.Output, Config: req.Config, InstanceID: req.InstanceID}
	if req.Enabled != nil {
		w.Enabled = *req.Enabled
	}
	return w
}
func (c *v2Controller) workflow(w http.ResponseWriter, r *http.Request, id string) {
	if strings.HasSuffix(id, "/prompt-schema") {
		if r.Method != http.MethodGet {
			envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
			return
		}
		base := strings.TrimSuffix(id, "/prompt-schema")
		promptSchema, err := c.loadPromptSchema(base)
		if err != nil {
			envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
			return
		}
		envelope(w, 200, 0, promptSchema, "")
		return
	}
	if strings.HasSuffix(id, "/run") {
		base := strings.TrimSuffix(id, "/run")
		var req testJobRequest
		if r.Body != nil {
			_ = decodeBody(r, &req)
		}
		req.WorkflowID = base
		b, _ := json.Marshal(req)
		r.Body = io.NopCloser(bytes.NewReader(b))
		c.testJob(w, r)
		return
	}
	for _, action := range []string{"schema", "config", "export"} {
		if strings.HasSuffix(id, "/"+action) {
			base := strings.TrimSuffix(id, "/"+action)
			if action == "schema" {
				wf, e := c.st.ComfyUI.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				cfg, _ := c.st.ComfyUI.LoadWorkflowConfig(base)
				envelope(w, 200, 0, map[string]any{"workflow": wf, "config": cfg, "fields": cfg.Fields}, "")
				return
			}
			if action == "config" {
				if r.Method != http.MethodPut {
					cfg, e := c.st.ComfyUI.LoadWorkflowConfig(base)
					if e != nil {
						envelopeErr(w, 404, codeNotFound, e)
						return
					}
					envelope(w, 200, 0, cfg, "")
					return
				}
				var cfg comfyui.WorkflowConfig
				if e := decodeBody(r, &cfg); e != nil {
					envelopeErr(w, 400, codeInvalidRequest, e)
					return
				}
				wf, e := c.st.ComfyUI.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				if es := comfyui.ValidateWorkflowConfig(cfg, wf.Workflow); len(es) > 0 {
					envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "workflow config validation failed")
					return
				}
				if e := c.st.ComfyUI.SaveWorkflowConfig(base, cfg); e != nil {
					envelopeErr(w, 500, codeWorkflowInvalid, e)
					return
				}
				envelope(w, 200, 0, cfg, "")
				return
			}
			if action == "export" {
				format := r.URL.Query().Get("format")
				if format == "" {
					format = "api"
				}
				wf, e := c.st.ComfyUI.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				var content any
				filename := base + ".api.json"
				if format == "config" {
					content, _ = c.st.ComfyUI.LoadWorkflowConfig(base)
					filename = base + ".config.json"
				} else if format == "api" {
					content = wf.Workflow
				} else {
					envelopeErr(w, 404, codeNotFound, fmt.Errorf("canvas export is unavailable"))
					return
				}
				envelope(w, 200, 0, map[string]any{"format": format, "id": base, "filename": filename, "content": content}, "")
				return
			}
		}
	}
	if strings.HasSuffix(id, "/canvas") {
		base := strings.TrimSuffix(id, "/canvas")
		c.workflowCanvas(w, r, base)
		return
	}
	id = strings.TrimSuffix(id, "/validate")
	validate := strings.HasSuffix(r.URL.Path, "/validate")
	if validate {
		wf, err := c.st.ComfyUI.LoadWorkflow(id)
		if err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		comfyui.NormalizeWorkflowConfig(&wf)
		errs := comfyui.ValidateWorkflow(wf)
		envelope(w, 200, 0, map[string]any{"valid": len(errs) == 0, "errors": errs}, "")
		return
	}
	wf, err := c.st.ComfyUI.LoadWorkflow(id)
	if err != nil {
		if os.IsNotExist(err) {
			envelopeErr(w, 404, codeNotFound, err)
		} else {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
		}
		return
	}
	comfyui.NormalizeWorkflowConfig(&wf)
	switch r.Method {
	case http.MethodGet:
		canvas, _ := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(id)
		envelope(w, 200, 0, map[string]any{"workflow": wf, "config": wf.Config, "canvas": canvas}, "")
	case http.MethodPut:
		var req workflowRequest
		if err := decodeBody(r, &req); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		nw := toWorkflow(req)
		nw.ID = id
		nw.CreatedAt = wf.CreatedAt
		nw.UpdatedAt = time.Now().UTC()
		if req.Enabled == nil {
			nw.Enabled = true
		}
		if len(nw.Workflow) == 0 {
			nw.Workflow = wf.Workflow
		}
		if len(req.APIJSON) > 0 {
			nw.Workflow = req.APIJSON
		}
		if nw.Config == nil {
			nw.Config = wf.Config
		}
		comfyui.NormalizeWorkflowConfig(&nw)
		if errs := comfyui.ValidateWorkflow(nw); len(errs) > 0 {
			envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": errs}, "workflow validation failed")
			return
		}
		canvas, _ := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(id)
		if req.Canvas != nil {
			canvas = req.Canvas.Normalize()
			canvas.WorkflowID = id
		}
		if es := comfyui.ValidateCanvas(canvas, nw); len(es) > 0 {
			envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "canvas validation failed")
			return
		}
		if err := c.st.ComfyUI.SaveWorkflow(nw); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		if err := c.st.ComfyUI.SaveWorkflowCanvas(id, canvas); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"workflow": nw, "config": nw.Config, "canvas": canvas}, "")
	case http.MethodDelete:
		if err := c.st.ComfyUI.DeleteWorkflow(id); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"deleted": true}, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) workflowCanvas(w http.ResponseWriter, r *http.Request, id string) {
	wf, err := c.st.ComfyUI.LoadWorkflow(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	canvas, err := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(id)
	if err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	switch r.Method {
	case http.MethodGet:
		envelope(w, 200, 0, canvas, "")
	case http.MethodPut:
		var payload map[string]any
		if err := decodeBody(r, &payload); err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		next, err := mergeCanvasPayload(canvas, payload, wf)
		if err != nil {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		next = next.Normalize()
		next.WorkflowID = id
		if es := comfyui.ValidateCanvas(next, wf); len(es) > 0 {
			envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "canvas validation failed")
			return
		}
		if err := c.st.ComfyUI.SaveWorkflowCanvas(id, next); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		envelope(w, 200, 0, next, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

// mergeCanvasPayload accepts the v2 CanvasDocument and the compact payload
// emitted by older browser clients (viewport.k plus a node-position map).
func mergeCanvasPayload(base comfyui.CanvasDocument, payload map[string]any, wf comfyui.Workflow) (comfyui.CanvasDocument, error) {
	_, nodesAreMap := payload["nodes"].(map[string]any)
	if raw, ok := payload["format"]; ok && fmt.Sprint(raw) != "" && !nodesAreMap {
		b, _ := json.Marshal(payload)
		var full comfyui.CanvasDocument
		if err := json.Unmarshal(b, &full); err != nil {
			return full, err
		}
		return full, nil
	}
	if raw, ok := payload["viewport"].(map[string]any); ok {
		if v, ok := raw["x"].(float64); ok {
			base.Viewport.X = v
		}
		if v, ok := raw["y"].(float64); ok {
			base.Viewport.Y = v
		}
		if v, ok := raw["scale"].(float64); ok {
			base.Viewport.Scale = v
		}
		if v, ok := raw["k"].(float64); ok {
			base.Viewport.Scale = v
		}
	}
	if raw, ok := payload["nodes"].(map[string]any); ok {
		bySource := map[string]*comfyui.CanvasNode{}
		for i := range base.Nodes {
			bySource[base.Nodes[i].SourceNodeID] = &base.Nodes[i]
		}
		for source, pos := range raw {
			p, ok := pos.(map[string]any)
			if !ok {
				continue
			}
			n := bySource[source]
			if n == nil {
				continue
			}
			if x, ok := p["x"].(float64); ok {
				n.X = x
			}
			if y, ok := p["y"].(float64); ok {
				n.Y = y
			}
		}
	}
	if fields, ok := payload["fields"]; ok {
		b, _ := json.Marshal(fields)
		if err := json.Unmarshal(b, &base.Fields); err != nil {
			return base, err
		}
	}
	if cards, ok := payload["mini_test_cards"]; ok {
		b, _ := json.Marshal(cards)
		if err := json.Unmarshal(b, &base.MiniTestCards); err != nil {
			return base, err
		}
	}
	base.WorkflowID = wf.ID
	return base, nil
}

type testJobRequest struct {
	WorkflowID     string           `json:"workflow_id"`
	Prompt         string           `json:"prompt"`
	NegativePrompt string           `json:"negative_prompt"`
	Parameters     map[string]any   `json:"parameters"`
	UnitID         string           `json:"unit_id"`
	Chapter        int              `json:"chapter"`
	Ordinal        int              `json:"ordinal"`
	InstanceID     string           `json:"instance_id"`
	Inputs         []map[string]any `json:"inputs"`
	FieldValues    map[string]any   `json:"field_values"`
	MiniTestValues map[string]any   `json:"mini_test_values"`
	Mode           string           `json:"mode"`
}

// mergeCanvasRuntime projects the editable canvas fields into the transient
// workflow used for this run. The persisted API workflow is never mutated.
// Canvas fields are authoritative when present, including an empty list after
// a user un-exposes every input.
func mergeCanvasRuntime(wf *comfyui.Workflow, canvas comfyui.CanvasDocument, values map[string]any) error {
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
	// Resolve compatibility aliases per binding. Generic input aliases are
	// accepted only when there is one exposed field with that input name.
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

func (c *v2Controller) testJob(w http.ResponseWriter, r *http.Request) {
	var req testJobRequest
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	cfg, err := c.st.ComfyUI.LoadConfig()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	wfID := req.WorkflowID
	if wfID == "" {
		wfID = cfg.WorkflowID
	}
	wf, err := c.st.ComfyUI.LoadWorkflow(wfID)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	comfyui.NormalizeWorkflowConfig(&wf)
	canvas, _ := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(wf.ID)
	instances, settings, e := c.st.ComfyUI.LoadInstances()
	if e == nil && len(instances) > 0 {
		queues := map[string]int{}
		for _, inst := range instances {
			if cl, e := inst.Client(cfg); e == nil {
				qctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
				pending, _, qe := cl.Queue(qctx)
				cancel()
				if qe == nil {
					queues[inst.ID] = pending
				}
			}
		}
		selected, se := comfyui.SelectLeastQueue(instances, queues, comfyui.SelectionHint{InstanceID: req.InstanceID, WorkflowInstanceID: wf.InstanceID, DefaultInstanceID: settings.DefaultInstanceID, Strategy: settings.Strategy})
		if se != nil && (req.InstanceID != "" || wf.InstanceID != "") {
			envelopeErr(w, 409, codeConflict, fmt.Errorf("requested ComfyUI instance is unavailable"))
			return
		}
		if se == nil {
			cfg.BaseURL = selected.BaseURL
			req.InstanceID = selected.ID
		}
	}
	values := comfyui.CanvasValues(canvas, req.FieldValues, req.MiniTestValues)
	// Explicit field_values win over parameters and prompt convenience aliases.
	for k, v := range req.Parameters {
		if _, exists := values[k]; !exists {
			values[k] = v
		}
	}
	if req.Prompt != "" {
		if _, exists := values["text"]; !exists {
			values["text"] = req.Prompt
		}
		if _, exists := values["positive_prompt"]; !exists {
			values["positive_prompt"] = req.Prompt
		}
	}
	if req.NegativePrompt != "" {
		if _, exists := values["negative_prompt"]; !exists {
			values["negative_prompt"] = req.NegativePrompt
		}
	}
	for _, field := range canvas.Fields {
		if field.Default != nil {
			if _, exists := values[field.ID]; !exists {
				values[field.ID] = field.Default
			}
		}
	}
	for _, input := range req.Inputs {
		key := fmt.Sprint(input["key"])
		if key == "" {
			key = fmt.Sprint(input["id"])
		}
		if key == "" {
			key = fmt.Sprint(input["input"])
		}
		if key == "" {
			continue
		}
		if ref, ok := input["media_ref"].(map[string]any); ok {
			if storage := fmt.Sprint(ref["storage_key"]); storage != "" {
				values[key] = storage
				continue
			}
		}
		if storage := fmt.Sprint(input["storage_key"]); storage != "" && storage != "<nil>" {
			values[key] = storage
			continue
		}
		if value, ok := input["value"]; ok {
			values[key] = value
		}
	}
	if err := mergeCanvasRuntime(&wf, canvas, values); err != nil {
		envelopeErr(w, 400, codeWorkflowInvalid, err)
		return
	}
	bound, err := comfyui.ApplyBindings(wf, values)
	if err != nil {
		envelopeErr(w, 400, codeWorkflowInvalid, err)
		return
	}
	for k, v := range req.Parameters {
		if b, ok := wf.Defaults[k]; ok {
			_ = b
			_ = v
		}
	}
	job := store.ImageJob{JobID: store.NewImageJobID(), UnitID: req.UnitID, Chapter: req.Chapter, Ordinal: req.Ordinal, WorkflowID: wf.ID, InstanceID: req.InstanceID, Status: "pending", Stage: "binding", Attempt: 1, Trigger: "test", Prompt: req.Prompt, NegativePrompt: req.NegativePrompt, PromptValues: values, Parameters: req.Parameters, Inputs: req.Inputs, Recoverable: true, StartedAt: time.Now().UTC()}
	if snapshotKey, snapshotErr := c.st.ComfyUI.SaveJobWorkflow(job.JobID, bound); snapshotErr == nil {
		job.SnapshotKey = snapshotKey
	}
	if err := c.st.ComfyUI.SaveJob(job); err != nil {
		envelopeErr(w, 500, codeJobFailed, err)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.running[job.JobID] = cancel
	c.mu.Unlock()
	go c.runJob(ctx, job, bound, cfg, wf)
	envelope(w, 202, 0, job, "")
}
func (c *v2Controller) runJob(ctx context.Context, job store.ImageJob, wfMap map[string]any, cfg comfyui.Config, wf comfyui.Workflow) {
	defer func() { c.mu.Lock(); delete(c.running, job.JobID); c.mu.Unlock() }()
	cl, err := comfyui.NewHTTPClient(cfg)
	if err == nil {
		ctx2, cancel := context.WithTimeout(ctx, cfg.Timeout())
		defer cancel()
		job.Status = "submitting"
		job.Stage = "submitting"
		_ = c.st.ComfyUI.SaveJob(job)
		var pid string
		pid, err = cl.Submit(ctx2, wfMap, cfg.ClientID)
		if err == nil {
			job.PromptID = pid
			job.Status = "queued"
			job.Stage = "queued"
			_ = c.st.ComfyUI.SaveJob(job)
			var h comfyui.History
			h, err = cl.Wait(ctx2, pid, cfg.PollInterval())
			if err == nil {
				job.Status = "running"
				job.Stage = "running"
				job.Outputs = classifyOutputs(h.Outputs, job)
				_ = c.st.ComfyUI.SaveJob(job)
				ref, ok := firstOutput(h.Outputs, wf.Output)
				if !ok {
					err = fmt.Errorf("ComfyUI returned no image output")
				} else {
					img, e := cl.Download(ctx2, ref, cfg.MaxResponseBytes)
					if e != nil {
						err = e
					} else {
						path := filepath.Join(c.rt.Dir(), "meta", "images", "tests", job.JobID+".png")
						if job.Chapter > 0 && job.Ordinal > 0 {
							path = filepath.Join(c.rt.Dir(), "drafts", fmt.Sprintf("%02d.units", job.Chapter), fmt.Sprintf("%03d.png", job.Ordinal))
						}
						_ = os.MkdirAll(filepath.Dir(path), 0755)
						err = os.WriteFile(path, img.Data, 0644)
						// Keep a browser-safe URL alongside the local file metadata. The
						// output endpoint serves the saved image with its real MIME type,
						// so clients do not need to reconstruct the drafts path.
						job.Output = map[string]any{
							"filename":    filepath.Base(path),
							"mime":        img.ContentType,
							"kind":        "image",
							"url":         fmt.Sprintf("/api/v2/comfyui/jobs/%s/image", job.JobID),
							"preview_url": fmt.Sprintf("/api/v2/comfyui/jobs/%s/image", job.JobID),
						}
					}
				}
			}
		}
		if err == nil {
			job.Status = "completed"
			job.Stage = "completed"
		} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx2.Err(), context.DeadlineExceeded) {
			job.Status = "timeout"
			job.Error = "ComfyUI job timed out"
			ictx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_ = cl.Interrupt(ictx)
			stop()
		} else if ctx.Err() != nil || errors.Is(err, context.Canceled) {
			job.Status = "cancelled"
			if ctx.Err() != nil {
				job.Error = ctx.Err().Error()
			} else {
				job.Error = "ComfyUI job cancelled"
			}
		} else {
			job.Status = "failed"
			job.Error = err.Error()
		}
	} else {
		job.Status = "failed"
		job.Error = err.Error()
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	_ = c.st.ComfyUI.SaveJob(job)
}

func classifyOutputs(outputs map[string]any, job store.ImageJob) []comfyui.MediaOutput {
	var out []comfyui.MediaOutput
	index := 0
	for node, v := range outputs {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		classType := fmt.Sprint(m["class_type"])
		if classType == "<nil>" {
			classType = ""
		}
		for key, val := range m {
			for _, x := range outputEntries(val) {
				name := fmt.Sprint(x["filename"])
				if name == "<nil>" {
					name = ""
				}
				mime := fmt.Sprint(x["mime"])
				if mime == "<nil>" || mime == "" {
					mime = fmt.Sprint(x["content_type"])
				}
				if mime == "<nil>" {
					mime = ""
				}
				if mime == "" {
					mime = mimeFromName(name)
				}
				kind := outputKind(name, mime, classType)
				if kind == "" {
					continue
				}
				out = append(out, comfyui.MediaOutput{Kind: kind, NodeID: node, OutputKey: key, ClassType: classType, MIME: mime, Previewable: kind == "image", URL: fmt.Sprintf("/api/v2/comfyui/jobs/%s/outputs/%d", job.JobID, index)})
				index++
			}
		}
	}
	return out
}

func outputEntries(raw any) []map[string]any {
	switch arr := raw.(type) {
	case []any:
		out := make([]map[string]any, 0, len(arr))
		for _, item := range arr {
			if m, ok := item.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case []map[string]any:
		return arr
	default:
		return nil
	}
}
func mimeFromName(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".bmp":
		return "image/bmp"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4":
		return "video/mp4"
	case ".wav":
		return "audio/wav"
	}
	return "application/octet-stream"
}
func outputKind(name, mime, classType string) string {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(mime, "image/") {
		return "image"
	}
	if strings.HasPrefix(mime, "video/") {
		return "video"
	}
	if strings.HasPrefix(mime, "audio/") {
		return "audio"
	}
	s := strings.ToLower(name + " " + mime + " " + classType)
	switch {
	case strings.Contains(s, "image") || isImageName(name):
		return "image"
	case strings.Contains(s, "video") || strings.HasSuffix(s, ".mp4"):
		return "video"
	case strings.Contains(s, "audio") || strings.HasSuffix(s, ".wav"):
		return "audio"
	case strings.Contains(s, "text") || strings.Contains(s, "string"):
		return "text"
	default:
		return "file"
	}
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(name))) {
	case ".png", ".jpg", ".jpeg", ".webp", ".gif", ".bmp", ".tif", ".tiff":
		return true
	default:
		return false
	}
}
func firstOutput(outputs map[string]any, s comfyui.OutputSpec) (comfyui.OutputRef, bool) {
	if v, ok := outputs[s.NodeID]; ok {
		if m, ok := v.(map[string]any); ok {
			if arr, ok := m[s.Path].([]any); ok && len(arr) > s.Index {
				if x, ok := arr[s.Index].(map[string]any); ok {
					return comfyui.OutputRef{Filename: fmt.Sprint(x["filename"]), Subfolder: fmt.Sprint(x["subfolder"]), Type: fmt.Sprint(x["type"])}, true
				}
			}
		}
	}
	for _, v := range outputs {
		if m, ok := v.(map[string]any); ok {
			if arr, ok := m["images"].([]any); ok && len(arr) > 0 {
				if x, ok := arr[0].(map[string]any); ok {
					return comfyui.OutputRef{Filename: fmt.Sprint(x["filename"]), Subfolder: fmt.Sprint(x["subfolder"]), Type: fmt.Sprint(x["type"])}, true
				}
			}
		}
	}
	return comfyui.OutputRef{}, false
}

func (c *v2Controller) job(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.Trim(id, "/")
	action := ""
	if i := strings.LastIndex(id, "/"); i >= 0 {
		action = id[i+1:]
		id = id[:i]
	}
	j, err := c.st.ComfyUI.LoadJob(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		envelope(w, 200, 0, j, "")
	case r.Method == http.MethodGet && action == "image":
		path := filepath.Join(c.rt.Dir(), "meta", "images", "tests", j.JobID+".png")
		if j.Chapter > 0 && j.Ordinal > 0 {
			path = filepath.Join(c.rt.Dir(), "drafts", fmt.Sprintf("%02d.units", j.Chapter), fmt.Sprintf("%03d.png", j.Ordinal))
		}
		if _, err := os.Stat(path); err != nil {
			envelopeErr(w, 404, codeNotFound, fmt.Errorf("job image not found"))
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-API-Code", "0")
		http.ServeFile(w, r, path)
	case r.Method == http.MethodGet && action == "outputs":
		envelope(w, 200, 0, j.Outputs, "")
	case r.Method == http.MethodPost && action == "cancel":
		c.mu.Lock()
		if cancel := c.running[id]; cancel != nil {
			cancel()
		}
		c.mu.Unlock()
		if cfg, e := c.st.ComfyUI.LoadConfig(); e == nil {
			if cl, e := comfyui.NewHTTPClient(cfg); e == nil {
				ictx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				_ = cl.Interrupt(ictx)
				cancel()
			}
		}
		j.Status = "cancelled"
		j.Error = "cancelled by user"
		if e := c.st.ComfyUI.SaveJob(j); e != nil {
			envelopeErr(w, 500, codeJobFailed, fmt.Errorf("job could not be saved"))
			return
		}
		envelope(w, 200, 0, j, "")
	case r.Method == http.MethodPost && action == "retry":
		var retryRequest struct {
			RegeneratePrompt bool `json:"regenerate_prompt"`
		}
		if r.Body != nil {
			_ = decodeBody(r, &retryRequest)
		}
		if retryRequest.RegeneratePrompt && j.Trigger == "unit" {
			bridge, bridgeErr := c.st.ComfyUI.LoadBridgeConfig()
			if bridgeErr != nil {
				envelopeErr(w, 500, codeConfigInvalid, bridgeErr)
				return
			}
			wf, wfErr := c.st.ComfyUI.LoadWorkflow(j.WorkflowID)
			if wfErr != nil {
				envelopeErr(w, 404, codeNotFound, wfErr)
				return
			}
			canvas, canvasErr := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(wf.ID)
			if canvasErr != nil {
				envelopeErr(w, 422, codePromptSchema, canvasErr)
				return
			}
			promptSchema, schemaErr := imagejob.BuildPromptSchema(wf.ID, canvas)
			if schemaErr != nil {
				envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, schemaErr.Error())
				return
			}
			if len(promptSchema.Fields) == 0 {
				envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, "请先在画布中勾选要发给提示词模型的字段")
				return
			}
			request, requestErr := c.unitPromptRequest(j.Chapter, j.Ordinal, bridge, promptSchema)
			if requestErr != nil {
				envelopeErr(w, 422, codePromptSchema, requestErr)
				return
			}
			request.SystemPrompt = promptSchema.Composed
			j.Status = "prompting"
			j.Stage = "prompting"
			j.Attempt++
			j.Error = ""
			j.PromptRaw = ""
			j.PromptValues = nil
			j.FinishedAt = nil
			j.StartedAt = time.Now().UTC()
			if saveErr := c.st.ComfyUI.SaveJob(j); saveErr != nil {
				envelopeErr(w, 500, codeJobFailed, saveErr)
				return
			}
			ctx, cancel := context.WithCancel(context.Background())
			c.mu.Lock()
			c.running[j.JobID] = cancel
			c.mu.Unlock()
			go c.runUnitImagePrompt(ctx, j, wf, canvas, bridge, promptSchema, request)
			envelope(w, 202, 0, j, "")
			return
		}
		j.Status = "pending"
		j.Stage = "binding"
		j.Attempt++
		j.Error = ""
		wf, e := c.st.ComfyUI.LoadWorkflow(j.WorkflowID)
		if e != nil {
			envelopeErr(w, 404, codeNotFound, fmt.Errorf("workflow for retry not found"))
			return
		}
		comfyui.NormalizeWorkflowConfig(&wf)
		canvas, _ := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(wf.ID)
		values := map[string]any{}
		for key, value := range j.PromptValues {
			values[key] = value
		}
		if len(values) == 0 {
			if j.Prompt != "" {
				values["text"] = j.Prompt
			}
			if j.NegativePrompt != "" {
				values["negative_prompt"] = j.NegativePrompt
			}
		}
		for k, v := range j.Parameters {
			values[k] = v
		}
		for _, input := range j.Inputs {
			key := fmt.Sprint(input["key"])
			if key == "" {
				key = fmt.Sprint(input["id"])
			}
			if key == "" {
				key = fmt.Sprint(input["input"])
			}
			if value, ok := input["value"]; ok && key != "" {
				values[key] = value
			}
			if storage := fmt.Sprint(input["storage_key"]); storage != "" && storage != "<nil>" && key != "" {
				values[key] = storage
			}
		}
		if e := mergeCanvasRuntime(&wf, canvas, values); e != nil {
			envelopeErr(w, 400, codeWorkflowInvalid, e)
			return
		}
		bound, e := comfyui.ApplyBindings(wf, values)
		if e != nil {
			envelopeErr(w, 400, codeWorkflowInvalid, e)
			return
		}
		cfg, e := c.st.ComfyUI.LoadConfig()
		if e == nil {
			if j.InstanceID != "" {
				if instances, _, ie := c.st.ComfyUI.LoadInstances(); ie == nil {
					for _, inst := range instances {
						if inst.ID == j.InstanceID {
							cfg.BaseURL = inst.BaseURL
							break
						}
					}
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			c.mu.Lock()
			c.running[id] = cancel
			c.mu.Unlock()
			if e := c.st.ComfyUI.SaveJob(j); e != nil {
				envelopeErr(w, 500, codeJobFailed, fmt.Errorf("retry job could not be saved"))
				return
			}
			go c.runJob(ctx, j, bound, cfg, wf)
			envelope(w, 202, 0, j, "")
			return
		}
		if e != nil {
			envelopeErr(w, 500, codeConfigInvalid, fmt.Errorf("ComfyUI configuration could not be loaded"))
			return
		}
		if e := c.st.ComfyUI.SaveJob(j); e != nil {
			envelopeErr(w, 500, codeJobFailed, fmt.Errorf("retry job could not be saved"))
			return
		}
		envelope(w, 202, 0, j, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}
func (c *v2Controller) jobOutput(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/outputs/")
	if len(parts) != 2 {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("output not found"))
		return
	}
	j, e := c.st.ComfyUI.LoadJob(parts[0])
	if e != nil {
		envelopeErr(w, 404, codeNotFound, e)
		return
	}
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("output not found"))
		return
	}
	var o comfyui.MediaOutput
	if idx < len(j.Outputs) {
		o = j.Outputs[idx]
	} else if idx == 0 && legacyImageOutput(j.Output) {
		o = comfyui.MediaOutput{Kind: "image", Previewable: true}
	} else {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("output not found"))
		return
	}
	if o.MIME == "" || o.MIME == "<nil>" {
		o.MIME = legacyOutputMIME(j.Output)
	}
	if o.MIME == "" || o.MIME == "<nil>" {
		o.MIME = "application/octet-stream"
	}
	if strings.HasPrefix(strings.ToLower(o.MIME), "image/") {
		o.Kind = "image"
		o.Previewable = true
	}
	if o.Kind != "image" && !o.Previewable {
		envelope(w, 200, 0, o, "")
		return
	}
	path := jobImagePath(c.rt.Dir(), j)
	if _, e := os.Stat(path); e != nil {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("output file not found"))
		return
	}
	w.Header().Set("Content-Type", o.MIME)
	w.Header().Set("X-API-Code", "0")
	w.Header().Set("Content-Disposition", "inline")
	http.ServeFile(w, r, path)
}

func jobImagePath(root string, j store.ImageJob) string {
	path := filepath.Join(root, "meta", "images", "tests", j.JobID+".png")
	if j.Chapter > 0 && j.Ordinal > 0 {
		path = filepath.Join(root, "drafts", fmt.Sprintf("%02d.units", j.Chapter), fmt.Sprintf("%03d.png", j.Ordinal))
	}
	return path
}

func legacyOutputMIME(output map[string]any) string {
	if output == nil {
		return ""
	}
	for _, key := range []string{"mime", "content_type", "contentType"} {
		if value := strings.TrimSpace(fmt.Sprint(output[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	if name := strings.TrimSpace(fmt.Sprint(output["filename"])); name != "" && name != "<nil>" {
		return mimeFromName(name)
	}
	return ""
}

func legacyImageOutput(output map[string]any) bool {
	mime := legacyOutputMIME(output)
	return strings.HasPrefix(strings.ToLower(mime), "image/") || outputKind(fmt.Sprint(output["filename"]), mime, "") == "image"
}

func (c *v2Controller) unit(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 {
		ch, err := strconv.Atoi(parts[0])
		if err != nil || ch <= 0 {
			envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("invalid chapter"))
			return
		}
		jobs, _ := c.st.ComfyUI.ListJobs()
		var filtered []store.ImageJob
		for _, j := range jobs {
			if j.Chapter == ch {
				j.PromptRaw = ""
				filtered = append(filtered, j)
			}
		}
		envelope(w, 200, 0, filtered, "")
		return
	}
	if len(parts) < 3 {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("unit route not found"))
		return
	}
	ch, e1 := strconv.Atoi(parts[0])
	ord, e2 := strconv.Atoi(parts[1])
	if e1 != nil || e2 != nil {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("invalid unit"))
		return
	}
	if len(parts) == 4 && parts[2] == "image" && parts[3] == "generate" {
		c.generateUnitImage(w, r, ch, ord)
		return
	}
	if strings.HasSuffix(rest, "/image-job") {
		jobs, _ := c.st.ComfyUI.ListJobs()
		for i := len(jobs) - 1; i >= 0; i-- {
			j := jobs[i]
			if j.Chapter == ch && j.Ordinal == ord {
				envelope(w, 200, 0, j, "")
				return
			}
		}
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	if strings.HasSuffix(rest, "/image/retry") {
		jobs, _ := c.st.ComfyUI.ListJobs()
		for _, j := range jobs {
			if j.Chapter == ch && j.Ordinal == ord {
				c.job(w, r, j.JobID+"/retry")
				return
			}
		}
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	base := filepath.Join(c.rt.Dir(), "drafts", fmt.Sprintf("%02d.units", ch), fmt.Sprintf("%03d", ord))
	for _, ext := range []string{".png", ".jpg", ".jpeg", ".webp"} {
		path := base + ext
		if _, err := os.Stat(path); err == nil {
			w.Header().Set("Content-Type", "image/"+strings.TrimPrefix(ext, "."))
			w.Header().Set("X-API-Code", "0")
			http.ServeFile(w, r, path)
			return
		}
	}
	envelopeErr(w, 404, codeNotFound, fmt.Errorf("unit image not found"))
}

type unitImageGenerateRequest struct {
	WorkflowID string `json:"workflow_id"`
	Force      bool   `json:"force"`
}

func (c *v2Controller) generateUnitImage(w http.ResponseWriter, r *http.Request, chapter, ordinal int) {
	if r.Method != http.MethodPost {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var request unitImageGenerateRequest
	if r.Body != nil && r.ContentLength != 0 {
		if err := decodeBody(r, &request); err != nil && !errors.Is(err, io.EOF) {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
	}
	bridge, err := c.st.ComfyUI.LoadBridgeConfig()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, fmt.Errorf("图片生成桥接配置无法读取"))
		return
	}
	if !bridge.Enabled {
		envelopeErr(w, 409, codeConflict, fmt.Errorf("图片生成桥接尚未启用"))
		return
	}
	workflowID := strings.TrimSpace(request.WorkflowID)
	if workflowID == "" {
		workflowID = bridge.WorkflowID
	}
	workflow, err := c.st.ComfyUI.LoadWorkflow(workflowID)
	if err != nil {
		envelopeErr(w, 422, codePromptSchema, fmt.Errorf("图片工作流不存在"))
		return
	}
	canvas, err := c.st.ComfyUI.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		envelopeErr(w, 422, codePromptSchema, err)
		return
	}
	promptSchema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
		return
	}
	if len(promptSchema.Fields) == 0 {
		envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, "请先在画布中勾选要发给提示词模型的字段")
		return
	}
	promptRequest, err := c.unitPromptRequest(chapter, ordinal, bridge, promptSchema)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	promptRequest.SystemPrompt = promptSchema.Composed
	workflowHash, err := hashJSON(workflow.Workflow)
	if err != nil {
		envelopeErr(w, 422, codePromptSchema, fmt.Errorf("cannot hash workflow"))
		return
	}
	idempotencyKey := unitImageIdempotency(promptRequest, workflow.ID, workflowHash, promptSchema.SchemaHash, imagejob.PromptFingerprint(promptRequest.SystemPrompt))
	jobs, err := c.st.ComfyUI.ListJobs()
	if err != nil {
		envelopeErr(w, 500, codeJobFailed, fmt.Errorf("图片任务无法读取"))
		return
	}
	for index := len(jobs) - 1; index >= 0; index-- {
		existing := jobs[index]
		if existing.Chapter != chapter || existing.Ordinal != ordinal || terminalImageJob(existing.Status) {
			continue
		}
		envelope(w, 409, codeUnitJobConflict, existing, "同一个 unit 已有图片任务正在运行")
		return
	}
	if !request.Force {
		if existing, found, err := c.st.ComfyUI.FindJobByIdempotencyKey(idempotencyKey); err == nil && found {
			envelope(w, http.StatusAccepted, 0, existing, "")
			return
		}
	} else {
		idempotencyKey += fmt.Sprintf(":attempt:%d", time.Now().UnixNano())
	}
	job := store.ImageJob{
		JobID: store.NewImageJobID(), UnitID: promptRequest.UnitID, Chapter: chapter, Ordinal: ordinal,
		WorkflowID: workflow.ID, Status: "prompting", Stage: "prompting", Attempt: 1,
		IdempotencyKey: idempotencyKey, Trigger: "unit", SchemaHash: promptSchema.SchemaHash,
		WorkflowHash: workflowHash, Recoverable: true, StartedAt: time.Now().UTC(),
	}
	if err := c.st.ComfyUI.SaveJob(job); err != nil {
		envelopeErr(w, 500, codeJobFailed, fmt.Errorf("图片任务无法保存"))
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.running[job.JobID] = cancel
	c.mu.Unlock()
	go c.runUnitImagePrompt(ctx, job, workflow, canvas, bridge, promptSchema, promptRequest)
	envelope(w, http.StatusAccepted, 0, job, "")
}

func (c *v2Controller) unitPromptRequest(chapter, ordinal int, bridge imagejob.BridgeConfig, promptSchema imagejob.PromptSchema) (imagejob.PromptRequest, error) {
	unitText, err := c.st.Drafts.LoadWritingUnit(chapter, ordinal)
	if err != nil {
		return imagejob.PromptRequest{}, err
	}
	if strings.TrimSpace(unitText) == "" {
		return imagejob.PromptRequest{}, fmt.Errorf("writing unit %d/%d 不存在或为空", chapter, ordinal)
	}
	plan, err := c.st.Drafts.LoadChapterPlan(chapter)
	if err != nil || plan == nil {
		return imagejob.PromptRequest{}, fmt.Errorf("第 %d 章计划不存在", chapter)
	}
	assignments := plan.WritingUnits()
	if ordinal <= 0 || ordinal > len(assignments) {
		return imagejob.PromptRequest{}, fmt.Errorf("writing unit ordinal is outside the chapter plan")
	}
	assignment := assignments[ordinal-1]
	planJSON, err := json.Marshal(assignment)
	if err != nil {
		return imagejob.PromptRequest{}, err
	}
	var previousTail string
	if ordinal > 1 && bridge.PreviousTailChars > 0 {
		previous, readErr := c.st.Drafts.LoadWritingUnit(chapter, ordinal-1)
		if readErr != nil {
			return imagejob.PromptRequest{}, readErr
		}
		previousTail = tailText(previous, bridge.PreviousTailChars)
	}
	schemaJSON, err := json.Marshal(promptSchema.Schema)
	if err != nil {
		return imagejob.PromptRequest{}, err
	}
	return imagejob.PromptRequest{
		UnitID: assignment.Unit.ID, Chapter: chapter, Ordinal: ordinal, ChapterTitle: plan.Title,
		UnitPlan: string(planJSON), UnitText: unitText, PreviousTail: previousTail,
		Schema: schemaJSON, SchemaHash: promptSchema.SchemaHash,
	}, nil
}

func (c *v2Controller) runUnitImagePrompt(ctx context.Context, job store.ImageJob, workflow comfyui.Workflow, canvas comfyui.CanvasDocument, bridge imagejob.BridgeConfig, promptSchema imagejob.PromptSchema, request imagejob.PromptRequest) {
	defer func() {
		c.mu.Lock()
		delete(c.running, job.JobID)
		c.mu.Unlock()
	}()
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(bridge.PrompterTimeoutMS)*time.Millisecond)
	raw, err := c.rt.GenerateImagePrompt(promptCtx, request)
	promptTimedOut := errors.Is(promptCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		if promptTimedOut {
			c.failImageJob(job, "failed", "prompting", "image prompt generation timed out", true)
		} else if ctx.Err() != nil {
			c.failImageJob(job, "cancelled", "prompting", "图片提示词生成已取消", true)
		} else {
			c.failImageJob(job, "failed", "prompting", err.Error(), true)
		}
		return
	}
	job.PromptRaw = truncateBytes(raw, 64<<10)
	job.Status = "validating"
	job.Stage = "validating"
	_ = c.st.ComfyUI.SaveJob(job)
	parsed, err := imagejob.ParseAndValidate(raw, promptSchema, bridge.Strict)
	if err != nil {
		c.failImageJob(job, "failed", "validating", err.Error(), true)
		return
	}
	if ctx.Err() != nil {
		c.failImageJob(job, "cancelled", "validating", "image generation cancelled", true)
		return
	}
	job.PromptValues = parsed.Values
	job.Status = "binding"
	job.Stage = "binding"
	_ = c.st.ComfyUI.SaveJob(job)
	values := make(map[string]any, len(parsed.Values))
	for key, value := range parsed.Values {
		values[key] = value
	}
	if err := mergeCanvasRuntime(&workflow, canvas, values); err != nil {
		c.failImageJob(job, "failed", "binding", err.Error(), true)
		return
	}
	if ctx.Err() != nil {
		c.failImageJob(job, "cancelled", "binding", "image generation cancelled", true)
		return
	}
	bound, err := comfyui.ApplyBindings(workflow, values)
	if err != nil {
		c.failImageJob(job, "failed", "binding", err.Error(), true)
		return
	}
	snapshotKey, err := c.st.ComfyUI.SaveJobWorkflow(job.JobID, bound)
	if err != nil {
		c.failImageJob(job, "failed", "binding", "cannot save workflow snapshot", true)
		return
	}
	job.SnapshotKey = snapshotKey
	job.Recoverable = true
	_ = c.st.ComfyUI.SaveJob(job)
	config, err := c.st.ComfyUI.LoadConfig()
	if err != nil {
		c.failImageJob(job, "failed", "binding", "ComfyUI 配置无法读取", true)
		return
	}
	if workflow.InstanceID != "" {
		job.InstanceID = workflow.InstanceID
		if instances, _, loadErr := c.st.ComfyUI.LoadInstances(); loadErr == nil {
			for _, instance := range instances {
				if instance.ID == workflow.InstanceID && instance.Enabled {
					config.BaseURL = instance.BaseURL
					break
				}
			}
		}
	}
	c.runJob(ctx, job, bound, config, workflow)
}

func (c *v2Controller) failImageJob(job store.ImageJob, status, stage, message string, recoverable bool) {
	job.Status = status
	job.Stage = stage
	job.Error = truncateBytes(message, 2048)
	job.Recoverable = recoverable
	now := time.Now().UTC()
	job.FinishedAt = &now
	_ = c.st.ComfyUI.SaveJob(job)
}

func hashJSON(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func unitImageIdempotency(request imagejob.PromptRequest, workflowID, workflowHash, schemaHash, promptFingerprint string) string {
	contentHash := sha256.Sum256([]byte(request.UnitText))
	parts := strings.Join([]string{request.UnitID, hex.EncodeToString(contentHash[:]), workflowID, workflowHash, schemaHash, promptFingerprint}, "\x00")
	sum := sha256.Sum256([]byte(parts))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func terminalImageJob(status string) bool {
	switch status {
	case "completed", "failed", "timeout", "cancelled":
		return true
	default:
		return false
	}
}

func tailText(value string, maximum int) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	return string(runes[len(runes)-maximum:])
}

func truncateBytes(value string, maximum int) string {
	if len(value) <= maximum {
		return value
	}
	return value[:maximum]
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(v)
}
