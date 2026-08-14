package imagejob

import (
	"fmt"
	"strings"
)

// BridgeConfig controls the novel-unit to ComfyUI bridge independently from
// the lower-level ComfyUI connection settings.
type BridgeConfig struct {
	Enabled           bool   `json:"enabled"`
	AutoGenerate      bool   `json:"auto_generate"`
	WorkflowID        string `json:"workflow_id"`
	Strict            bool   `json:"strict"`
	PrompterTimeoutMS int    `json:"prompter_timeout_ms"`
	PreviousTailChars int    `json:"previous_tail_chars"`
}

func DefaultBridgeConfig() BridgeConfig {
	return BridgeConfig{Strict: true, PrompterTimeoutMS: 120000, PreviousTailChars: 1200}
}

func NormalizeBridgeConfig(config BridgeConfig) BridgeConfig {
	defaults := DefaultBridgeConfig()
	config.WorkflowID = strings.TrimSpace(config.WorkflowID)
	if config.PrompterTimeoutMS == 0 {
		config.PrompterTimeoutMS = defaults.PrompterTimeoutMS
	}
	return config
}

func ValidateBridgeConfig(config BridgeConfig) error {
	if config.Enabled && strings.TrimSpace(config.WorkflowID) == "" {
		return fmt.Errorf("workflow_id is required")
	}
	if config.PrompterTimeoutMS < 1000 || config.PrompterTimeoutMS > 600000 {
		return fmt.Errorf("prompter_timeout_ms must be between 1000 and 600000")
	}
	if config.PreviousTailChars < 0 || config.PreviousTailChars > 8000 {
		return fmt.Errorf("previous_tail_chars must be between 0 and 8000")
	}
	return nil
}
