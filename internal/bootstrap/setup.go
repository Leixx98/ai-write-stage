package bootstrap

import (
	"context"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/voocel/agentcore"
)

//go:embed config.example.jsonc
var exampleConfig string

type ProviderPreset struct {
	Name           string `json:"name"`
	Label          string `json:"label"`
	BaseURL        string `json:"base_url,omitempty"`
	NeedType       bool   `json:"need_type,omitempty"`
	APIKeyOptional bool   `json:"api_key_optional,omitempty"`
}

type SetupRequest struct {
	Preset   string `json:"preset"`
	Provider string `json:"provider"`
	Type     string `json:"type,omitempty"`
	API      string `json:"api,omitempty"`
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Model    string `json:"model"`
}

var providerPresets = []ProviderPreset{
	{Name: "openrouter", Label: "OpenRouter", BaseURL: "https://openrouter.ai/api/v1"},
	{Name: "anthropic", Label: "Anthropic"},
	{Name: "gemini", Label: "Gemini"},
	{Name: "openai", Label: "OpenAI"},
	{Name: "deepseek", Label: "DeepSeek"},
	{Name: "qwen", Label: "Qwen"},
	{Name: "glm", Label: "GLM"},
	{Name: "grok", Label: "Grok"},
	{Name: "ollama", Label: "Ollama", BaseURL: "http://localhost:11434/v1", APIKeyOptional: true},
	{Name: "bedrock", Label: "Bedrock", APIKeyOptional: true},
	{Name: "custom", Label: "Custom Proxy", NeedType: true, APIKeyOptional: true},
}

func NeedsSetup() bool {
	path := DefaultModelLibraryPath()
	if path == "" {
		return true
	}
	_, err := os.Stat(path)
	return err != nil
}

func ProviderPresets() []ProviderPreset {
	return append([]ProviderPreset(nil), providerPresets...)
}

func ValidateSetup(request SetupRequest) error {
	_, err := setupConfig(request)
	return err
}

func SaveSetup(request SetupRequest) (Config, error) {
	cfg, err := setupConfig(request)
	if err != nil {
		return Config{}, err
	}
	library := ModelLibraryFromConfig(cfg, []string{cfg.Provider})
	if err := SaveModelLibrary(library); err != nil {
		return Config{}, fmt.Errorf("save model library: %w", err)
	}
	_ = saveExampleConfig()
	return cfg, nil
}

func TestSetupConnection(ctx context.Context, request SetupRequest) error {
	cfg, err := setupConfig(request)
	if err != nil {
		return err
	}
	models, err := NewModelSet(cfg)
	if err != nil {
		return fmt.Errorf("create model client: %w", err)
	}
	if _, err := models.Default.Generate(ctx, []agentcore.Message{agentcore.UserMsg("Reply OK.")}, nil); err != nil {
		return fmt.Errorf("connection test failed for %s/%s: %w", cfg.Provider, cfg.ModelName, err)
	}
	return nil
}

func setupConfig(request SetupRequest) (Config, error) {
	request = normalizeSetupRequest(request)
	preset, ok := findProviderPreset(request.Preset)
	if !ok {
		return Config{}, fmt.Errorf("unknown provider preset %q", request.Preset)
	}
	provider := preset.Name
	if preset.NeedType {
		provider = request.Provider
		if provider == "" {
			return Config{}, fmt.Errorf("provider is required for a custom proxy")
		}
		if request.Type == "" {
			return Config{}, fmt.Errorf("type is required for a custom proxy")
		}
	} else if request.Provider != "" && request.Provider != preset.Name {
		return Config{}, fmt.Errorf("provider must match preset %q", preset.Name)
	}
	if request.Model == "" {
		return Config{}, fmt.Errorf("model is required")
	}
	if !preset.APIKeyOptional && request.APIKey == "" {
		return Config{}, fmt.Errorf("api_key is required for provider %q", provider)
	}
	if request.BaseURL != "" {
		parsed, err := url.ParseRequestURI(request.BaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return Config{}, fmt.Errorf("base_url must be an absolute HTTP URL")
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return Config{}, fmt.Errorf("base_url must use http or https")
		}
	}
	pc := ProviderConfig{Type: request.Type, API: request.API, APIKey: request.APIKey, BaseURL: request.BaseURL, Models: []ModelConfig{{Name: request.Model}}}
	cfg := Config{Provider: provider, ModelName: request.Model, Providers: map[string]ProviderConfig{provider: pc}, Roles: map[string]RoleConfig{}, Style: "default"}
	if err := cfg.ValidateBase(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func normalizeSetupRequest(request SetupRequest) SetupRequest {
	request.Preset = strings.TrimSpace(request.Preset)
	request.Provider = strings.TrimSpace(request.Provider)
	request.Type = strings.TrimSpace(request.Type)
	request.API = strings.TrimSpace(request.API)
	request.APIKey = strings.TrimSpace(request.APIKey)
	request.BaseURL = strings.TrimSpace(request.BaseURL)
	request.Model = strings.TrimSpace(request.Model)
	if preset, ok := findProviderPreset(request.Preset); ok && request.BaseURL == "" {
		request.BaseURL = preset.BaseURL
	}
	return request
}

func findProviderPreset(name string) (ProviderPreset, bool) {
	for _, preset := range providerPresets {
		if preset.Name == name {
			return preset, true
		}
	}
	return ProviderPreset{}, false
}

func saveExampleConfig() error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "config.example.jsonc"), []byte(exampleConfig), 0o644)
}
