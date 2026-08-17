package service

import (
	"context"
	"errors"
	"sync"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
	"github.com/voocel/ainovel-cli/internal/store"
)

const (
	TriggerTest    = "test"
	TriggerUnit    = "unit"
	TriggerGalgame = "galgame"
)

var (
	ErrInvalidUnitIdentity    = errors.New("unit image requires chapter > 0 and ordinal > 0")
	ErrInvalidGalgameIdentity = errors.New("galgame image requires a session id and must not set chapter or ordinal")
	ErrUnitBusy               = errors.New("unit image job already running")
	ErrNotFound               = errors.New("image job not found")
	ErrWorkflow               = errors.New("workflow")
	ErrSchema                 = errors.New("prompt schema")
	ErrConfig                 = errors.New("config")
	ErrBind                   = errors.New("workflow bind")
	ErrSave                   = errors.New("job save")
)

type ConflictError struct {
	Job store.ImageJob
}

func (e ConflictError) Error() string { return ErrUnitBusy.Error() }
func (e ConflictError) Unwrap() error { return ErrUnitBusy }

type PromptRun struct {
	Request  imagejob.PromptRequest
	Workflow comfyui.Workflow
	Canvas   comfyui.CanvasDocument
	Bridge   imagejob.BridgeConfig
	Schema   imagejob.PromptSchema
}

type UnitPromptLoader func(chapter, ordinal int, bridge imagejob.BridgeConfig, schema imagejob.PromptSchema) (imagejob.PromptRequest, error)

type Config struct {
	Root           string
	Store          *store.ComfyUIStore
	Prompter       imagejob.Prompter
	LoadUnitPrompt UnitPromptLoader
}

type Service struct {
	root           string
	jobs           *store.ComfyUIStore
	prompter       imagejob.Prompter
	loadUnitPrompt UnitPromptLoader
	mu             sync.Mutex
	running        map[string]context.CancelFunc
}

func New(cfg Config) *Service {
	return &Service{
		root:           cfg.Root,
		jobs:           cfg.Store,
		prompter:       cfg.Prompter,
		loadUnitPrompt: cfg.LoadUnitPrompt,
		running:        map[string]context.CancelFunc{},
	}
}

func TerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "timeout", "cancelled":
		return true
	default:
		return false
	}
}

func IsUnitJob(job store.ImageJob) bool {
	if job.Trigger == TriggerUnit {
		return true
	}
	return job.Trigger == "" && job.Chapter > 0 && job.Ordinal > 0
}
