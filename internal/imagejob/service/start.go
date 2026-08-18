package service

import (
	"fmt"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

func (s *Service) StartTest(job store.ImageJob, bound map[string]any, cfg comfyui.Config, wf comfyui.Workflow) (store.ImageJob, error) {
	if job.Trigger == "" {
		job.Trigger = TriggerTest
	}
	if job.Trigger != TriggerTest {
		return job, fmt.Errorf("StartTest requires trigger %s", TriggerTest)
	}
	if job.JobID == "" {
		job.JobID = store.NewImageJobID()
	}
	if snapshotKey, err := s.jobs.SaveJobWorkflow(job.JobID, bound); err == nil {
		job.SnapshotKey = snapshotKey
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return job, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchJob(job, bound, cfg, wf)
	return job, nil
}

func (s *Service) StartUnit(chapter, ordinal int, force bool, run PromptRun) (store.ImageJob, error) {
	if chapter <= 0 || ordinal <= 0 {
		return store.ImageJob{}, ErrInvalidUnitIdentity
	}
	workflowHash, err := hashJSON(run.Workflow.Workflow)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: cannot hash workflow", ErrSchema)
	}
	idempotencyKey := UnitIdempotencyKey(run.Request, run.Workflow.ID, workflowHash, run.Schema.SchemaHash, imagejob.PromptFingerprint(run.Request.SystemPrompt))
	jobs, err := s.jobs.ListJobs()
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrSave, err)
	}
	for index := len(jobs) - 1; index >= 0; index-- {
		existing := jobs[index]
		if !IsUnitJob(existing) || existing.Chapter != chapter || existing.Ordinal != ordinal || TerminalStatus(existing.Status) {
			continue
		}
		return existing, ConflictError{Job: existing}
	}
	if !force {
		if existing, found, findErr := s.jobs.FindJobByIdempotencyKey(idempotencyKey); findErr == nil && found {
			return existing, nil
		}
	} else {
		idempotencyKey += fmt.Sprintf(":attempt:%d", time.Now().UnixNano())
	}
	job := store.ImageJob{
		JobID: store.NewImageJobID(), UnitID: run.Request.UnitID, Chapter: chapter, Ordinal: ordinal,
		WorkflowID: run.Workflow.ID, Status: "prompting", Stage: "prompting", Attempt: 1,
		IdempotencyKey: idempotencyKey, Trigger: TriggerUnit, SchemaHash: run.Schema.SchemaHash,
		WorkflowHash: workflowHash, Recoverable: true, StartedAt: time.Now().UTC(),
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchPrompt(job, run)
	return job, nil
}

func (s *Service) StartGalgame(sessionID string, run PromptRun) (store.ImageJob, error) {
	sessionID = safeSessionID(sessionID)
	if sessionID == "" || run.Request.Chapter != 0 || run.Request.Ordinal != 0 {
		return store.ImageJob{}, ErrInvalidGalgameIdentity
	}
	workflowHash, err := hashJSON(run.Workflow.Workflow)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: cannot hash workflow", ErrSchema)
	}
	idempotencyKey := GalgameIdempotencyKey(sessionID, run.Request, run.Workflow.ID, workflowHash, run.Schema.SchemaHash, imagejob.PromptFingerprint(run.Request.SystemPrompt))
	if existing, found, findErr := s.jobs.FindJobByIdempotencyKey(idempotencyKey); findErr == nil && found {
		return existing, nil
	}
	job := store.ImageJob{
		JobID: store.NewImageJobID(), UnitID: run.Request.UnitID, SessionID: sessionID, WorkflowID: run.Workflow.ID,
		Status: "prompting", Stage: "prompting", Attempt: 1, IdempotencyKey: idempotencyKey,
		Trigger: TriggerGalgame, SchemaHash: run.Schema.SchemaHash, WorkflowHash: workflowHash,
		Recoverable: true, StartedAt: time.Now().UTC(),
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchPrompt(job, run)
	return job, nil
}

func (s *Service) StartPlayBeat(playID string, ordinal int, run PromptRun) (store.ImageJob, error) {
	playID = safeSessionID(playID)
	if playID == "" || ordinal < 1 || run.Request.Chapter != 0 {
		return store.ImageJob{}, ErrInvalidPlayIdentity
	}
	workflowHash, err := hashJSON(run.Workflow.Workflow)
	if err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: cannot hash workflow", ErrSchema)
	}
	idempotencyKey := PlayIdempotencyKey(playID, ordinal, run.Request, run.Workflow.ID, workflowHash, run.Schema.SchemaHash, imagejob.PromptFingerprint(run.Request.SystemPrompt))
	if existing, found, findErr := s.jobs.FindJobByIdempotencyKey(idempotencyKey); findErr == nil && found {
		return existing, nil
	}
	job := store.ImageJob{
		JobID: store.NewImageJobID(), UnitID: run.Request.UnitID, PlayID: playID, Ordinal: ordinal, WorkflowID: run.Workflow.ID,
		Status: "prompting", Stage: "prompting", Attempt: 1, IdempotencyKey: idempotencyKey,
		Trigger: TriggerPlay, SchemaHash: run.Schema.SchemaHash, WorkflowHash: workflowHash,
		Recoverable: true, StartedAt: time.Now().UTC(),
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return store.ImageJob{}, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launchPrompt(job, run)
	return job, nil
}
