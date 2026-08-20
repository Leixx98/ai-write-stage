package web

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/imagejob/comfyadapter"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

func mergeCanvasRuntime(workflow *comfyui.Workflow, canvas comfyui.CanvasDocument, values map[string]any) error {
	return comfyadapter.MergeCanvasRuntime(workflow, canvas, values)
}

func classifyOutputs(outputs map[string]any, job store.ImageJob) []imagejob.MediaOutput {
	return imagejob.ClassifyOutputs(outputs, job.JobID)
}

func outputKind(name, mime, classType string) string {
	return imagejob.OutputKind(name, mime, classType)
}

func (c *v2Controller) imageJobs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	jobs, err := c.images.ListJobs()
	if err != nil {
		envelopeErr(w, http.StatusInternalServerError, codeJobFailed, err)
		return
	}
	for index := range jobs {
		jobs[index].PromptRaw = ""
	}
	envelope(w, http.StatusOK, 0, jobs, "")
}

func (c *v2Controller) job(w http.ResponseWriter, r *http.Request, rest string) {
	rest = strings.Trim(rest, "/")
	if strings.Contains(rest, "/outputs/") {
		c.jobOutput(w, r, rest)
		return
	}
	parts := strings.Split(rest, "/")
	if len(parts) == 0 || parts[0] == "" {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}
	job, err := c.images.LoadJob(id)
	if err != nil {
		envelopeErr(w, http.StatusNotFound, codeNotFound, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && action == "":
		envelope(w, http.StatusOK, 0, job, "")
	case r.Method == http.MethodGet && action == "outputs":
		envelope(w, http.StatusOK, 0, job.Outputs, "")
	case r.Method == http.MethodPost && action == "cancel":
		cancelled, cancelErr := c.svc.Cancel(id)
		if cancelErr != nil {
			c.writeImageServiceErr(w, cancelErr)
			return
		}
		envelope(w, http.StatusOK, 0, cancelled, "")
	case r.Method == http.MethodPost && action == "retry":
		var request struct {
			RegeneratePrompt bool `json:"regenerate_prompt"`
		}
		if r.Body != nil {
			_ = decodeBody(r, &request)
		}
		retried, retryErr := c.svc.Retry(id, request.RegeneratePrompt)
		if retryErr != nil {
			c.writeImageServiceErr(w, retryErr)
			return
		}
		envelope(w, http.StatusAccepted, 0, retried, "")
	default:
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
	}
}

func (c *v2Controller) jobOutput(w http.ResponseWriter, r *http.Request, rest string) {
	if r.Method != http.MethodGet {
		envelopeErr(w, http.StatusMethodNotAllowed, codeInvalidRequest, fmt.Errorf("method not allowed"))
		return
	}
	parts := strings.Split(strings.Trim(rest, "/"), "/outputs/")
	if len(parts) != 2 {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("output not found"))
		return
	}
	job, err := c.images.LoadJob(parts[0])
	if err != nil {
		envelopeErr(w, http.StatusNotFound, codeNotFound, err)
		return
	}
	index, err := strconv.Atoi(parts[1])
	if err != nil || index < 0 || index >= len(job.Outputs) {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("output not found"))
		return
	}
	output := job.Outputs[index]
	clean := filepath.Clean(filepath.FromSlash(output.StorageKey))
	root := filepath.Clean(c.rt.Dir())
	path := filepath.Join(root, clean)
	if output.StorageKey == "" || !strings.HasPrefix(filepath.Clean(path), root+string(os.PathSeparator)) {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("output file not found"))
		return
	}
	if _, err := os.Stat(path); err != nil {
		envelopeErr(w, http.StatusNotFound, codeNotFound, fmt.Errorf("output file not found"))
		return
	}
	mime := output.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	w.Header().Set("Content-Type", mime)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-API-Code", "0")
	http.ServeFile(w, r, path)
}

func (c *v2Controller) writeImageServiceErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, imagesvc.ErrNotFound):
		envelopeErr(w, http.StatusNotFound, codeNotFound, err)
	case errors.Is(err, imagesvc.ErrConfig):
		envelopeErr(w, http.StatusUnprocessableEntity, codeConfigInvalid, err)
	case errors.Is(err, imagesvc.ErrProvider):
		envelopeErr(w, http.StatusBadGateway, codeUnreachable, err)
	case errors.Is(err, imagesvc.ErrSave):
		envelopeErr(w, http.StatusInternalServerError, codeJobFailed, err)
	case errors.Is(err, imagesvc.ErrInvalidUnitIdentity), errors.Is(err, imagesvc.ErrInvalidGalgameIdentity), errors.Is(err, imagesvc.ErrInvalidPlayIdentity):
		envelopeErr(w, http.StatusBadRequest, codeInvalidRequest, err)
	default:
		envelopeErr(w, http.StatusInternalServerError, codeJobFailed, err)
	}
}
