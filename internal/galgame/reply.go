package galgame

import (
	"context"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

const maxHistory = 24
const maxReplyTokens = 4096

type GenerateFunc func(ctx context.Context, msgs []agentcore.Message) (string, error)

func Reply(ctx context.Context, generate GenerateFunc, character store.GalgameCharacter, session store.GalgameSession, userInput string) (string, error) {
	if generate == nil {
		return "", fmt.Errorf("galgame model is unavailable")
	}
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return "", fmt.Errorf("user input is empty")
	}
	response, err := generate(ctx, composeMessages(character, session, userInput))
	if err != nil {
		return "", fmt.Errorf("galgame generate: %w", err)
	}
	text := strings.TrimSpace(response)
	if text == "" {
		return "", fmt.Errorf("galgame returned an empty response")
	}
	return text, nil
}

func composeMessages(character store.GalgameCharacter, session store.GalgameSession, userInput string) []agentcore.Message {
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
	if len(session.Messages) > maxHistory {
		start = len(session.Messages) - maxHistory
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
	return append(msgs, agentcore.UserMsg(userInput))
}

func MaxReplyTokens() int { return maxReplyTokens }
