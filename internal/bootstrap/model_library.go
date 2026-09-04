package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const modelLibraryVersion = 1

// ModelLibrary is the shared provider and model catalog. Workspace choices
// live in workspaces/<name>/.ainovel/config.json and are deliberately not stored here.
type ModelLibrary struct {
	Version       int                       `json:"version"`
	ProviderOrder []string                  `json:"provider_order"`
	Providers     map[string]ProviderConfig `json:"providers"`
}

// DefaultModelLibraryPath returns the shared model catalog path.
func DefaultModelLibraryPath() string {
	dir := DefaultConfigDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "models.json")
}

func LoadModelLibrary() (ModelLibrary, error) {
	path := DefaultModelLibraryPath()
	if path == "" {
		return ModelLibrary{}, fmt.Errorf("cannot locate shared model library")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ModelLibrary{}, err
	}
	var library ModelLibrary
	if err := json.Unmarshal(data, &library); err != nil {
		return ModelLibrary{}, fmt.Errorf("parse %s: %w", path, err)
	}
	if err := library.Validate(); err != nil {
		return ModelLibrary{}, fmt.Errorf("invalid model library %s: %w", path, err)
	}
	return library, nil
}

func SaveModelLibrary(library ModelLibrary) error {
	if library.Version == 0 {
		library.Version = modelLibraryVersion
	}
	if err := library.Validate(); err != nil {
		return err
	}
	path := DefaultModelLibraryPath()
	if path == "" {
		return fmt.Errorf("cannot locate shared model library")
	}
	return saveJSON(path, library)
}

func (library ModelLibrary) Validate() error {
	if library.Version != modelLibraryVersion {
		return fmt.Errorf("unsupported version %d", library.Version)
	}
	if len(library.ProviderOrder) == 0 || len(library.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	seen := make(map[string]bool, len(library.ProviderOrder))
	for _, rawName := range library.ProviderOrder {
		name := strings.TrimSpace(rawName)
		if name == "" || seen[name] {
			return fmt.Errorf("provider_order contains an empty or duplicate provider")
		}
		provider, ok := library.Providers[name]
		if !ok {
			return fmt.Errorf("provider_order references missing provider %q", name)
		}
		if len(provider.Models) == 0 {
			return fmt.Errorf("provider %q has no models", name)
		}
		modelNames := make(map[string]bool, len(provider.Models))
		for _, model := range provider.Models {
			modelName := strings.TrimSpace(model.Name)
			if modelName == "" || modelNames[modelName] {
				return fmt.Errorf("provider %q contains an empty or duplicate model", name)
			}
			if model.ContextWindow < 0 {
				return fmt.Errorf("model %q context_window cannot be negative", modelName)
			}
			if model.Temperature != nil && (*model.Temperature < 0 || *model.Temperature > 2) {
				return fmt.Errorf("model %q temperature must be between 0 and 2", modelName)
			}
			if !validReasoningEffort(model.ReasoningEffort) {
				return fmt.Errorf("model %q has invalid reasoning_effort %q", modelName, model.ReasoningEffort)
			}
			modelNames[modelName] = true
		}
		seen[name] = true
	}
	if len(seen) != len(library.Providers) {
		return fmt.Errorf("every provider must appear in provider_order")
	}
	return nil
}

func validReasoningEffort(value string) bool {
	switch strings.TrimSpace(value) {
	case "", "off", "low", "medium", "high", "xhigh", "max":
		return true
	default:
		return false
	}
}

// FirstModel returns the first provider in ProviderOrder and its first model.
func (library ModelLibrary) FirstModel() (provider string, model ModelConfig, err error) {
	if err := library.Validate(); err != nil {
		return "", ModelConfig{}, err
	}
	provider = library.ProviderOrder[0]
	return provider, library.Providers[provider].Models[0], nil
}

func ModelLibraryFromConfig(cfg Config, providerOrder []string) ModelLibrary {
	providers := make(map[string]ProviderConfig, len(cfg.Providers))
	for name, provider := range cfg.Providers {
		provider.Models = append([]ModelConfig(nil), provider.Models...)
		provider.Extra = cloneMap(provider.Extra)
		provider.ExtraBody = cloneMap(provider.ExtraBody)
		providers[name] = provider
	}
	return ModelLibrary{Version: modelLibraryVersion, ProviderOrder: append([]string(nil), providerOrder...), Providers: providers}
}

func saveJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	committed = true
	return nil
}
