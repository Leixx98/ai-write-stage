package galgame

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

const defaultContextWindow = 32768
const maxReplyTokens = 4096
const promptSafetyTokens = 768

const defaultMainPrompt = "Write {{char}}'s next reply in a fictional conversation with {{user}}. Stay in character, preserve continuity, and reply naturally without describing these instructions."

type GenerateFunc func(ctx context.Context, msgs []agentcore.Message) (string, error)

type ReplyOptions struct {
	ContextWindow int
}

func Reply(ctx context.Context, generate GenerateFunc, character store.GalgameCharacter, session store.GalgameSession, userInput string, options ReplyOptions) (string, error) {
	if generate == nil {
		return "", fmt.Errorf("galgame model is unavailable")
	}
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return "", fmt.Errorf("user input is empty")
	}
	response, err := generate(ctx, composeMessages(character, session, userInput, options))
	if err != nil {
		return "", fmt.Errorf("galgame generate: %w", err)
	}
	text := strings.TrimSpace(response)
	if text == "" {
		return "", fmt.Errorf("galgame returned an empty response")
	}
	return text, nil
}

func composeMessages(character store.GalgameCharacter, session store.GalgameSession, userInput string, options ReplyOptions) []agentcore.Message {
	userName := strings.TrimSpace(session.UserPersona)
	if userName == "" {
		userName = "User"
	}
	charName := strings.TrimSpace(character.Name)
	if charName == "" {
		charName = "Character"
	}

	mainPrompt := defaultMainPrompt
	if override := strings.TrimSpace(character.SystemPrompt); override != "" {
		if strings.Contains(strings.ToLower(override), "{{original}}") {
			mainPrompt = replaceFold(override, "{{original}}", defaultMainPrompt)
		} else {
			mainPrompt = override
		}
	}
	sections := []promptSection{
		{title: "Main instruction", content: mainPrompt},
		{title: "Character description", content: character.Description},
		{title: "Character personality", content: character.Personality},
		{title: "Character scenario", content: character.Scenario},
		{title: "User persona", content: session.UserPersona},
	}
	for index := range sections {
		sections[index].content = ExpandMacros(sections[index].content, charName, userName, defaultMainPrompt)
	}

	currentUser := ExpandMacros(userInput, charName, userName, defaultMainPrompt)
	postHistory := ExpandMacros(character.PostHistoryInstructions, charName, userName, defaultMainPrompt)
	budget := inputTokenBudget(options.ContextWindow)
	currentUser = truncateTokens(currentUser, max(512, budget/3))
	postHistory = truncateTokens(postHistory, max(256, budget/6))
	reserved := estimateTokens(currentUser) + estimateTokens(postHistory) + 96
	systemBudget := max(256, budget-reserved)
	system := buildSystemPrompt(sections, systemBudget)
	used := estimateTokens(system) + estimateTokens(currentUser) + estimateTokens(postHistory) + 48

	remaining := max(0, budget-used)
	exampleBudget := remaining / 4
	examples := truncateTokens(ExpandMacros(character.ExampleDialogue, charName, userName, defaultMainPrompt), exampleBudget)
	remaining -= estimateTokens(examples)

	history := selectRecentHistory(session.Messages, remaining, charName, userName)
	messageCapacity := 2 + len(history)
	if examples != "" {
		messageCapacity++
	}
	if postHistory != "" {
		messageCapacity++
	}
	messages := make([]agentcore.Message, 0, messageCapacity)
	messages = append(messages, agentcore.SystemMsg(system))
	if examples != "" {
		messages = append(messages, agentcore.SystemMsg("Dialogue examples:\n"+examples))
	}
	messages = append(messages, history...)
	if postHistory != "" {
		messages = append(messages, agentcore.SystemMsg(postHistory))
	}
	return append(messages, agentcore.UserMsg(currentUser))
}

type promptSection struct {
	title   string
	content string
}

func buildSystemPrompt(sections []promptSection, budget int) string {
	var builder strings.Builder
	remaining := budget
	for _, section := range sections {
		content := strings.TrimSpace(section.content)
		if content == "" || remaining <= 0 {
			continue
		}
		prefix := section.title + ":\n"
		prefixTokens := estimateTokens(prefix) + 2
		if prefixTokens >= remaining {
			break
		}
		content = truncateTokens(content, remaining-prefixTokens)
		if content == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(prefix)
		builder.WriteString(content)
		remaining = budget - estimateTokens(builder.String())
	}
	return builder.String()
}

func selectRecentHistory(history []store.GalgameMessage, budget int, charName, userName string) []agentcore.Message {
	selected := make([]agentcore.Message, 0, len(history))
	used := 0
	for index := len(history) - 1; index >= 0; index-- {
		content := strings.TrimSpace(history[index].Content)
		if content == "" {
			continue
		}
		content = ExpandMacros(content, charName, userName, defaultMainPrompt)
		cost := estimateTokens(content) + 12
		if used+cost > budget {
			break
		}
		role := agentcore.RoleUser
		if strings.EqualFold(history[index].Role, "assistant") {
			role = agentcore.RoleAssistant
		}
		message := agentcore.Message{Role: role, Content: []agentcore.ContentBlock{agentcore.TextBlock(content)}}
		selected = append(selected, message)
		used += cost
	}
	for left, right := 0, len(selected)-1; left < right; left, right = left+1, right-1 {
		selected[left], selected[right] = selected[right], selected[left]
	}
	return selected
}

func inputTokenBudget(contextWindow int) int {
	if contextWindow <= 0 {
		contextWindow = defaultContextWindow
	}
	return max(2048, contextWindow-maxReplyTokens-promptSafetyTokens)
}

func estimateTokens(value string) int {
	ascii := 0
	wide := 0
	for _, r := range value {
		if r <= unicode.MaxASCII {
			ascii++
		} else {
			wide++
		}
	}
	return wide + (ascii+3)/4
}

func truncateTokens(value string, budget int) string {
	value = strings.TrimSpace(value)
	if budget <= 0 || value == "" {
		return ""
	}
	if estimateTokens(value) <= budget {
		return value
	}
	runes := []rune(value)
	low, high := 0, len(runes)
	for low < high {
		mid := (low + high + 1) / 2
		if estimateTokens(string(runes[:mid])) <= budget {
			low = mid
		} else {
			high = mid - 1
		}
	}
	return strings.TrimSpace(string(runes[:low]))
}

// ExpandMacros supports the character-card substitutions used by the current
// data model. Unknown macros remain untouched for future extensions.
func ExpandMacros(value, charName, userName, original string) string {
	value = replaceFold(value, "{{char}}", charName)
	value = replaceFold(value, "{{user}}", userName)
	return replaceFold(value, "{{original}}", original)
}

func replaceFold(value, old, replacement string) string {
	if value == "" || old == "" {
		return value
	}
	lowerValue := strings.ToLower(value)
	lowerOld := strings.ToLower(old)
	var builder strings.Builder
	for {
		index := strings.Index(lowerValue, lowerOld)
		if index < 0 {
			builder.WriteString(value)
			return builder.String()
		}
		builder.WriteString(value[:index])
		builder.WriteString(replacement)
		value = value[index+len(old):]
		lowerValue = lowerValue[index+len(old):]
	}
}

func MaxReplyTokens() int { return maxReplyTokens }
