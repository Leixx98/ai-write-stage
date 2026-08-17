package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func (s *Service) launchJob(job store.ImageJob, bound map[string]any, cfg comfyui.Config, wf comfyui.Workflow) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[job.JobID] = cancel
	s.mu.Unlock()
	go s.runJob(ctx, job, bound, cfg, wf)
}

func (s *Service) launchPrompt(job store.ImageJob, run PromptRun) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[job.JobID] = cancel
	s.mu.Unlock()
	go s.runPrompt(ctx, job, run)
}

func (s *Service) runPrompt(ctx context.Context, job store.ImageJob, run PromptRun) {
	defer func() {
		s.mu.Lock()
		delete(s.running, job.JobID)
		s.mu.Unlock()
	}()
	if s.prompter == nil {
		s.failJob(job, "failed", "prompting", "Prompter is unavailable", true)
		return
	}
	promptCtx, cancel := context.WithTimeout(ctx, time.Duration(run.Bridge.PrompterTimeoutMS)*time.Millisecond)
	raw, err := s.prompter.Generate(promptCtx, run.Request)
	promptTimedOut := errors.Is(promptCtx.Err(), context.DeadlineExceeded)
	cancel()
	if err != nil {
		if promptTimedOut {
			s.failJob(job, "failed", "prompting", "image prompt generation timed out", true)
		} else if ctx.Err() != nil {
			s.failJob(job, "cancelled", "prompting", "图片提示词生成已取消", true)
		} else {
			s.failJob(job, "failed", "prompting", err.Error(), true)
		}
		return
	}
	job.PromptRaw = truncateBytes(raw, 64<<10)
	job.Status = "validating"
	job.Stage = "validating"
	_ = s.jobs.SaveJob(job)
	parsed, err := imagejob.ParseAndValidate(raw, run.Schema, run.Bridge.Strict)
	if err != nil {
		s.failJob(job, "failed", "validating", err.Error(), true)
		return
	}
	if ctx.Err() != nil {
		s.failJob(job, "cancelled", "validating", "image generation cancelled", true)
		return
	}
	job.PromptValues = parsed.Values
	job.Status = "binding"
	job.Stage = "binding"
	_ = s.jobs.SaveJob(job)
	values := make(map[string]any, len(parsed.Values))
	for key, value := range parsed.Values {
		values[key] = value
	}
	workflow := run.Workflow
	if err := imagejob.MergeCanvasRuntime(&workflow, run.Canvas, values); err != nil {
		s.failJob(job, "failed", "binding", err.Error(), true)
		return
	}
	if ctx.Err() != nil {
		s.failJob(job, "cancelled", "binding", "image generation cancelled", true)
		return
	}
	bound, err := comfyui.ApplyBindings(workflow, values)
	if err != nil {
		s.failJob(job, "failed", "binding", err.Error(), true)
		return
	}
	snapshotKey, err := s.jobs.SaveJobWorkflow(job.JobID, bound)
	if err != nil {
		s.failJob(job, "failed", "binding", "cannot save workflow snapshot", true)
		return
	}
	job.SnapshotKey = snapshotKey
	job.Recoverable = true
	_ = s.jobs.SaveJob(job)
	config, err := s.jobs.LoadConfig()
	if err != nil {
		s.failJob(job, "failed", "binding", "ComfyUI 配置无法读取", true)
		return
	}
	if workflow.InstanceID != "" {
		job.InstanceID = workflow.InstanceID
		if instances, _, loadErr := s.jobs.LoadInstances(); loadErr == nil {
			for _, instance := range instances {
				if instance.ID == workflow.InstanceID && instance.Enabled {
					config.BaseURL = instance.BaseURL
					break
				}
			}
		}
	}
	s.runJob(ctx, job, bound, config, workflow)
}

func (s *Service) runJob(ctx context.Context, job store.ImageJob, wfMap map[string]any, cfg comfyui.Config, wf comfyui.Workflow) {
	defer func() {
		s.mu.Lock()
		delete(s.running, job.JobID)
		s.mu.Unlock()
	}()
	cl, err := comfyui.NewHTTPClient(cfg)
	if err == nil {
		ctx2, cancel := context.WithTimeout(ctx, cfg.Timeout())
		defer cancel()
		job.Status = "submitting"
		job.Stage = "submitting"
		_ = s.jobs.SaveJob(job)
		var pid string
		pid, err = cl.Submit(ctx2, wfMap, cfg.ClientID)
		if err == nil {
			job.PromptID = pid
			job.Status = "queued"
			job.Stage = "queued"
			_ = s.jobs.SaveJob(job)
			var h comfyui.History
			h, err = cl.Wait(ctx2, pid, cfg.PollInterval())
			if err == nil {
				job.Status = "running"
				job.Stage = "running"
				job.Outputs = imagejob.ClassifyOutputs(h.Outputs, job.JobID)
				_ = s.jobs.SaveJob(job)
				ref, ok := imagejob.FirstOutput(h.Outputs, wf.Output)
				if !ok {
					err = fmt.Errorf("ComfyUI returned no image output")
				} else {
					img, e := cl.Download(ctx2, ref, cfg.MaxResponseBytes)
					if e != nil {
						err = e
					} else {
						path := ImagePath(s.root, job)
						_ = os.MkdirAll(filepath.Dir(path), 0755)
						err = os.WriteFile(path, img.Data, 0644)
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
	_ = s.jobs.SaveJob(job)
}

func (s *Service) failJob(job store.ImageJob, status, stage, message string, recoverable bool) {
	job.Status = status
	job.Stage = stage
	job.Error = truncateBytes(message, 2048)
	job.Recoverable = recoverable
	now := time.Now().UTC()
	job.FinishedAt = &now
	_ = s.jobs.SaveJob(job)
}
