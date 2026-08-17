package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

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
	return imagejob.MergeCanvasRuntime(wf, canvas, values)
}

func (c *v2Controller) testJob(w http.ResponseWriter, r *http.Request) {
	var req testJobRequest
	if err := decodeBody(r, &req); err != nil {
		envelopeErr(w, 400, codeInvalidRequest, err)
		return
	}
	cfg, err := c.media.LoadConfig()
	if err != nil {
		envelopeErr(w, 500, codeConfigInvalid, err)
		return
	}
	wfID := req.WorkflowID
	if wfID == "" {
		wfID = cfg.WorkflowID
	}
	wf, err := c.media.LoadWorkflow(wfID)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	comfyui.NormalizeWorkflowConfig(&wf)
	canvas, _ := c.media.LoadOrCreateWorkflowCanvas(wf.ID)
	instances, settings, e := c.media.LoadInstances()
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
	job := store.ImageJob{JobID: store.NewImageJobID(), UnitID: req.UnitID, Chapter: req.Chapter, Ordinal: req.Ordinal, WorkflowID: wf.ID, InstanceID: req.InstanceID, Status: "pending", Stage: "binding", Attempt: 1, Trigger: imagesvc.TriggerTest, Prompt: req.Prompt, NegativePrompt: req.NegativePrompt, PromptValues: values, Parameters: req.Parameters, Inputs: req.Inputs, Recoverable: true, StartedAt: time.Now().UTC()}
	started, err := c.svc.StartTest(job, bound, cfg, wf)
	if err != nil {
		envelopeErr(w, 500, codeJobFailed, err)
		return
	}
	envelope(w, 202, 0, started, "")
}
func classifyOutputs(outputs map[string]any, job store.ImageJob) []comfyui.MediaOutput {
	return imagejob.ClassifyOutputs(outputs, job.JobID)
}

func mimeFromName(name string) string {
	return imagejob.MIMEFromName(name)
}

func outputKind(name, mime, classType string) string {
	return imagejob.OutputKind(name, mime, classType)
}

func (c *v2Controller) job(w http.ResponseWriter, r *http.Request, id string) {
	id = strings.Trim(id, "/")
	action := ""
	if i := strings.LastIndex(id, "/"); i >= 0 {
		action = id[i+1:]
		id = id[:i]
	}
	j, err := c.media.LoadJob(id)
	if err != nil {
		envelopeErr(w, 404, codeNotFound, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		envelope(w, 200, 0, j, "")
	case r.Method == http.MethodGet && action == "image":
		path := imagesvc.ImagePath(c.rt.Dir(), j)
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
		cancelled, err := c.svc.Cancel(id)
		if err != nil {
			if errors.Is(err, imagesvc.ErrSave) {
				envelopeErr(w, 500, codeJobFailed, fmt.Errorf("job could not be saved"))
				return
			}
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		envelope(w, 200, 0, cancelled, "")
	case r.Method == http.MethodPost && action == "retry":
		var retryRequest struct {
			RegeneratePrompt bool `json:"regenerate_prompt"`
		}
		if r.Body != nil {
			_ = decodeBody(r, &retryRequest)
		}
		retried, err := c.svc.Retry(id, retryRequest.RegeneratePrompt)
		if err != nil {
			c.writeImageServiceErr(w, err)
			return
		}
		envelope(w, 202, 0, retried, "")
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
	j, e := c.media.LoadJob(parts[0])
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
	return imagesvc.ImagePath(root, j)
}

func (c *v2Controller) writeImageServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imagesvc.ErrNotFound):
		envelopeErr(w, 404, codeNotFound, err)
	case errors.Is(err, imagesvc.ErrConfig):
		envelopeErr(w, 500, codeConfigInvalid, err)
	case errors.Is(err, imagesvc.ErrSchema):
		envelopeErr(w, 422, codePromptSchema, err)
	case errors.Is(err, imagesvc.ErrBind):
		envelopeErr(w, 400, codeWorkflowInvalid, err)
	case errors.Is(err, imagesvc.ErrSave):
		envelopeErr(w, 500, codeJobFailed, err)
	case errors.Is(err, imagesvc.ErrInvalidUnitIdentity), errors.Is(err, imagesvc.ErrInvalidGalgameIdentity):
		envelopeErr(w, 400, codeInvalidRequest, err)
	default:
		envelopeErr(w, 500, codeJobFailed, err)
	}
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
