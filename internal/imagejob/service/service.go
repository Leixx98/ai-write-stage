package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/imagejob"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

const (
	TriggerTest    = "test"
	TriggerUnit    = "unit"
	TriggerGalgame = "galgame"
	TriggerPlay    = "play"
)

var (
	ErrInvalidUnitIdentity    = errors.New("unit image requires chapter > 0 and ordinal > 0")
	ErrInvalidGalgameIdentity = errors.New("chat image requires a session id")
	ErrInvalidPlayIdentity    = errors.New("play image requires a play id and beat ordinal")
	ErrUnitBusy               = errors.New("unit image job already running")
	ErrNotFound               = errors.New("image job not found")
	ErrConfig                 = errors.New("image generation config")
	ErrSave                   = errors.New("job save")
	ErrProvider               = errors.New("image provider")
)

type ConflictError struct{ Job store.ImageJob }

func (e ConflictError) Error() string { return ErrUnitBusy.Error() }
func (e ConflictError) Unwrap() error { return ErrUnitBusy }

type GeneratedOutput struct {
	Media imagejob.MediaOutput
	Data  []byte
}

type Reporter interface {
	Stage(status, stage string)
	ExternalID(id string)
	Progress(current, total int, node string)
	Prompt(raw string, values map[string]any)
	ProviderSnapshot(snapshot any)
}

type Provider interface {
	Info() imagejob.ProviderInfo
	ValidateProfile(imagejob.Profile) error
	Execute(context.Context, imagejob.ProviderRequest, Reporter) ([]GeneratedOutput, error)
	Cancel(context.Context, string) error
	TestConnection(context.Context) error
}

type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry(providers ...Provider) *Registry {
	registry := &Registry{providers: map[string]Provider{}}
	for _, provider := range providers {
		registry.Register(provider)
	}
	return registry
}

func (r *Registry) Register(provider Provider) {
	if r == nil || provider == nil {
		return
	}
	id := strings.ToLower(strings.TrimSpace(provider.Info().ID))
	if id == "" {
		return
	}
	r.mu.Lock()
	r.providers[id] = provider
	r.mu.Unlock()
}

func (r *Registry) Provider(id string) (Provider, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	provider, ok := r.providers[strings.ToLower(strings.TrimSpace(id))]
	r.mu.RUnlock()
	return provider, ok
}

func (r *Registry) List() []imagejob.ProviderInfo {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	providers := make([]imagejob.ProviderInfo, 0, len(r.providers))
	for _, provider := range r.providers {
		providers = append(providers, provider.Info())
	}
	r.mu.RUnlock()
	sort.Slice(providers, func(i, j int) bool { return providers[i].ID < providers[j].ID })
	return providers
}

type StageObserver func(job store.ImageJob, prevStatus, prevStage string)

type Config struct {
	Root          string
	Jobs          *store.ImageStore
	Configuration *store.ImageConfigStore
	Providers     *Registry
	StageObserver StageObserver
}

type Service struct {
	root      string
	jobs      *store.ImageStore
	config    *store.ImageConfigStore
	providers *Registry
	observer  StageObserver
	mu        sync.Mutex
	running   map[string]context.CancelFunc
}

func New(cfg Config) *Service {
	return &Service{
		root: cfg.Root, jobs: cfg.Jobs, config: cfg.Configuration,
		providers: cfg.Providers, observer: cfg.StageObserver,
		running: map[string]context.CancelFunc{},
	}
}

func (s *Service) Providers() []imagejob.ProviderInfo { return s.providers.List() }

func (s *Service) ValidateProfile(profile imagejob.Profile) error {
	profile = imagejob.NormalizeProfile(profile)
	provider, ok := s.providers.Provider(profile.Provider)
	if !ok {
		return fmt.Errorf("%w: provider %q is not registered", ErrProvider, profile.Provider)
	}
	return provider.ValidateProfile(profile)
}

func (s *Service) TestProvider(ctx context.Context, id string) error {
	provider, ok := s.providers.Provider(id)
	if !ok {
		return fmt.Errorf("%w: provider %q is not registered", ErrProvider, id)
	}
	return provider.TestConnection(ctx)
}

