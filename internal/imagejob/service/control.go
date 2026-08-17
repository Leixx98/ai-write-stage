package service

import (
	"context"
	"fmt"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func (s *Service) Cancel(jobID string) (store.ImageJob, error) {
	job, err := s.jobs.LoadJob(jobID)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	s.mu.Lock()
	if cancel := s.running[jobID]; cancel != nil {
		cancel()
	}
	s.mu.Unlock()
	if cfg, e := s.jobs.LoadConfig(); e == nil {
		if cl, e := comfyui.NewHTTPClient(cfg); e == nil {
			ictx, stop := context.WithTimeout(context.Background(), 2*time.Second)
			_ = cl.Interrupt(ictx)
			stop()
		}
	}
	job.Status = "cancelled"
	job.Error = "cancelled by user"
	if err := s.jobs.SaveJob(job); err != nil {
		return job, fmt.Errorf("%w: %v", ErrSave, err)
	}
	return job, nil
}

func (s *Service) Retry(jobID string, regeneratePrompt bool) (store.ImageJob, error) {
	job, err := s.jobs.LoadJob(jobID)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	if regeneratePrompt && job.Trigger == TriggerUnit {
		return s.retryUnitPrompt(job)
	}
	return s.retryBoundJob(job)
}

func (s *Service) retryUnitPrompt(job store.ImageJob) (store.ImageJob, error) {
	if job.Chapter <= 0 || job.Ordinal <= 0 {
		return job, ErrInvalidUnitIdentity
	}
	bridge, err := s.jobs.LoadBridgeConfig()
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	wf, err := s.jobs.LoadWorkflow(job.WorkflowID)
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrNotFound, err)
	}
	canvas, err := s.jobs.LoadOrCreateWorkflowCanvas(wf.ID)
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	schema, err := imagejob.BuildPromptSchema(wf.ID, canvas)
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	if len(schema.Fields) == 0 {
		return job, fmt.Errorf("%w: 请先在画布中勾选要发给提示词模型的字段", ErrSchema)
	}
	if s.loadUnitPrompt == nil {
		return job, fmt.Errorf("%w: unit prompt loader is not configured", ErrSchema)
	}
	request, err := s.loadUnitPrompt(job.Chapter, job.Ordinal, bridge, schema)
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrSchema, err)
	}
	request.SystemPrompt = schema.Composed
	job.Status = "prompting"
	job.Stage = "prompting"
	job.Attempt++
	job.Error = ""
	job.PromptRaw = ""
	job.PromptValues = nil
	job.FinishedAt = nil
	job.StartedAt = time.Now().UTC()
	if err := s.jobs.SaveJob(job); err != nil {
		return job, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchPrompt(job, PromptRun{Request: request, Workflow: wf, Canvas: canvas, Bridge: bridge, Schema: schema})
	return job, nil
}

func (s *Service) retryBoundJob(job store.ImageJob) (store.ImageJob, error) {
	job.Status = "pending"
	job.Stage = "binding"
	job.Attempt++
	job.Error = ""
	wf, err := s.jobs.LoadWorkflow(job.WorkflowID)
	if err != nil {
		return job, fmt.Errorf("%w: workflow for retry not found", ErrNotFound)
	}
	comfyui.NormalizeWorkflowConfig(&wf)
	canvas, _ := s.jobs.LoadOrCreateWorkflowCanvas(wf.ID)
	values := map[string]any{}
	for key, value := range job.PromptValues {
		values[key] = value
	}
	if len(values) == 0 {
		if job.Prompt != "" {
			values["text"] = job.Prompt
		}
		if job.NegativePrompt != "" {
			values["negative_prompt"] = job.NegativePrompt
		}
	}
	for k, v := range job.Parameters {
		values[k] = v
	}
	for _, input := range job.Inputs {
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
	if err := imagejob.MergeCanvasRuntime(&wf, canvas, values); err != nil {
		return job, fmt.Errorf("%w: %v", ErrBind, err)
	}
	bound, err := comfyui.ApplyBindings(wf, values)
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrBind, err)
	}
	cfg, err := s.jobs.LoadConfig()
	if err != nil {
		return job, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	if job.InstanceID != "" {
		if instances, _, ie := s.jobs.LoadInstances(); ie == nil {
			for _, inst := range instances {
				if inst.ID == job.InstanceID {
					cfg.BaseURL = inst.BaseURL
					break
				}
			}
		}
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return job, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchJob(job, bound, cfg, wf)
	return job, nil
}
