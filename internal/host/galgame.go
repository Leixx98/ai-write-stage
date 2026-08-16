package host

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

// GenerateGalgameReply is the deliberately small model boundary for the
// interactive mode. Prompt assembly remains in the galgame web/domain layer.
func (h *Host) GenerateGalgameReply(ctx context.Context, character store.GalgameCharacter, session store.GalgameSession, userInput string) (string, error) {
	if h == nil || h.models == nil {
		return "", fmt.Errorf("galgame model is unavailable")
	}
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return "", fmt.Errorf("user input is empty")
	}
	system := strings.TrimSpace(character.SystemPrompt)
	if system == "" {
		system = "You are roleplaying the character below. Stay in character and reply naturally.\n\nCharacter prompt:\n" + character.Prompt
	}
	if character.Scenario != "" {
		system += "\n\nScenario:\n" + character.Scenario
	}
	if session.StoryPreset != "" {
		system += "\n\nStory preset:\n" + session.StoryPreset
	}
	if session.UserPersona != "" {
		system += "\n\nUser persona:\n" + session.UserPersona
	}
	if character.PostHistoryInstructions != "" {
		system += "\n\nPost-history instructions:\n" + character.PostHistoryInstructions
	}
	msgs := []agentcore.Message{agentcore.SystemMsg(system)}
	start := 0
	if len(session.Messages) > 24 {
		start = len(session.Messages) - 24
	}
	for _, m := range session.Messages[start:] {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		role := agentcore.RoleUser
		if strings.EqualFold(m.Role, "assistant") {
			role = agentcore.RoleAssistant
		}
		msgs = append(msgs, agentcore.Message{Role: role, Content: []agentcore.ContentBlock{agentcore.TextBlock(m.Content)}})
	}
	msgs = append(msgs, agentcore.UserMsg(userInput))
	h.mu.Lock()
	var record func(string, string, agentcore.AgentMessage)
	if h.usage != nil {
		record = h.usage.Record
	}
	model := newUsageTrackedModel(h.models.ForRole("galgame"), "galgame", record)
	thinking := h.resolveThinkingForRoleLocked("galgame")
	h.mu.Unlock()
	response, err := model.Generate(ctx, msgs, nil, agentcore.WithThinking(thinking), agentcore.WithMaxTokens(4096))
	if err != nil {
		return "", fmt.Errorf("galgame generate: %w", err)
	}
	text := strings.TrimSpace(response.Message.TextContent())
	if text == "" {
		return "", fmt.Errorf("galgame returned an empty response")
	}
	return text, nil
}
