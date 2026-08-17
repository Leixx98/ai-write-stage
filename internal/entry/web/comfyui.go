package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
)

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

func (c *v2Controller) bridgeConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		config, err := c.media.LoadBridgeConfig()
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
			if err := c.media.SaveBridgeConfig(config); err != nil {
				envelopeErr(w, 500, codeConfigInvalid, err)
				return
			}
			envelope(w, 200, 0, config, "")
			return
		}
		workflow, err := c.media.LoadWorkflow(config.WorkflowID)
		if err != nil {
			envelopeErr(w, 422, codePromptSchema, fmt.Errorf("workflow_id must reference a saved workflow"))
			return
		}
		canvas, err := c.media.LoadOrCreateWorkflowCanvas(workflow.ID)
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
		if err := c.media.SaveBridgeConfig(config); err != nil {
			envelopeErr(w, 500, codeConfigInvalid, fmt.Errorf("图片生成桥接配置无法保存"))
			return
		}
		envelope(w, 200, 0, config, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) loadPromptSchema(workflowID string) (imagejob.PromptSchema, error) {
	workflow, err := c.media.LoadWorkflow(workflowID)
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	canvas, err := c.media.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	schema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	presets, err := c.mergedPrompterPresets()
	if err != nil {
		return imagejob.PromptSchema{}, err
	}
	schema.Presets = presets
	if id := strings.TrimSpace(canvas.PrompterPreset); id != "" {
		schema.Preset = id
	}
	return schema, nil
}

func (c *v2Controller) mergedPrompterPresets() ([]imagejob.PrompterPreset, error) {
	doc, err := c.media.LoadPrompterPresets()
	if err != nil {
		return nil, err
	}
	builtins := imagejob.PrompterPresets()
	merged := make([]imagejob.PrompterPreset, 0, len(builtins)+len(doc.Presets))
	seen := make(map[string]struct{}, len(builtins))
	for _, preset := range builtins {
		if saved, ok := doc.Presets[preset.ID]; ok {
			if strings.TrimSpace(saved.Label) == "" {
				saved.Label = preset.Label
			}
			if strings.TrimSpace(saved.Description) == "" {
				saved.Description = preset.Description
			}
			preset = saved
		}
		merged = append(merged, preset)
		seen[preset.ID] = struct{}{}
	}
	customIDs := make([]string, 0, len(doc.Presets))
	for id := range doc.Presets {
		if _, ok := seen[id]; !ok {
			customIDs = append(customIDs, id)
		}
	}
	sort.Strings(customIDs)
	for _, id := range customIDs {
		merged = append(merged, doc.Presets[id])
	}
	return merged, nil
}

