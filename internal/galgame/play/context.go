package play

import (
	"encoding/json"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/store"
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

type spineTurn struct {
	Density store.PlayDensity `json:"density"`
	Pacing  store.PlayPacing  `json:"pacing"`
}

type architectTurn struct {
	Density           store.PlayDensity        `json:"density"`
	Pacing            store.PlayPacing         `json:"pacing"`
	CurrentStation    store.PlayStation        `json:"current_station"`
	RemainingStations []store.PlayStation      `json:"remaining_stations"`
	Threads           []store.PlayThread       `json:"threads"`
	Facts             []store.PlayFact         `json:"facts"`
	ChoiceHistory     []store.PlayChoiceRecord `json:"choice_history"`
	RecentBeats       []PromptBeat             `json:"recent_beats"`
}

type reviseTurn struct {
	Density       store.PlayDensity        `json:"density"`
	Pacing        store.PlayPacing         `json:"pacing"`
	Choice        store.PlayChoice         `json:"choice"`
	Facts         []store.PlayFact         `json:"facts"`
	ChoiceHistory []store.PlayChoiceRecord `json:"choice_history"`
	RecentBeats   []PromptBeat             `json:"recent_beats"`
	NextStation   store.PlayStation        `json:"next_station"`
	Threads       []store.PlayThread       `json:"threads"`
	Throughline   string                   `json:"throughline,omitempty"`
}

type replanTurn struct {
	Density           store.PlayDensity        `json:"density"`
	Pacing            store.PlayPacing         `json:"pacing"`
	Instruction       string                   `json:"instruction"`
	Throughline       string                   `json:"throughline,omitempty"`
	KeptStations      []store.PlayStation      `json:"kept_stations"`
	RemainingStations []store.PlayStation      `json:"remaining_stations"`
	Threads           []store.PlayThread       `json:"threads"`
	Facts             []store.PlayFact         `json:"facts"`
	ChoiceHistory     []store.PlayChoiceRecord `json:"choice_history"`
	RecentBeats       []PromptBeat             `json:"recent_beats"`
}

type plannerTurn struct {
	Density        store.PlayDensity `json:"density"`
	Pacing         store.PlayPacing  `json:"pacing"`
	Location       string            `json:"location,omitempty"`
	CurrentStation store.PlayStation `json:"current_station"`
	Facts          []store.PlayFact  `json:"facts"`
	LastStation    bool              `json:"last_station"`
	Architect      ArchitectOutput   `json:"architect"`
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

func withPlayHints(prompt string, density store.PlayDensity, pacing store.PlayPacing) string {
	hint := strings.TrimSpace(profileFor(density, pacing).PromptHint)
	prompt = strings.TrimSpace(prompt)
	if hint == "" {
		return prompt
	}
	if prompt == "" {
		return hint
	}
	return prompt + "\n\n" + hint
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

func spineRequest(prompt string, in SpineInput) (string, string, error) {
	system, err := attachStaticContext(withPlayHints(prompt, in.Density, in.Pacing), sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	payload, err := marshalTurn(spineTurn{Density: store.NormalizePlayDensity(string(in.Density)), Pacing: store.NormalizePlayPacing(string(in.Pacing))})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func architectRequest(prompt string, in ArchitectInput) (string, string, error) {
	system, err := attachStaticContext(withPlayHints(prompt, in.Density, in.Pacing), sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	history := in.ChoiceHistory
	if history == nil {
		history = []store.PlayChoiceRecord{}
	}
	facts := in.Facts
	if facts == nil {
		facts = []store.PlayFact{}
	}
	remaining := in.RemainingStations
	if remaining == nil {
		remaining = []store.PlayStation{}
	}
	threads := in.Threads
	if threads == nil {
		threads = []store.PlayThread{}
	}
	payload, err := marshalTurn(architectTurn{
		Density:           store.NormalizePlayDensity(string(in.Density)),
		Pacing:            store.NormalizePlayPacing(string(in.Pacing)),
		CurrentStation:    in.CurrentStation,
		RemainingStations: remaining,
		Threads:           threads,
		Facts:             facts,
		ChoiceHistory:     history,
		RecentBeats:       slimBeats(in.RecentBeats),
	})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func plannerRequest(prompt string, in PlannerInput) (string, string, error) {
	system, err := attachStaticContext(withPlayHints(prompt, in.Density, in.Pacing), sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	facts := in.Facts
	if facts == nil {
		facts = []store.PlayFact{}
	}
	payload, err := marshalTurn(plannerTurn{
		Density:        store.NormalizePlayDensity(string(in.Density)),
		Pacing:         store.NormalizePlayPacing(string(in.Pacing)),
		Location:       strings.TrimSpace(in.Location),
		CurrentStation: in.CurrentStation,
		Facts:          facts,
		LastStation:    in.LastStation,
		Architect:      in.Architect,
	})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func reviseRequest(prompt string, in ReviseNextInput) (string, string, error) {
	system, err := attachStaticContext(withPlayHints(prompt, in.Density, in.Pacing), sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	history := in.ChoiceHistory
	if history == nil {
		history = []store.PlayChoiceRecord{}
	}
	facts := in.Facts
	if facts == nil {
		facts = []store.PlayFact{}
	}
	threads := in.Threads
	if threads == nil {
		threads = []store.PlayThread{}
	}
	payload, err := marshalTurn(reviseTurn{
		Density:       store.NormalizePlayDensity(string(in.Density)),
		Pacing:        store.NormalizePlayPacing(string(in.Pacing)),
		Choice:        in.Choice,
		Facts:         facts,
		ChoiceHistory: history,
		RecentBeats:   slimBeats(in.RecentBeats),
		NextStation:   in.NextStation,
		Threads:       threads,
		Throughline:   strings.TrimSpace(in.Throughline),
	})
	if err != nil {
		return "", "", err
	}
	return system, payload, nil
}

func replanRequest(prompt string, in ReplanInput) (string, string, error) {
	system, err := attachStaticContext(withPlayHints(prompt, in.Density, in.Pacing), sharedCard(in.Character, in.Premise, in.UserPersona))
	if err != nil {
		return "", "", err
	}
	history := in.ChoiceHistory
	if history == nil {
		history = []store.PlayChoiceRecord{}
	}
	facts := in.Facts
	if facts == nil {
		facts = []store.PlayFact{}
	}
	threads := in.Threads
	if threads == nil {
		threads = []store.PlayThread{}
	}
	kept := in.KeptStations
	if kept == nil {
		kept = []store.PlayStation{}
	}
	remaining := in.RemainingStations
	if remaining == nil {
		remaining = []store.PlayStation{}
	}
	payload, err := marshalTurn(replanTurn{
		Density:           store.NormalizePlayDensity(string(in.Density)),
		Pacing:            store.NormalizePlayPacing(string(in.Pacing)),
		Instruction:       strings.TrimSpace(in.Instruction),
		Throughline:       strings.TrimSpace(in.Throughline),
		KeptStations:      kept,
		RemainingStations: remaining,
		Threads:           threads,
		Facts:             facts,
		ChoiceHistory:     history,
		RecentBeats:       slimBeats(in.RecentBeats),
	})
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
