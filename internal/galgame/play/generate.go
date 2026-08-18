package play

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

const playMaxTokens = 16384

type ArchitectInput struct {
	Character     store.GalgameCharacter
	Premise       string
	UserPersona   string
	ChoiceHistory []store.PlayChoiceRecord
	RecentBeats   []store.PlayBeat
	LastGoal      string
}

type PlannerInput struct {
	Architect ArchitectOutput
	Character store.GalgameCharacter
	Premise   string
	Location  string
}

type WriterInput struct {
	Card        store.PlayBeatCard
	Character   store.GalgameCharacter
	Premise     string
	UserPersona string
	RecentBeats []store.PlayBeat
}

type Generator struct {
	ArchitectModel  agentcore.ChatModel
	PlannerModel    agentcore.ChatModel
	WriterModel     agentcore.ChatModel
	ArchitectPrompt string
	PlannerPrompt   string
	WriterPrompt    string
}

func (g Generator) Architect(ctx context.Context, in ArchitectInput) (ArchitectOutput, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return ArchitectOutput{}, err
	}
	return execute(ctx, g.ArchitectModel, architectContract, g.ArchitectPrompt, string(payload), (*ArchitectOutput).Validate)
}

func (g Generator) Planner(ctx context.Context, in PlannerInput) (PlannerOutput, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return PlannerOutput{}, err
	}
	out, err := execute(ctx, g.PlannerModel, plannerContract, g.PlannerPrompt, string(payload), (*PlannerOutput).Validate)
	if err != nil {
		return PlannerOutput{}, err
	}
	if err := validatePlannerAgainstArchitect(out, in.Architect); err != nil {
		return PlannerOutput{}, err
	}
	return out, nil
}

func (g Generator) Writer(ctx context.Context, in WriterInput) (WriterOutput, error) {
	payload, err := json.Marshal(in)
	if err != nil {
		return WriterOutput{}, err
	}
	return execute(ctx, g.WriterModel, writerContract, g.WriterPrompt, string(payload), (*WriterOutput).Validate)
}

func execute[T any](ctx context.Context, model agentcore.ChatModel, contract llmcontract.Contract, systemPrompt, payload string, validate func(*T) error) (T, error) {
	var zero T
	if model == nil {
		return zero, fmt.Errorf("play model is unavailable")
	}
	out, err := llmcontract.Execute(ctx, model, llmcontract.Request[T]{
		Contract:     contract,
		SystemPrompt: strings.TrimSpace(systemPrompt),
		Payload:      payload,
		Options:      []agentcore.CallOption{agentcore.WithMaxTokens(playMaxTokens)},
		Validate:     validate,
		Agent:        "galplay",
	})
	if err != nil {
		return zero, fmt.Errorf("play %s: %w", contract.Name, err)
	}
	return out, nil
}