func (s *Service) Start(request imagejob.SceneImageRequest) (store.ImageJob, bool, error) {
	if s.jobs == nil || s.config == nil || s.providers == nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: gateway is not configured", ErrConfig)
	}
	if err := validateRequest(request); err != nil {
		return store.ImageJob{}, false, err
	}
	settings, err := s.config.LoadSettings()
	if err != nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: %v", ErrConfig, err)
	}
	sceneCfg, ok := imagejob.SceneSettings(settings, request.Scene)
	if !ok {
		return store.ImageJob{}, false, fmt.Errorf("unknown image scene %q", request.Scene)
	}
	if !shouldGenerate(request, sceneCfg) {
		return store.ImageJob{Scene: request.Scene, SceneID: request.SceneID, Status: "skipped", Stage: "skipped", RequestSnapshot: request}, true, nil
	}
	profileID := strings.TrimSpace(request.ProfileID)
	if profileID == "" {
		profileID = sceneCfg.DefaultProfileID
	}
	if profileID == "" {
		return store.ImageJob{}, false, fmt.Errorf("%w: no profile configured for %s", ErrConfig, request.Scene)
	}
	profile, err := s.config.LoadProfile(profileID)
	if err != nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: profile %q not found", ErrConfig, profileID)
	}
	provider, found := s.providers.Provider(profile.Provider)
	if !found {
		return store.ImageJob{}, false, fmt.Errorf("%w: provider %q is not registered", ErrProvider, profile.Provider)
	}
	if !provider.Info().Enabled {
		return store.ImageJob{}, false, fmt.Errorf("%w: provider %q is disabled", ErrProvider, profile.Provider)
	}
	if err := provider.ValidateProfile(profile); err != nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: %v", ErrConfig, err)
	}

	idempotencyKey, err := requestKey(request, profile)
	if err != nil {
		return store.ImageJob{}, false, err
	}
	if request.Force {
		idempotencyKey += fmt.Sprintf(":attempt:%d", time.Now().UnixNano())
	} else if existing, found, findErr := s.jobs.FindJobByIdempotencyKey(idempotencyKey); findErr == nil && found {
		return existing, false, nil
	}
	jobs, err := s.jobs.ListJobs()
	if err != nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: %v", ErrSave, err)
	}
	for index := len(jobs) - 1; index >= 0; index-- {
		existing := jobs[index]
		if sameBusyIdentity(existing, request) && !TerminalStatus(existing.Status) {
			return existing, false, ConflictError{Job: existing}
		}
	}

	request.ProfileID = profile.ID
	job := store.ImageJob{
		JobID: store.NewImageJobID(), Scene: request.Scene, SceneID: request.SceneID,
		UnitID: request.UnitID, Chapter: request.Chapter, Ordinal: request.Ordinal,
		ProfileID: profile.ID, Provider: profile.Provider, Status: "prompting", Stage: "prompting",
		Attempt: 1, IdempotencyKey: idempotencyKey, Trigger: triggerForScene(request.Scene),
		RequestSnapshot: request, Recoverable: true, StartedAt: time.Now().UTC(),
	}
	if request.Scene == imagejob.SceneChat {
		job.SessionID = request.SceneID
	}
	if request.Scene == imagejob.ScenePlay {
		job.PlayID = request.SceneID
	}
	profileSnapshot, _ := json.Marshal(profile)
	job.ProviderSnapshot = profileSnapshot
	if err := s.jobs.SaveJob(job); err != nil {
		return store.ImageJob{}, false, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.notify(job, "", "")
	s.launch(job, profile, provider)
	return job, false, nil
}

// sameBusyIdentity matches the old per-unit lock: one in-flight job per
// novel unit, chat message, or play beat. Scene+SceneID alone is too wide
// for play/chat because SceneID is the play/session, not the beat/message.
func sameBusyIdentity(existing store.ImageJob, request imagejob.SceneImageRequest) bool {
	if existing.Scene != request.Scene {
		return false
	}
	switch request.Scene {
	case imagejob.SceneNovel:
		return existing.Chapter == request.Chapter && existing.Ordinal == request.Ordinal
	case imagejob.SceneChat:
		return existing.SceneID == request.SceneID && existing.UnitID == request.UnitID
	case imagejob.ScenePlay:
		return existing.SceneID == request.SceneID && existing.Ordinal == request.Ordinal
	default:
		return existing.SceneID == request.SceneID
	}
}

