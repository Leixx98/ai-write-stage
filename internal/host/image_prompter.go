package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/agents"
	"github.com/Leixx98/ai-write-stage/internal/imagejob"
	"github.com/voocel/agentcore"
)

// GenerateImagePrompt calls the Prompter role. The system prompt comes from
// the selected workflow template, field JSON, and user-authored field notes.
func (h *Host) GenerateImagePrompt(ctx context.Context, request imagejob.PromptRequest) (string, error) {
	if h == nil || h.models == nil {
		return "", fmt.Errorf("Prompter model is unavailable")
	}
	userPrompt, err := imagejob.UserPrompt(request)
	if err != nil {
		return "", err
	}
	systemPrompt := strings.TrimSpace(request.SystemPrompt)
	if systemPrompt == "" {
		systemPrompt = imagejob.DefaultPrompterTemplate
	}
	h.mu.Lock()
	model := h.models.ForRole("prompter")
	thinking := h.resolveThinkingForRoleLocked("prompter")
	h.mu.Unlock()
	opts := append([]agentcore.CallOption{agentcore.WithMaxTokens(16384), agentcore.WithJSONMode()}, agents.ThinkingCallOptions(thinking)...)
	response, err := model.Generate(ctx, []agentcore.Message{
		agentcore.SystemMsg(systemPrompt),
		agentcore.UserMsg(userPrompt),
	}, nil, opts...)
	if err != nil {
		return "", fmt.Errorf("Prompter generate: %w", err)
	}
	raw := strings.TrimSpace(response.Message.TextContent())
	if raw == "" {
		return "", fmt.Errorf("Prompter returned an empty response")
	}
	return raw, nil
}

// ImagePrompterFingerprint participates in unit-image idempotency without
// exposing the configured system prompt through HTTP.
func (h *Host) ImagePrompterFingerprint() string {
	if h == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(imagejob.DefaultPrompterTemplate))
	return hex.EncodeToString(sum[:])
}
