package play

import (
	"encoding/json"
	"strings"

	"github.com/voocel/ainovel-cli/internal/store"
)

const staticContextHeading = "## 固定上下文"

type PromptCharacter struct {
	Name         string `json:"name,omitempty"`
	Description  string `json:"description,omitempty"`
	Personality  string `json:"personality,omitempty"`
	Scenario     string `json:"scenario,omitempty"`
	SystemPrompt string `json:"system_prompt,omitempty"`
}

type PromptBeat struct {
	Ordinal   int                `json:"ordinal"`
	Kind      store.PlayBeatKind `json:"kind"`
	Speaker   string             `json:"speaker,omitempty"`
	Text      string             `json:"text,omitempty"`
	Location  string             `json:"location,omitempty"`
	TimeOfDay string             `json:"time_of_day,omitempty"`
}

type sharedStatic struct {
	Character   PromptCharacter `json:"character"`
	Premise     string          `json:"premise,omitempty"`
	UserPersona string          `json:"user_persona,omitempty"`
}

type writerTurn struct {
	Card store.PlayBeatCard `json:"card"`
}

type architectTurn struct {
	ChoiceHistory []store.PlayChoiceRecord `json:"choice_history"`
	RecentBeats   []PromptBeat             `json:"recent_beats"`
	LastGoal      string                   `json:"last_goal,omitempty"`
}

type plannerTurn struct {
	Location  string          `json:"location,omitempty"`
	Architect ArchitectOutput `json:"architect"`
}

func slimCharacter(character store.GalgameCharacter) PromptCharacter {
	return PromptCharacter{
		Name:         strings.TrimSpace(character.Name),
		Description:  strings.TrimSpace(character.Description),
		Personality:  strings.TrimSpace(character.Personality),
		Scenario:     strings.TrimSpace(character.Scenario),
		SystemPrompt: strings.TrimSpace(character.SystemPrompt),
	}
}

func slimBeats(beats []store.PlayBeat) []PromptBeat {
	out := make([]PromptBeat, 0, len(beats))
	for _, beat := range beats {
		out = append(out, PromptBeat{
			Ordinal:   beat.Ordinal,
			Kind:      beat.Kind,
			Speaker:   strings.TrimSpace(beat.Speaker),
			Text:      strings.TrimSpace(beat.Text),
			Location:  strings.TrimSpace(beat.Location),
			TimeOfDay: strings.TrimSpace(beat.TimeOfDay),
		})
	}
	return out
}

func attachStaticContext(prompt string, static any) (string, error) {
	raw, err := json.Marshal(static)
	if err != nil {
		return "", err
	}
	system := staticContextHeading + "\n" + string(raw)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return system, nil
	}
	return system + "\n\n" + prompt, nil
}

func sharedCard(character store.GalgameCharacter, premise, userPersona string) sharedStatic {
	return sharedStatic{
		Character:   slimCharacter(character),
		Premise:     strings.TrimSpace(premise),
		UserPersona: strings.TrimSpace(userPersona),
	}
}

func marshalTurn(turn any) (string, error) {
	raw, err := json.Marshal(turn)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func writerRequest(prompt string, in WriterInput) (string, string, error) {
	system, err := attachStaticContext(prompt, sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	payload, err := marshalTurn(writerTurn{Card: in.Card})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func architectRequest(prompt string, in ArchitectInput) (string, string, error) {
	system, err := attachStaticContext(prompt, sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	history := in.ChoiceHistory
	if history == nil {
		history = []store.PlayChoiceRecord{}
	}
	payload, err := marshalTurn(architectTurn{
		ChoiceHistory: history,
		RecentBeats:   slimBeats(in.RecentBeats),
		LastGoal:      strings.TrimSpace(in.LastGoal),
	})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func plannerRequest(prompt string, in PlannerInput) (string, string, error) {
	system, err := attachStaticContext(prompt, sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	payload, err := marshalTurn(plannerTurn{Location: strings.TrimSpace(in.Location), Architect: in.Architect})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func playCacheKey(playID, contractName string) string {
	playID = strings.TrimSpace(playID)
	if playID == "" {
		return ""
	}
	return "play-" + playID + "-" + strings.TrimPrefix(contractName, "play_")
}
