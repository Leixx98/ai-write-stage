package galgame

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"unicode"

	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore"
)

const defaultContextWindow = 32768
const maxReplyTokens = 4096
const promptSafetyTokens = 768

const defaultMainPrompt = "Write {{char}}'s next reply in a fictional conversation with {{user}}. Stay in character, preserve continuity, and reply naturally without describing these instructions."

type GenerateFunc func(ctx context.Context, msgs []agentcore.Message) (string, error)

type Delta struct {
	Kind string
	Text string
}

type StreamFunc func(ctx context.Context, msgs []agentcore.Message, emit func(Delta)) (string, error)

type ReplyOptions struct {
	ContextWindow int
}

func Reply(ctx context.Context, generate GenerateFunc, character store.GalgameCharacter, session *store.GalgameSession, userInput string, options ReplyOptions) (string, error) {
	if generate == nil {
		return "", fmt.Errorf("galgame model is unavailable")
	}
	return ReplyStream(ctx, func(ctx context.Context, msgs []agentcore.Message, _ func(Delta)) (string, error) {
		return generate(ctx, msgs)
	}, character, session, userInput, options, nil)
}

func ReplyStream(ctx context.Context, stream StreamFunc, character store.GalgameCharacter, session *store.GalgameSession, userInput string, options ReplyOptions, emit func(Delta)) (string, error) {
	if stream == nil {
		return "", fmt.Errorf("galgame model is unavailable")
	}
	if session == nil {
		return "", fmt.Errorf("galgame session is unavailable")
	}
	userInput = strings.TrimSpace(userInput)
	if userInput == "" {
		return "", fmt.Errorf("user input is empty")
	}
	if emit == nil {
		emit = func(Delta) {}
	}
	messages, cutoff := composeMessages(character, *session, userInput, options)
	session.HistoryCutoff = cutoff
	response, err := stream(ctx, messages, emit)
	if err != nil {
		return "", fmt.Errorf("galgame generate: %w", err)
	}
	text := strings.TrimSpace(response)
	if text == "" {
		return "", fmt.Errorf("galgame returned an empty response")
	}
	return text, nil
}

func composeMessages(character store.GalgameCharacter, session store.GalgameSession, userInput string, options ReplyOptions) ([]agentcore.Message, int) {
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
	budget := inputTokenBudget(options.ContextWindow)
	staticBudget := max(256, budget/2)
	userReserve := max(512, budget/6)
	currentUser := truncateTokens(ExpandMacros(userInput, charName, userName, defaultMainPrompt), userReserve)
	postHistory := truncateTokens(ExpandMacros(character.PostHistoryInstructions, charName, userName, defaultMainPrompt), max(256, staticBudget/6))
	examples := truncateTokens(ExpandMacros(character.ExampleDialogue, charName, userName, defaultMainPrompt), max(256, staticBudget/4))
	sectionBudget := max(256, staticBudget-estimateTokens(postHistory)-estimateTokens(examples))
	system := buildFrozenSystem(sections, examples, postHistory, sectionBudget)
	historyBudget := max(0, budget-estimateTokens(system)-userReserve-48)
	history, cutoff := selectCommittedHistory(session.Messages, session.HistoryCutoff, historyBudget, charName, userName)
	messages := make([]agentcore.Message, 0, 2+len(history))
	messages = append(messages, agentcore.SystemMsg(system))
	messages = append(messages, history...)
	return append(messages, agentcore.UserMsg(currentUser)), cutoff
}

func buildFrozenSystem(sections []promptSection, examples, postHistory string, sectionBudget int) string {
	system := buildSystemPrompt(sections, sectionBudget)
	if examples != "" {
		if system != "" {
			system += "\n\n"
		}
		system += "Dialogue examples:\n" + examples
	}
	if postHistory != "" {
		if system != "" {
			system += "\n\n"
		}
		system += "Post-history instructions:\n" + postHistory
	}
	return system
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

func selectCommittedHistory(history []store.GalgameMessage, cutoff, budget int, charName, userName string) ([]agentcore.Message, int) {
	cutoff = clampCutoff(cutoff, len(history))
	if historyFits(history, cutoff, budget, charName, userName) {
		return collectHistory(history, cutoff, budget, charName, userName), cutoff
	}
	keepBudget := budget * 3 / 4
	if keepBudget < 1 {
		keepBudget = budget
	}
	cutoff = nextCommittedCutoff(history, keepBudget, charName, userName)
	return collectHistory(history, cutoff, budget, charName, userName), cutoff
}

func clampCutoff(cutoff, length int) int {
	if cutoff < 0 {
		return 0
	}
	if cutoff > length {
		return length
	}
	return cutoff
}

func historyContent(message store.GalgameMessage, charName, userName string) (string, bool) {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		return "", false
	}
	return ExpandMacros(content, charName, userName, defaultMainPrompt), true
}

func historyCost(content string) int {
	return estimateTokens(content) + 12
}

func historyFits(history []store.GalgameMessage, cutoff, budget int, charName, userName string) bool {
	used := 0
	for index := cutoff; index < len(history); index++ {
		content, ok := historyContent(history[index], charName, userName)
		if !ok {
			continue
		}
		cost := historyCost(content)
		if used+cost > budget {
			return false
		}
		used += cost
	}
	return true
}

func nextCommittedCutoff(history []store.GalgameMessage, keepBudget int, charName, userName string) int {
	used := 0
	cutoff := len(history)
	for index := len(history) - 1; index >= 0; index-- {
		content, ok := historyContent(history[index], charName, userName)
		if !ok {
			continue
		}
		cost := historyCost(content)
		if used+cost > keepBudget {
			break
		}
		cutoff = index
		used += cost
	}
	return cutoff
}

func collectHistory(history []store.GalgameMessage, cutoff, budget int, charName, userName string) []agentcore.Message {
	selected := make([]agentcore.Message, 0, max(0, len(history)-cutoff))
	used := 0
	for index := cutoff; index < len(history); index++ {
		content, ok := historyContent(history[index], charName, userName)
		if !ok {
			continue
		}
		cost := historyCost(content)
		if used+cost > budget {
			break
		}
		role := agentcore.RoleUser
		if strings.EqualFold(history[index].Role, "assistant") {
			role = agentcore.RoleAssistant
		}
		selected = append(selected, agentcore.Message{Role: role, Content: []agentcore.ContentBlock{agentcore.TextBlock(content)}})
		used += cost
	}
	return selected
}

func ChatCacheKey(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	return "gal-" + sessionID
}

func PinPromptCache(messages []agentcore.Message) []agentcore.Message {
	if len(messages) == 0 {
		return messages
	}
	out := slices.Clone(messages)
	if out[0].Role == agentcore.RoleSystem {
		md := maps.Clone(out[0].Metadata)
		if md == nil {
			md = map[string]any{}
		}
		md["cache_control"] = "ephemeral"
		out[0].Metadata = md
	}
	return agentcore.MarkLastMessageForCache(out, "ephemeral")
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
