package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/voocel/ainovel-cli/internal/comfyui"
)

type ComfyUIStore struct {
	io *IO
}

func NewComfyUIStore(io *IO) *ComfyUIStore { return &ComfyUIStore{io: io} }

func safeComfyID(id string) bool {
	return id != "" && filepath.Base(id) == id && !strings.ContainsAny(id, `/\\`) && id != "." && id != ".."
}
func (s *ComfyUIStore) LoadConfig() (comfyui.Config, error) {
	c := comfyui.DefaultConfig()
	data, err := s.io.ReadFile("providers/comfyui/config.json")
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
	return s.io.WriteJSON("providers/comfyui/config.json", comfyui.NormalizeConfig(c))
}
func (s *ComfyUIStore) instancesPath() string { return "providers/comfyui/instances.json" }
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
	return filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".json"))
}
func (s *ComfyUIStore) SaveWorkflow(w comfyui.Workflow) error {
	if !safeComfyID(w.ID) {
		return errors.New("workflow id is required")
	}
	if err := s.io.WriteJSON(s.workflowPath(w.ID), w); err != nil {
		return err
	}
	if err := s.io.WriteJSON(filepath.ToSlash(filepath.Join("providers/comfyui/workflows", w.ID+".api.json")), w.Workflow); err != nil {
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
	err := s.io.ReadJSON(s.workflowPath(id), &w)
	if os.IsNotExist(err) {
		var api map[string]any
		apiPath := filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".api.json"))
		if e := s.io.ReadJSON(apiPath, &api); e != nil {
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
	return filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".config.json"))
}

func (s *ComfyUIStore) workflowCanvasPath(id string) string {
	return filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".canvas.json"))
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
	return s.io.WriteJSON(s.workflowCanvasPath(id), canvas)
}

func (s *ComfyUIStore) LoadWorkflowCanvas(id string) (comfyui.CanvasDocument, error) {
	if !safeComfyID(id) {
		return comfyui.CanvasDocument{}, fmt.Errorf("invalid workflow id")
	}
	var canvas comfyui.CanvasDocument
	err := s.io.ReadJSON(s.workflowCanvasPath(id), &canvas)
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
	return s.io.WriteJSON(s.workflowConfigPath(id), c)
}
func (s *ComfyUIStore) LoadWorkflowConfig(id string) (comfyui.WorkflowConfig, error) {
	if !safeComfyID(id) {
		return comfyui.WorkflowConfig{}, fmt.Errorf("invalid workflow id")
	}
	var c comfyui.WorkflowConfig
	err := s.io.ReadJSON(s.workflowConfigPath(id), &c)
	if os.IsNotExist(err) {
		var api map[string]any
		apiPath := filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".api.json"))
		if e := s.io.ReadJSON(apiPath, &api); e != nil {
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
	for _, path := range []string{s.workflowPath(id), filepath.ToSlash(filepath.Join("providers/comfyui/workflows", id+".api.json")), s.workflowConfigPath(id), s.workflowCanvasPath(id)} {
		if err := s.io.RemoveFile(path); err != nil {
			return err
		}
	}
	return nil
}
func (s *ComfyUIStore) ListWorkflows() ([]comfyui.Workflow, error) {
	rel := "providers/comfyui/workflows"
	entries, err := os.ReadDir(filepath.Join(s.io.dir, filepath.FromSlash(rel)))
	if os.IsNotExist(err) {
		return []comfyui.Workflow{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []comfyui.Workflow
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || filepath.Ext(name) != ".json" || strings.HasSuffix(name, ".api.json") || strings.HasSuffix(name, ".config.json") || strings.HasSuffix(name, ".canvas.json") {
			continue
		}
		var w comfyui.Workflow
		data, err := os.ReadFile(filepath.Join(s.io.dir, filepath.FromSlash(rel), name))
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