func (c *v2Controller) prompterPresets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		presets, err := c.mergedPrompterPresets()
		if err != nil {
			envelopeErr(w, 500, codeConfigInvalid, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"presets": presets}, "")
		return
	}
	if r.Method != http.MethodPut {
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	var req struct {
		Action     string `json:"action"`
		Name       string `json:"name"`
		WorkflowID string `json:"workflow_id"`
		Template   string `json:"template"`
		Overwrite  bool   `json:"overwrite"`
	}
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.WorkflowID = strings.TrimSpace(req.WorkflowID)
	if req.Action != "save" && req.Action != "save_as" {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("unsupported prompter preset operation"))
		return
	}
	if !validPresetName(req.Name) {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("preset name must be 1-64 characters"))
		return
	}
	if req.WorkflowID == "" {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("workflow_id is required"))
		return
	}
	if strings.TrimSpace(req.Template) == "" {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("提示词模板不能为空"))
		return
	}
	if len([]rune(req.Template)) > 100000 {
		envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("提示词模板不能超过 100000 个字符"))
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.media.LoadWorkflow(req.WorkflowID); err != nil {
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("图片工作流不存在"))
		return
	}
	doc, err := c.media.LoadPrompterPresets()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	_, customExists := doc.Presets[req.Name]
	builtinExists := false
	var label, description string
	for _, preset := range imagejob.PrompterPresets() {
		if preset.ID == req.Name {
			builtinExists = true
			label, description = preset.Label, preset.Description
			break
		}
	}
	if req.Action == "save_as" && (customExists || builtinExists) && !req.Overwrite {
		envelopeErr(w, 409, codeConflict, fmt.Errorf("同名生图提示词预设已存在"))
		return
	}
	if label == "" {
		label = req.Name
	}
	doc.Presets[req.Name] = imagejob.PrompterPreset{ID: req.Name, Label: label, Description: description, Template: req.Template}
	if err := c.media.SavePrompterPresets(doc); err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	canvas, err := c.media.LoadOrCreateWorkflowCanvas(req.WorkflowID)
	if err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	canvas.PrompterPreset = req.Name
	canvas.PrompterTemplate = req.Template
	if err := c.media.SaveWorkflowCanvas(req.WorkflowID, canvas); err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	schema, err := c.loadPromptSchema(req.WorkflowID)
	if err != nil {
		envelopeErr(w, 500, codePromptSchema, err)
		return
	}
	envelope(w, 200, 0, schema, "")
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
	if bridge, bridgeErr := c.media.LoadBridgeConfig(); bridgeErr == nil {
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
		cfg, err := c.media.LoadConfig()
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
		if err := c.media.SaveConfig(cfg); err != nil {
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
		cfg, err = c.media.LoadConfig()
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
	items, settings, err := c.media.LoadInstances()
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
	if _, loaded, e := c.media.LoadInstances(); e == nil {
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
	if err := c.media.SaveInstances(doc.Instances, settings); err != nil {
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
	items, _, err := c.media.LoadInstances()
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
	cfg, _ := c.media.LoadConfig()
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
	ref, err := c.media.SaveMedia(data, h.Filename, h.Header.Get("Content-Type"), r.FormValue("instance_id"))
	if err != nil {
		envelopeErr(w, 500, 3007, fmt.Errorf("media could not be stored"))
		return
	}
	envelope(w, 201, 0, ref, "")
}
func (c *v2Controller) getMedia(w http.ResponseWriter, r *http.Request, id string) {
	ref, path, err := c.media.LoadMedia(id)
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
	items, err := c.media.ListWorkflows()
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
	if err := c.media.SaveWorkflow(wflow); err != nil {
		envelopeErr(w, 500, codeWorkflowInvalid, err)
		return
	}
	if err := c.media.SaveWorkflowCanvas(wflow.ID, canvas); err != nil {
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
				wf, e := c.media.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				cfg, _ := c.media.LoadWorkflowConfig(base)
				envelope(w, 200, 0, map[string]any{"workflow": wf, "config": cfg, "fields": cfg.Fields}, "")
				return
			}
			if action == "config" {
				if r.Method != http.MethodPut {
					cfg, e := c.media.LoadWorkflowConfig(base)
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
				wf, e := c.media.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				if es := comfyui.ValidateWorkflowConfig(cfg, wf.Workflow); len(es) > 0 {
					envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "workflow config validation failed")
					return
				}
				if e := c.media.SaveWorkflowConfig(base, cfg); e != nil {
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
				wf, e := c.media.LoadWorkflow(base)
				if e != nil {
					envelopeErr(w, 404, codeNotFound, e)
					return
				}
				var content any
				filename := base + ".api.json"
				if format == "config" {
					content, _ = c.media.LoadWorkflowConfig(base)
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
		wf, err := c.media.LoadWorkflow(id)
		if err != nil {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		comfyui.NormalizeWorkflowConfig(&wf)
		errs := comfyui.ValidateWorkflow(wf)
		envelope(w, 200, 0, map[string]any{"valid": len(errs) == 0, "errors": errs}, "")
		return
	}
	wf, err := c.media.LoadWorkflow(id)
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
		canvas, _ := c.media.LoadOrCreateWorkflowCanvas(id)
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
		canvas, _ := c.media.LoadOrCreateWorkflowCanvas(id)
		if req.Canvas != nil {
			canvas = req.Canvas.Normalize()
			canvas.WorkflowID = id
		}
		if es := comfyui.ValidateCanvas(canvas, nw); len(es) > 0 {
			envelope(w, 400, codeWorkflowInvalid, map[string]any{"errors": es}, "canvas validation failed")
			return
		}
		if err := c.media.SaveWorkflow(nw); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		if err := c.media.SaveWorkflowCanvas(id, canvas); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"workflow": nw, "config": nw.Config, "canvas": canvas}, "")
	case http.MethodDelete:
		if err := c.media.DeleteWorkflow(id); err != nil {
			envelopeErr(w, 500, codeWorkflowInvalid, err)
			return
		}
		envelope(w, 200, 0, map[string]any{"deleted": true}, "")
	default:
		envelopeErr(w, 405, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) workflowCanvas(w http.ResponseWriter, r *http.Request, id string) {
	wf, err := c.media.LoadWorkflow(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	canvas, err := c.media.LoadOrCreateWorkflowCanvas(id)
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
		if err := c.media.SaveWorkflowCanvas(id, next); err != nil {
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
