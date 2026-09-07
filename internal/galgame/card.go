package galgame

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

const maxCharacterCardBytes = 4 << 20

type alternateGreetings []string

func (g *alternateGreetings) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		*g = nil
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err == nil {
		*g = many
		return nil
	}
	var one string
	if err := json.Unmarshal(data, &one); err != nil {
		return fmt.Errorf("alternate_greetings must be a string or string array")
	}
	if strings.TrimSpace(one) == "" {
		*g = nil
	} else {
		*g = []string{one}
	}
	return nil
}

type importedCharacter struct {
	Name                    string             `json:"name"`
	Description             string             `json:"description"`
	Personality             string             `json:"personality"`
	Scenario                string             `json:"scenario"`
	FirstMessage            string             `json:"first_mes"`
	ExampleDialogue         string             `json:"mes_example"`
	SystemPrompt            string             `json:"system_prompt"`
	PostHistoryInstructions string             `json:"post_history_instructions"`
	AlternateGreetings      alternateGreetings `json:"alternate_greetings"`
	Extensions              map[string]any     `json:"extensions"`
	Data                    *importedCharacter `json:"data"`
}

// ImportCharacterJSON normalizes the flat V1 shape, the V2 data envelope and
// this application's native character JSON into the store representation.
func ImportCharacterJSON(raw []byte) (store.GalgameCharacter, error) {
	if len(raw) == 0 {
		return store.GalgameCharacter{}, fmt.Errorf("character card is empty")
	}
	if len(raw) > maxCharacterCardBytes {
		return store.GalgameCharacter{}, fmt.Errorf("character card exceeds 4 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var card importedCharacter
	if err := decoder.Decode(&card); err != nil {
		return store.GalgameCharacter{}, fmt.Errorf("decode character card: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return store.GalgameCharacter{}, fmt.Errorf("character card must contain one JSON object")
	}
	if card.Data != nil {
		card = *card.Data
	}
	character := store.GalgameCharacter{
		Name:                    strings.TrimSpace(card.Name),
		Description:             strings.TrimSpace(card.Description),
		Personality:             strings.TrimSpace(card.Personality),
		Scenario:                strings.TrimSpace(card.Scenario),
		FirstMessage:            strings.TrimSpace(card.FirstMessage),
		ExampleDialogue:         strings.TrimSpace(card.ExampleDialogue),
		SystemPrompt:            strings.TrimSpace(card.SystemPrompt),
		PostHistoryInstructions: strings.TrimSpace(card.PostHistoryInstructions),
		AlternateGreetings:      cleanStrings(card.AlternateGreetings),
		Extensions:              card.Extensions,
	}
	if character.Name == "" {
		return store.GalgameCharacter{}, fmt.Errorf("character name is required")
	}
	if !hasCharacterDefinition(character) {
		return store.GalgameCharacter{}, fmt.Errorf("character card has no usable definition")
	}
	return character, nil
}

func hasCharacterDefinition(character store.GalgameCharacter) bool {
	return strings.TrimSpace(character.Description) != "" ||
		strings.TrimSpace(character.Personality) != "" ||
		strings.TrimSpace(character.Scenario) != "" ||
		strings.TrimSpace(character.SystemPrompt) != ""
}

func cleanStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

// InitializeSession applies the character greeting once when a new session is
// created. Existing messages are never changed.
func InitializeSession(character store.GalgameCharacter, session store.GalgameSession, now time.Time, greetingIndex ...int) store.GalgameSession {
	greeting := character.FirstMessage
	if len(greetingIndex) > 0 && greetingIndex[0] > 0 && greetingIndex[0] <= len(character.AlternateGreetings) {
		greeting = character.AlternateGreetings[greetingIndex[0]-1]
	}
	if len(session.Messages) != 0 || strings.TrimSpace(greeting) == "" {
		return session
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	session.Messages = append(session.Messages, store.GalgameMessage{
		Role:      "assistant",
		Name:      character.Name,
		Content:   ExpandMacros(greeting, character.Name, session.UserPersona, ""),
		CreatedAt: now,
	})
	return session
}
