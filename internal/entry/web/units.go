package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/imagejob"
	imagesvc "github.com/voocel/ainovel-cli/internal/imagejob/service"
	"github.com/voocel/ainovel-cli/internal/store"
)

// watchCompletedUnits bridges completed writing units without coupling the
// writing tools to the web controller. It is intentionally conservative:
// existing jobs are left for the normal retry controls, and the watcher stops
// with the host runtime.
func (c *v2Controller) watchCompletedUnits() {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-c.rt.Closed():
			return
		case <-ticker.C:
			bridge, err := c.media.LoadBridgeConfig()
			if err != nil || !bridge.Enabled || !bridge.AutoGenerate {
				continue
			}
			outline, err := c.st.Outline.LoadOutline()
			if err != nil {
				continue
			}
			jobs, err := c.media.ListJobs()
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
						if imagesvc.IsUnitJob(job) && job.Chapter == chapter.Chapter && job.Ordinal == ordinal {
							found = true
							break
						}
					}
					if found {
						continue
					}
					_, _ = c.startUnitImage(chapter.Chapter, ordinal, "", false)
				}
			}
		}
	}
}

func (c *v2Controller) unit(w http.ResponseWriter, r *http.Request, rest string) {
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 1 {
		ch, err := strconv.Atoi(parts[0])
		if err != nil || ch <= 0 {
			envelopeErr(w, 400, codeInvalidRequest, fmt.Errorf("invalid chapter"))
			return
		}
		jobs, _ := c.media.ListJobs()
		var filtered []store.ImageJob
		for _, j := range jobs {
			if imagesvc.IsUnitJob(j) && j.Chapter == ch {
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
		jobs, _ := c.media.ListJobs()
		for i := len(jobs) - 1; i >= 0; i-- {
			j := jobs[i]
			if imagesvc.IsUnitJob(j) && j.Chapter == ch && j.Ordinal == ord {
				envelope(w, 200, 0, j, "")
				return
			}
		}
		envelopeErr(w, 404, codeNotFound, fmt.Errorf("image job not found"))
		return
	}
	if strings.HasSuffix(rest, "/image/retry") {
		jobs, _ := c.media.ListJobs()
		for _, j := range jobs {
			if imagesvc.IsUnitJob(j) && j.Chapter == ch && j.Ordinal == ord {
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
	job, err := c.startUnitImage(chapter, ordinal, request.WorkflowID, request.Force)
	if err != nil {
		var conflict imagesvc.ConflictError
		if errors.As(err, &conflict) {
			envelope(w, 409, codeUnitJobConflict, conflict.Job, "同一个 unit 已有图片任务正在运行")
			return
		}
		if errors.Is(err, imagesvc.ErrInvalidUnitIdentity) {
			envelopeErr(w, 400, codeInvalidRequest, err)
			return
		}
		if strings.Contains(err.Error(), "桥接配置无法读取") {
			envelopeErr(w, 500, codeConfigInvalid, err)
			return
		}
		if strings.Contains(err.Error(), "尚未启用") {
			envelopeErr(w, 409, codeConflict, err)
			return
		}
		if strings.Contains(err.Error(), "不存在或为空") || strings.Contains(err.Error(), "计划不存在") || strings.Contains(err.Error(), "outside the chapter plan") {
			envelopeErr(w, 404, codeNotFound, err)
			return
		}
		if errors.Is(err, imagesvc.ErrSave) {
			envelopeErr(w, 500, codeJobFailed, err)
			return
		}
		envelope(w, 422, codePromptSchema, map[string]any{"valid": false}, err.Error())
		return
	}
	envelope(w, http.StatusAccepted, 0, job, "")
}

func (c *v2Controller) startUnitImage(chapter, ordinal int, workflowID string, force bool) (store.ImageJob, error) {
	bridge, err := c.media.LoadBridgeConfig()
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("图片生成桥接配置无法读取")
	}
	if !bridge.Enabled {
		return store.ImageJob{}, fmt.Errorf("图片生成桥接尚未启用")
	}
	workflowID = strings.TrimSpace(workflowID)
	if workflowID == "" {
		workflowID = bridge.WorkflowID
	}
	workflow, err := c.media.LoadWorkflow(workflowID)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("图片工作流不存在")
	}
	canvas, err := c.media.LoadOrCreateWorkflowCanvas(workflow.ID)
	if err != nil {
		return store.ImageJob{}, err
	}
	promptSchema, err := imagejob.BuildPromptSchema(workflow.ID, canvas)
	if err != nil {
		return store.ImageJob{}, err
	}
	if len(promptSchema.Fields) == 0 {
		return store.ImageJob{}, fmt.Errorf("请先在画布中勾选要发给提示词模型的字段")
	}
	promptRequest, err := c.unitPromptRequest(chapter, ordinal, bridge, promptSchema)
	if err != nil {
		return store.ImageJob{}, err
	}
	promptRequest.SystemPrompt = promptSchema.Composed
	return c.svc.StartUnit(chapter, ordinal, force, imagesvc.PromptRun{
		Request: promptRequest, Workflow: workflow, Canvas: canvas, Bridge: bridge, Schema: promptSchema,
	})
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

func tailText(value string, maximum int) string {
	runes := []rune(value)
	if maximum <= 0 || len(runes) <= maximum {
		return value
	}
	return string(runes[len(runes)-maximum:])
}
