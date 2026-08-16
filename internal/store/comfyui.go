package store

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
	"github.com/voocel/ainovel-cli/internal/imagejob"
)

type MediaRef struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	StorageKey string `json:"storage_key"`
	UploadName string `json:"upload_name"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	InstanceID string `json:"instance_id,omitempty"`
}

type ImageJob struct {
	JobID          string                `json:"job_id"`
	UnitID         string                `json:"unit_id"`
	Chapter        int                   `json:"chapter"`
	Ordinal        int                   `json:"ordinal"`
	WorkflowID     string                `json:"workflow_id"`
	InstanceID     string                `json:"instance_id,omitempty"`
	Status         string                `json:"status"`
	Stage          string                `json:"stage,omitempty"`
	Attempt        int                   `json:"attempt"`
	IdempotencyKey string                `json:"idempotency_key,omitempty"`
	Trigger        string                `json:"trigger,omitempty"`
	SchemaHash     string                `json:"schema_hash,omitempty"`
	WorkflowHash   string                `json:"workflow_hash,omitempty"`
	PromptRaw      string                `json:"prompt_raw,omitempty"`
	PromptValues   map[string]any        `json:"prompt_values,omitempty"`
	SnapshotKey    string                `json:"snapshot_key,omitempty"`
	Recoverable    bool                  `json:"recoverable,omitempty"`
	PromptID       string                `json:"prompt_id,omitempty"`
	Prompt         string                `json:"prompt,omitempty"`
	NegativePrompt string                `json:"negative_prompt,omitempty"`
	Parameters     map[string]any        `json:"parameters,omitempty"`
	Inputs         []map[string]any      `json:"inputs,omitempty"`
	Output         map[string]any        `json:"output,omitempty"`
	Outputs        []comfyui.MediaOutput `json:"outputs,omitempty"`
	Error          string                `json:"error,omitempty"`
	StartedAt      time.Time             `json:"started_at,omitempty"`
	FinishedAt     *time.Time            `json:"finished_at,omitempty"`
}

type ComfyUIStore struct {
	io        *IO // workspace data: jobs, media and legacy fallback
	projectIO *IO // project data: workflow definitions and shared settings
	mu        sync.Mutex
}

func NewComfyUIStore(io *IO) *ComfyUIStore { return NewComfyUIStoreWithProject(io, io) }
func NewComfyUIStoreWithProject(io, projectIO *IO) *ComfyUIStore {
	if projectIO == nil {
		projectIO = io
	}
	return &ComfyUIStore{io: io, projectIO: projectIO}
}

func (s *ComfyUIStore) readProjectJSON(rel string, value any) error {
	err := s.projectIO.ReadJSON(rel, value)
	if os.IsNotExist(err) && s.projectIO != s.io {
		legacyRel := filepath.ToSlash(filepath.Join("meta/comfyui", strings.TrimPrefix(rel, "comfyui/")))
		return s.io.ReadJSON(legacyRel, value)
	}
	return err
}
func (s *ComfyUIStore) writeProjectJSON(rel string, value any) error {
	return s.projectIO.WriteJSON(rel, value)
}
func safeComfyID(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`) && id != "." && id != ".."
}
func (s *ComfyUIStore) LoadConfig() (comfyui.Config, error) {
	c := comfyui.DefaultConfig()
	data, err := s.io.ReadFile("meta/comfyui/config.json")
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return c, err
	}
	return comfyui.NormalizeConfig(c), nil
}
func (s *ComfyUIStore) SaveConfig(c comfyui.Config) error {
	return s.io.WriteJSON("meta/comfyui/config.json", comfyui.NormalizeConfig(c))
}

func (s *ComfyUIStore) LoadBridgeConfig() (imagejob.BridgeConfig, error) {
	config := imagejob.DefaultBridgeConfig()
	err := s.readProjectJSON("comfyui/bridge.json", &config)
	if os.IsNotExist(err) {
		return config, nil
	}
	if err != nil {
		return config, err
	}
	return imagejob.NormalizeBridgeConfig(config), nil
}

func (s *ComfyUIStore) SaveBridgeConfig(config imagejob.BridgeConfig) error {
	config = imagejob.NormalizeBridgeConfig(config)
	if err := imagejob.ValidateBridgeConfig(config); err != nil {
		return err
	}
	return s.writeProjectJSON("comfyui/bridge.json", config)
}

func (s *ComfyUIStore) LoadPrompterPresets() (imagejob.PrompterPresetDocument, error) {
	doc := imagejob.PrompterPresetDocument{Version: 1, Presets: map[string]imagejob.PrompterPreset{}}
	err := s.readProjectJSON("comfyui/prompter_presets.json", &doc)
	if os.IsNotExist(err) {
		return doc, nil
	}
	if err != nil {
		return doc, err
	}
	if doc.Presets == nil {
		doc.Presets = map[string]imagejob.PrompterPreset{}
	}
	return doc, nil
}

