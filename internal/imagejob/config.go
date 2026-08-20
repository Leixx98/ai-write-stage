package imagejob

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Scene string

const (
	SceneNovel Scene = "novel"
	SceneChat  Scene = "chat"
	ScenePlay  Scene = "play"
)

const (
	ChatEveryReply = "every_reply"
	ChatEveryN     = "every_n"
	ChatManual     = "manual"
)

type SceneConfig struct {
	Enabled          bool   `json:"enabled"`
	AutoGenerate     bool   `json:"auto_generate,omitempty"`
	DefaultProfileID string `json:"default_profile_id,omitempty"`
	ChatPolicy       string `json:"chat_policy,omitempty"`
	EveryN           int    `json:"every_n,omitempty"`
}

type Settings struct {
	Version int         `json:"version"`
	Novel   SceneConfig `json:"novel"`
	Chat    SceneConfig `json:"chat"`
	Play    SceneConfig `json:"play"`
}

type Profile struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Provider         string         `json:"provider"`
	PrompterPresetID string         `json:"prompter_preset_id,omitempty"`
	AspectRatio      string         `json:"aspect_ratio,omitempty"`
	ImageCount       int            `json:"image_count,omitempty"`
	TimeoutMS        int            `json:"timeout_ms,omitempty"`
	ProviderOptions  map[string]any `json:"provider_options,omitempty"`
}

type Capabilities struct {
	AspectRatio bool `json:"aspect_ratio"`
	ImageCount  bool `json:"image_count"`
	Cancel      bool `json:"cancel"`
	Progress    bool `json:"progress"`
}

type ProviderInfo struct {
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Enabled      bool         `json:"enabled"`
	Capabilities Capabilities `json:"capabilities"`
}

type CharacterContext struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	ReferenceID string `json:"reference_id,omitempty"`
}

// SceneImageRequest is the provider-neutral contract accepted from business modules.
type SceneImageRequest struct {
	Scene          Scene              `json:"scene"`
	SceneID        string             `json:"scene_id"`
	UnitID         string             `json:"unit_id,omitempty"`
	Chapter        int                `json:"chapter,omitempty"`
	Ordinal        int                `json:"ordinal,omitempty"`
	AssistantIndex int                `json:"assistant_index,omitempty"`
	Title          string             `json:"title,omitempty"`
	Text           string             `json:"text,omitempty"`
	Dialogue       string             `json:"dialogue,omitempty"`
	PreviousText   string             `json:"previous_text,omitempty"`
	VisualIntent   string             `json:"visual_intent,omitempty"`
	Characters     []CharacterContext `json:"characters,omitempty"`
	ReferenceIDs   []string           `json:"reference_ids,omitempty"`
	ProfileID      string             `json:"profile_id,omitempty"`
	Manual         bool               `json:"manual,omitempty"`
	Force          bool               `json:"force,omitempty"`
	Metadata       map[string]any     `json:"metadata,omitempty"`
}

// ProviderRequest is an immutable, resolved request passed to an adapter.
type ProviderRequest struct {
	JobID            string            `json:"job_id"`
	SceneRequest     SceneImageRequest `json:"scene_request"`
	Profile          Profile           `json:"profile"`
	Prompt           string            `json:"prompt,omitempty"`
	NegativePrompt   string            `json:"negative_prompt,omitempty"`
	PromptValues     map[string]any    `json:"prompt_values,omitempty"`
	ProviderSnapshot json.RawMessage   `json:"provider_snapshot,omitempty"`
}