func validateRequest(request imagejob.SceneImageRequest) error {
	request.SceneID = strings.TrimSpace(request.SceneID)
	switch request.Scene {
	case imagejob.SceneNovel:
		if request.Chapter <= 0 || request.Ordinal <= 0 {
			return ErrInvalidUnitIdentity
		}
	case imagejob.SceneChat:
		if request.SceneID == "" {
			return ErrInvalidGalgameIdentity
		}
	case imagejob.ScenePlay:
		if request.SceneID == "" || request.Ordinal <= 0 {
			return ErrInvalidPlayIdentity
		}
	default:
		return fmt.Errorf("unknown image scene %q", request.Scene)
	}
	return nil
}

func shouldGenerate(request imagejob.SceneImageRequest, cfg imagejob.SceneConfig) bool {
	if request.Scene == imagejob.SceneChat {
		return imagejob.ShouldGenerateChat(cfg, request.AssistantIndex, request.Manual)
	}
	if !cfg.Enabled {
		return false
	}
	return request.Manual || cfg.AutoGenerate
}

func triggerForScene(scene imagejob.Scene) string {
	switch scene {
	case imagejob.SceneNovel:
		return TriggerUnit
	case imagejob.SceneChat:
		return TriggerGalgame
	case imagejob.ScenePlay:
		return TriggerPlay
	default:
		return TriggerTest
	}
}