func (s *ComfyUIStore) SavePrompterPresets(doc imagejob.PrompterPresetDocument) error {
	doc.Version = 1
	if doc.Presets == nil {
		doc.Presets = map[string]imagejob.PrompterPreset{}
	}
	return s.writeProjectJSON("comfyui/prompter_presets.json", doc)
}
func (s *ComfyUIStore) instancesPath() string { return "meta/comfyui/instances.json" }
func (s *ComfyUIStore) LoadInstances() ([]comfyui.Instance, comfyui.InstanceSettings, error) {
	var doc struct {
		Instances []comfyui.Instance       `json:"instances"`
		Settings  comfyui.InstanceSettings `json:"settings"`
	}
	data, err := s.io.ReadFile(s.instancesPath())
	if os.IsNotExist(err) {
		d := comfyui.DefaultInstance()
		return []comfyui.Instance{d}, comfyui.DefaultInstanceSettings(), nil
	}
	if err != nil {
		return nil, comfyui.InstanceSettings{}, err
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, comfyui.InstanceSettings{}, err
	}
	if len(doc.Instances) == 0 {
		doc.Instances = []comfyui.Instance{comfyui.DefaultInstance()}
	}
	for n := range doc.Instances {
		doc.Instances[n] = comfyui.NormalizeInstance(doc.Instances[n])
	}
	return doc.Instances, comfyui.NormalizeInstanceSettings(doc.Settings), nil
}
func (s *ComfyUIStore) SaveInstances(instances []comfyui.Instance, settings comfyui.InstanceSettings) error {
	for n := range instances {
		instances[n] = comfyui.NormalizeInstance(instances[n])
		if err := comfyui.ValidateInstance(instances[n]); err != nil {
			return err
		}
	}
	doc := struct {
		Instances []comfyui.Instance       `json:"instances"`
		Settings  comfyui.InstanceSettings `json:"settings"`
	}{instances, comfyui.NormalizeInstanceSettings(settings)}
	return s.io.WriteJSON(s.instancesPath(), doc)
}
func (s *ComfyUIStore) workflowPath(id string) string {
	return filepath.ToSlash(filepath.Join("comfyui/workflows", id+".json"))
}
func (s *ComfyUIStore) legacyWorkflowPath(id string) string {
	return filepath.ToSlash(filepath.Join("meta/comfyui/workflows", id+".json"))
}
func (s *ComfyUIStore) SaveWorkflow(w comfyui.Workflow) error {
	if !safeComfyID(w.ID) {
		return errors.New("workflow id is required")
	}
	if err := s.projectIO.WriteJSON(s.workflowPath(w.ID), w); err != nil {
		return err
	}
	if err := s.projectIO.WriteJSON(filepath.ToSlash(filepath.Join("comfyui/workflows", w.ID+".api.json")), w.Workflow); err != nil {
		return err
	}
	if w.Config != nil {
		return s.SaveWorkflowConfig(w.ID, *w.Config)
	}
	return nil
}
func (s *ComfyUIStore) LoadWorkflow(id string) (comfyui.Workflow, error) {
	if !safeComfyID(id) {
		return comfyui.Workflow{}, fmt.Errorf("invalid workflow id")
	}
	var w comfyui.Workflow
	err := s.projectIO.ReadJSON(s.workflowPath(id), &w)
	if os.IsNotExist(err) && s.projectIO != s.io {
		err = s.io.ReadJSON(s.legacyWorkflowPath(id), &w)
	}
	if os.IsNotExist(err) {
		var api map[string]any
		apiPath := filepath.ToSlash(filepath.Join("comfyui/workflows", id+".api.json"))
		if s.projectIO != s.io {
			if e := s.projectIO.ReadJSON(apiPath, &api); e != nil {
				apiPath = filepath.ToSlash(filepath.Join("meta/comfyui/workflows", id+".api.json"))
				if e = s.io.ReadJSON(apiPath, &api); e != nil {
					return w, err
				}
			}
		} else if e := s.io.ReadJSON(apiPath, &api); e != nil {
			return w, err
		}
		w.ID = id
		w.Workflow = api
		cfg, _ := s.LoadWorkflowConfig(id)
		w.Config = &cfg
		return w, nil
	}
	return w, err
}
func (s *ComfyUIStore) workflowConfigPath(id string) string {
	return filepath.ToSlash(filepath.Join("comfyui/workflows", id+".config.json"))
}
func (s *ComfyUIStore) legacyWorkflowConfigPath(id string) string {
	return filepath.ToSlash(filepath.Join("meta/comfyui/workflows", id+".config.json"))
}