type MediaRef struct {
	ID         string `json:"id"`
	Source     string `json:"source"`
	StorageKey string `json:"storage_key"`
	UploadName string `json:"upload_name"`
	MIME       string `json:"mime"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
}

type MediaOutput struct {
	Kind        string `json:"kind"`
	NodeID      string `json:"node_id,omitempty"`
	OutputKey   string `json:"output_key,omitempty"`
	ClassType   string `json:"class_type,omitempty"`
	MIME        string `json:"mime"`
	Previewable bool   `json:"previewable"`
	URL         string `json:"url,omitempty"`
	StorageKey  string `json:"storage_key,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

func DefaultSettings() Settings {
	return Settings{
		Version: 1,
		Novel:   SceneConfig{Enabled: false, AutoGenerate: true},
		Chat:    SceneConfig{Enabled: false, AutoGenerate: true, ChatPolicy: ChatEveryReply, EveryN: 3},
		Play:    SceneConfig{Enabled: false, AutoGenerate: true},
	}
}

func NormalizeSettings(settings Settings) Settings {
	settings.Version = 1
	settings.Novel = normalizeSceneConfig(SceneNovel, settings.Novel)
	settings.Chat = normalizeSceneConfig(SceneChat, settings.Chat)
	settings.Play = normalizeSceneConfig(ScenePlay, settings.Play)
	return settings
}

func normalizeSceneConfig(scene Scene, cfg SceneConfig) SceneConfig {
	cfg.DefaultProfileID = strings.TrimSpace(cfg.DefaultProfileID)
	if scene == SceneChat {
		switch cfg.ChatPolicy {
		case ChatEveryReply, ChatEveryN, ChatManual:
		default:
			cfg.ChatPolicy = ChatEveryReply
		}
		if cfg.EveryN == 0 {
			cfg.EveryN = 3
		}
	} else {
		cfg.ChatPolicy = ""
		cfg.EveryN = 0
	}
	return cfg
}

func ValidateSettings(settings Settings) error {
	settings = NormalizeSettings(settings)
	if settings.Chat.EveryN < 2 || settings.Chat.EveryN > 100 {
		return fmt.Errorf("chat every_n must be between 2 and 100")
	}
	return nil
}

func NormalizeProfile(profile Profile) Profile {
	profile.ID = strings.TrimSpace(profile.ID)
	profile.Name = strings.TrimSpace(profile.Name)
	profile.Provider = strings.ToLower(strings.TrimSpace(profile.Provider))
	profile.PrompterPresetID = strings.TrimSpace(profile.PrompterPresetID)
	profile.AspectRatio = strings.TrimSpace(profile.AspectRatio)
	if profile.ImageCount == 0 {
		profile.ImageCount = 1
	}
	if profile.TimeoutMS == 0 {
		profile.TimeoutMS = int((10 * time.Minute) / time.Millisecond)
	}
	if profile.ProviderOptions == nil {
		profile.ProviderOptions = map[string]any{}
	}
	return profile
}

func ValidateProfile(profile Profile, capabilities Capabilities) error {
	profile = NormalizeProfile(profile)
	if profile.ID == "" || strings.ContainsAny(profile.ID, `/\\`) || profile.ID == "." || profile.ID == ".." {
		return fmt.Errorf("profile id is invalid")
	}
	if profile.Name == "" {
		return fmt.Errorf("profile name is required")
	}
	if profile.Provider == "" {
		return fmt.Errorf("profile provider is required")
	}
	if profile.TimeoutMS < 1000 {
		return fmt.Errorf("profile timeout_ms must be at least 1000")
	}
	if capabilities.ImageCount {
		if profile.ImageCount < 1 || profile.ImageCount > 16 {
			return fmt.Errorf("profile image_count must be between 1 and 16")
		}
	} else if profile.ImageCount != 1 {
		return fmt.Errorf("provider does not support image_count")
	}
	if !capabilities.AspectRatio && profile.AspectRatio != "" {
		return fmt.Errorf("provider does not support aspect_ratio")
	}
	return nil
}

func SceneSettings(settings Settings, scene Scene) (SceneConfig, bool) {
	settings = NormalizeSettings(settings)
	switch scene {
	case SceneNovel:
		return settings.Novel, true
	case SceneChat:
		return settings.Chat, true
	case ScenePlay:
		return settings.Play, true
	default:
		return SceneConfig{}, false
	}
}

func ShouldGenerateChat(cfg SceneConfig, assistantIndex int, manual bool) bool {
	if !cfg.Enabled {
		return false
	}
	if manual {
		return assistantIndex > 0
	}
	if !cfg.AutoGenerate || assistantIndex <= 0 {
		return false
	}
	cfg = normalizeSceneConfig(SceneChat, cfg)
	switch cfg.ChatPolicy {
	case ChatEveryReply:
		return true
	case ChatEveryN:
		return assistantIndex%cfg.EveryN == 0
	case ChatManual:
		return false
	default:
		return false
	}
}

func ProviderOptionString(profile Profile, key string) string {
	if profile.ProviderOptions == nil {
		return ""
	}
	value, ok := profile.ProviderOptions[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