func requestKey(request imagejob.SceneImageRequest, profile imagejob.Profile) (string, error) {
	request.Force = false
	payload := struct {
		Request imagejob.SceneImageRequest `json:"request"`
		Profile imagejob.Profile           `json:"profile"`
	}{request, profile}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("hash image request: %w", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (s *Service) launch(job store.ImageJob, profile imagejob.Profile, provider Provider) {
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.running[job.JobID] = cancel
	s.mu.Unlock()
	go s.run(ctx, job, profile, provider)
}

func (s *Service) run(ctx context.Context, job store.ImageJob, profile imagejob.Profile, provider Provider) {
	defer func() {
		s.mu.Lock()
		delete(s.running, job.JobID)
		s.mu.Unlock()
	}()
	reporter := &jobReporter{service: s, job: &job}
	request := imagejob.ProviderRequest{JobID: job.JobID, SceneRequest: job.RequestSnapshot, Profile: profile, ProviderSnapshot: job.ProviderSnapshot}
	outputs, err := provider.Execute(ctx, request, reporter)
	prevStatus, prevStage := job.Status, job.Stage
	if err == nil {
		if len(outputs) == 0 {
			err = errors.New("provider returned no image output")
		} else {
			err = s.persistOutputs(&job, outputs)
		}
	}
	if err == nil {
		job.Status, job.Stage, job.Error = "completed", "completed", ""
	} else if errors.Is(err, context.DeadlineExceeded) {
		job.Status, job.Stage, job.Error = "timeout", "timeout", err.Error()
	} else if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		job.Status, job.Stage, job.Error = "cancelled", "cancelled", "image generation cancelled"
	} else {
		job.Status, job.Stage, job.Error = "failed", "failed", truncateBytes(err.Error(), 2048)
	}
	now := time.Now().UTC()
	job.FinishedAt = &now
	_ = s.jobs.SaveJob(job)
	s.notify(job, prevStatus, prevStage)
}

func (s *Service) persistOutputs(job *store.ImageJob, generated []GeneratedOutput) error {
	outputs := make([]imagejob.MediaOutput, 0, len(generated))
	for index, generatedOutput := range generated {
		output := generatedOutput.Media
		if output.Kind == "" {
			output.Kind = "image"
		}
		if output.MIME == "" {
			output.MIME = "image/png"
		}
		ext := extensionForMIME(output.MIME)
		rel := filepath.ToSlash(filepath.Join(store.NovelDirName, "meta/image-generation/outputs", job.JobID, fmt.Sprintf("%03d%s", index, ext)))
		path := filepath.Join(s.root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return err
		}
		if err := os.WriteFile(path, generatedOutput.Data, 0644); err != nil {
			return err
		}
		output.StorageKey = rel
		output.Size = int64(len(generatedOutput.Data))
		output.URL = fmt.Sprintf("/api/v2/image-jobs/%s/outputs/%d", job.JobID, index)
		output.Previewable = output.Kind == "image"
		outputs = append(outputs, output)
		if index == 0 {
			businessPath := ImagePath(s.root, *job)
			if err := os.MkdirAll(filepath.Dir(businessPath), 0755); err == nil {
				_ = os.WriteFile(businessPath, generatedOutput.Data, 0644)
			}
			job.Output = map[string]any{"filename": filepath.Base(businessPath), "mime": output.MIME, "kind": output.Kind, "url": output.URL, "preview_url": output.URL}
		}
	}
	job.Outputs = outputs
	return nil
}

func extensionForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0])) {
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ".png"
	}
}

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
	if provider, ok := s.providers.Provider(job.Provider); ok && job.ExternalJobID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = provider.Cancel(ctx, job.ExternalJobID)
		cancel()
	}
	job.Status, job.Stage, job.Error = "cancelled", "cancelled", "cancelled by user"
	now := time.Now().UTC()
	job.FinishedAt = &now
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
	profile, err := s.config.LoadProfile(job.ProfileID)
	if err != nil {
		return job, fmt.Errorf("%w: profile %q not found", ErrConfig, job.ProfileID)
	}
	provider, ok := s.providers.Provider(job.Provider)
	if !ok {
		return job, fmt.Errorf("%w: provider %q is not registered", ErrProvider, job.Provider)
	}
	job.Attempt++
	job.Status, job.Stage, job.Error = "prompting", "prompting", ""
	job.FinishedAt = nil
	job.StartedAt = time.Now().UTC()
	if regeneratePrompt {
		job.PromptRaw = ""
		job.PromptValues = nil
	}
	if err := s.jobs.SaveJob(job); err != nil {
		return job, fmt.Errorf("%w: %v", ErrSave, err)
	}
	s.launch(job, profile, provider)
	return job, nil
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
	return job.Scene == imagejob.SceneNovel || job.Trigger == TriggerUnit
}

func (s *Service) notify(job store.ImageJob, prevStatus, prevStage string) {
	if s.observer != nil {
		s.observer(job, prevStatus, prevStage)
	}
}

type jobReporter struct {
	service *Service
	job     *store.ImageJob
}

func (r *jobReporter) Stage(status, stage string) {
	prevStatus, prevStage := r.job.Status, r.job.Stage
	r.job.Status, r.job.Stage = status, stage
	_ = r.service.jobs.SaveJob(*r.job)
	r.service.notify(*r.job, prevStatus, prevStage)
}

func (r *jobReporter) ExternalID(id string) {
	r.job.ExternalJobID = strings.TrimSpace(id)
	_ = r.service.jobs.SaveJob(*r.job)
}

func (r *jobReporter) Progress(current, total int, node string) {
	r.job.ProgressCurrent, r.job.ProgressTotal, r.job.ProgressNode = current, total, node
	_ = r.service.jobs.SaveJob(*r.job)
}

func (r *jobReporter) Prompt(raw string, values map[string]any) {
	r.job.PromptRaw = truncateBytes(raw, 64<<10)
	r.job.PromptValues = values
	_ = r.service.jobs.SaveJob(*r.job)
}

func (r *jobReporter) ProviderSnapshot(snapshot any) {
	data, err := json.Marshal(snapshot)
	if err != nil {
		return
	}
	r.job.ProviderSnapshot = data
	if key, err := r.service.jobs.SaveProviderSnapshot(r.job.JobID, snapshot); err == nil {
		r.job.SnapshotKey = key
	}
	_ = r.service.jobs.SaveJob(*r.job)
}