func (s *ComfyUIStore) workflowCanvasPath(id string) string {
	return filepath.ToSlash(filepath.Join("comfyui/workflows", id+".canvas.json"))
}
func (s *ComfyUIStore) legacyWorkflowCanvasPath(id string) string {
	return filepath.ToSlash(filepath.Join("meta/comfyui/workflows", id+".canvas.json"))
}

// SaveWorkflowCanvas persists only the canvas projection. API workflow and
// legacy config files remain untouched so older clients can continue to load.
func (s *ComfyUIStore) SaveWorkflowCanvas(id string, canvas comfyui.CanvasDocument) error {
	if !safeComfyID(id) {
		return fmt.Errorf("invalid workflow id")
	}
	canvas = canvas.Normalize()
	canvas.WorkflowID = id
	canvas.ID = id
	canvas.UpdatedAt = time.Now().UTC()
	return s.projectIO.WriteJSON(s.workflowCanvasPath(id), canvas)
}

func (s *ComfyUIStore) LoadWorkflowCanvas(id string) (comfyui.CanvasDocument, error) {
	if !safeComfyID(id) {
		return comfyui.CanvasDocument{}, fmt.Errorf("invalid workflow id")
	}
	var canvas comfyui.CanvasDocument
	err := s.projectIO.ReadJSON(s.workflowCanvasPath(id), &canvas)
	if os.IsNotExist(err) && s.projectIO != s.io {
		err = s.io.ReadJSON(s.legacyWorkflowCanvasPath(id), &canvas)
	}
	if err != nil {
		return canvas, err
	}
	return canvas.Normalize(), nil
}

// LoadOrCreateWorkflowCanvas returns a deterministic default for workflows
// imported before canvas support was added. The default is not written until
// the user saves it explicitly.
func (s *ComfyUIStore) LoadOrCreateWorkflowCanvas(id string) (comfyui.CanvasDocument, error) {
	canvas, err := s.LoadWorkflowCanvas(id)
	if err == nil {
		return canvas, nil
	}
	if !os.IsNotExist(err) {
		return canvas, err
	}
	wf, e := s.LoadWorkflow(id)
	if e != nil {
		return canvas, e
	}
	return comfyui.DefaultCanvas(wf), nil
}
func (s *ComfyUIStore) SaveWorkflowConfig(id string, c comfyui.WorkflowConfig) error {
	if !safeComfyID(id) {
		return fmt.Errorf("invalid workflow id")
	}
	return s.projectIO.WriteJSON(s.workflowConfigPath(id), c)
}
func (s *ComfyUIStore) LoadWorkflowConfig(id string) (comfyui.WorkflowConfig, error) {
	if !safeComfyID(id) {
		return comfyui.WorkflowConfig{}, fmt.Errorf("invalid workflow id")
	}
	var c comfyui.WorkflowConfig
	err := s.projectIO.ReadJSON(s.workflowConfigPath(id), &c)
	if os.IsNotExist(err) && s.projectIO != s.io {
		err = s.io.ReadJSON(s.legacyWorkflowConfigPath(id), &c)
	}
	if os.IsNotExist(err) {
		var api map[string]any
		apiPath := filepath.ToSlash(filepath.Join("comfyui/workflows", id+".api.json"))
		reader := s.projectIO
		if s.projectIO != s.io {
			if e := reader.ReadJSON(apiPath, &api); e != nil {
				reader = s.io
				apiPath = filepath.ToSlash(filepath.Join("meta/comfyui/workflows", id+".api.json"))
			}
		}
		if e := reader.ReadJSON(apiPath, &api); e != nil {
			return c, err
		}
		c, _ = comfyui.InferBindings(api)
		return c, nil
	}
	return c, err
}
func (s *ComfyUIStore) DeleteWorkflow(id string) error {
	if !safeComfyID(id) {
		return fmt.Errorf("invalid workflow id")
	}
	for _, path := range []string{s.workflowPath(id), filepath.ToSlash(filepath.Join("comfyui/workflows", id+".api.json")), s.workflowConfigPath(id), s.workflowCanvasPath(id)} {
		if err := s.projectIO.RemoveFile(path); err != nil {
			return err
		}
	}
	return nil
}
func (s *ComfyUIStore) ListWorkflows() ([]comfyui.Workflow, error) {
	type workflowSource struct{ root, rel string }
	sources := []workflowSource{{s.projectIO.dir, "comfyui/workflows"}}
	if s.projectIO != s.io {
		sources = append(sources, workflowSource{s.io.dir, "meta/comfyui/workflows"})
	}
	paths := map[string]workflowSource{}
	for _, source := range sources {
		entries, err := os.ReadDir(filepath.Join(source.root, source.rel))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() || filepath.Ext(e.Name()) != ".json" || strings.HasSuffix(e.Name(), ".api.json") || strings.HasSuffix(e.Name(), ".config.json") || strings.HasSuffix(e.Name(), ".canvas.json") {
				continue
			}
			if _, exists := paths[e.Name()]; !exists {
				paths[e.Name()] = source
			}
		}
	}
	var out []comfyui.Workflow
	for name, source := range paths {
		var w comfyui.Workflow
		path := filepath.ToSlash(filepath.Join(source.rel, name))
		data, err := os.ReadFile(filepath.Join(source.root, path))
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}
func (s *ComfyUIStore) jobPath(id string) string {
	return filepath.ToSlash(filepath.Join("meta/images/jobs", id+".json"))
}
func (s *ComfyUIStore) SaveJob(j ImageJob) error {
	if !safeComfyID(j.JobID) {
		return errors.New("job_id is required")
	}
	return s.io.WriteJSON(s.jobPath(j.JobID), j)
}

func (s *ComfyUIStore) SaveJobWorkflow(jobID string, workflow map[string]any) (string, error) {
	if !safeComfyID(jobID) {
		return "", fmt.Errorf("invalid job id")
	}
	key := filepath.ToSlash(filepath.Join("meta/images/jobs", jobID+".workflow.json"))
	if err := s.io.WriteJSON(key, workflow); err != nil {
		return "", err
	}
	return key, nil
}

func (s *ComfyUIStore) LoadJobWorkflow(jobID string) (map[string]any, error) {
	if !safeComfyID(jobID) {
		return nil, fmt.Errorf("invalid job id")
	}
	var workflow map[string]any
	err := s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/images/jobs", jobID+".workflow.json")), &workflow)
	return workflow, err
}

func (s *ComfyUIStore) FindJobByIdempotencyKey(key string) (ImageJob, bool, error) {
	if strings.TrimSpace(key) == "" {
		return ImageJob{}, false, nil
	}
	jobs, err := s.ListJobs()
	if err != nil {
		return ImageJob{}, false, err
	}
	for i := len(jobs) - 1; i >= 0; i-- {
		if jobs[i].IdempotencyKey == key {
			return jobs[i], true, nil
		}
	}
	return ImageJob{}, false, nil
}
func (s *ComfyUIStore) LoadJob(id string) (ImageJob, error) {
	if !safeComfyID(id) {
		return ImageJob{}, fmt.Errorf("invalid job id")
	}
	var j ImageJob
	err := s.io.ReadJSON(s.jobPath(id), &j)
	return j, err
}
func (s *ComfyUIStore) ListJobs() ([]ImageJob, error) {
	entries, err := os.ReadDir(filepath.Join(s.io.dir, "meta/images/jobs"))
	if os.IsNotExist(err) {
		return []ImageJob{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []ImageJob
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" || strings.HasSuffix(e.Name(), ".workflow.json") {
			continue
		}
		var j ImageJob
		if err := s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/images/jobs", e.Name())), &j); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}
func NewImageJobID() string { return fmt.Sprintf("img_%d", time.Now().UnixNano()) }

func (s *ComfyUIStore) SaveMedia(data []byte, name, mime, instanceID string) (MediaRef, error) {
	sum := sha256.Sum256(data)
	hash := fmt.Sprintf("%x", sum[:])
	ext := strings.ToLower(filepath.Ext(name))
	if len(ext) > 10 {
		ext = ""
	}
	key := filepath.ToSlash(filepath.Join("assets/input", hash+ext))
	if err := s.io.WriteFileUnlocked(key, data); err != nil {
		return MediaRef{}, err
	}
	ref := MediaRef{ID: hash, SHA256: hash, Source: "local", StorageKey: key, UploadName: filepath.Base(name), MIME: mime, Size: int64(len(data)), InstanceID: instanceID}
	if err := s.io.WriteJSON(filepath.ToSlash(filepath.Join("meta/comfyui/media", hash+".json")), ref); err != nil {
		return MediaRef{}, err
	}
	return ref, nil
}
func (s *ComfyUIStore) LoadMedia(id string) (MediaRef, string, error) {
	if !safeComfyID(id) {
		return MediaRef{}, "", fmt.Errorf("invalid media id")
	}
	var ref MediaRef
	if err := s.io.ReadJSON(filepath.ToSlash(filepath.Join("meta/comfyui/media", id+".json")), &ref); err != nil {
		return ref, "", err
	}
	clean := filepath.ToSlash(filepath.Clean(ref.StorageKey))
	if !strings.HasPrefix(clean, "assets/input/") || strings.Contains(clean, "..") {
		return MediaRef{}, "", fmt.Errorf("invalid media storage key")
	}
	ref.StorageKey = clean
	return ref, filepath.Join(s.io.dir, filepath.FromSlash(ref.StorageKey)), nil
}
