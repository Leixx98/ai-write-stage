package play

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/galgame/runlog"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/llmretry"
	"github.com/voocel/ainovel-cli/internal/store"
)

const playMaxTokens = 16384

type SpineInput struct {
	Character   store.GalgameCharacter
	Premise     string
	UserPersona string
	Density     store.PlayDensity
}

type ArchitectInput struct {
	Character         store.GalgameCharacter
	Premise           string
	UserPersona       string
	Density           store.PlayDensity
	CurrentStation    store.PlayStation
	RemainingStations []store.PlayStation
	Facts             []store.PlayFact
	ChoiceHistory     []store.PlayChoiceRecord
	RecentBeats       []store.PlayBeat
	LastGoal          string
}

type PlannerInput struct {
	Architect      ArchitectOutput
	Character      store.GalgameCharacter
	Premise        string
	UserPersona    string
	Location       string
	Density        store.PlayDensity
	CurrentStation store.PlayStation
	Facts          []store.PlayFact
	LastStation    bool
}

type WriterInput struct {
	Card        store.PlayBeatCard
	Character   store.GalgameCharacter
	Premise     string
	UserPersona string
}

type Generator struct {
	ArchitectModel    agentcore.ChatModel
	PlannerModel      agentcore.ChatModel
	WriterModel       agentcore.ChatModel
	SpinePrompt       string
	ArchitectPrompt   string
	PlannerPrompt     string
	WriterPrompt      string
	Density           store.PlayDensity
	ArchitectThinking agentcore.ThinkingLevel
	PlannerThinking   agentcore.ThinkingLevel
	WriterThinking    agentcore.ThinkingLevel
	PlayID            string
	Store             *store.GalgameStore
	ContextWindow     int
	Sink              runlog.Sink
}

func (g Generator) Spine(ctx context.Context, in SpineInput) (SpineOutput, error) {
	system, payload, err := spineRequest(g.SpinePrompt, in)
	if err != nil {
		return SpineOutput{}, err
	}
	return execute(ctx, g, g.ArchitectModel, spineContract, system, payload, nil, (*SpineOutput).Validate)
}

func (g Generator) Architect(ctx context.Context, in ArchitectInput) (ArchitectOutput, error) {
	system, payload, err := architectRequest(g.ArchitectPrompt, in)
	if err != nil {
		return ArchitectOutput{}, err
	}
	return execute(ctx, g, g.ArchitectModel, architectContract, system, payload, nil, (*ArchitectOutput).Validate)
}

func (g Generator) Planner(ctx context.Context, in PlannerInput) (PlannerOutput, error) {
	system, payload, err := plannerRequest(g.PlannerPrompt, in)
	if err != nil {
		return PlannerOutput{}, err
	}
	profile := profileFor(in.Density)
	return execute(ctx, g, g.PlannerModel, plannerContract, system, payload, nil, func(out *PlannerOutput) error {
		out.Cards = repairPlannerCards(out.Cards, profile.FillEmptyCG)
		if err := validateCardCount(len(out.Cards), profile); err != nil {
			return err
		}
		return validatePlannerAgainstArchitect(*out, in.Architect, in.LastStation)
	})
}

func (g Generator) Writer(ctx context.Context, in WriterInput) (WriterOutput, error) {
	system, payload, err := writerRequest(g.WriterPrompt, in)
	if err != nil {
		return WriterOutput{}, err
	}
	session, err := g.loadWriterSession()
	if err != nil {
		return WriterOutput{}, err
	}
	compacted := compactWriterTurns(session.Turns, system, payload, g.ContextWindow)
	compacted = capWriterTurns(compacted, profileFor(g.Density).WriterTurns)
	if len(compacted) != len(session.Turns) {
		session.Turns = compacted
		if err := g.saveWriterSession(session); err != nil {
			return WriterOutput{}, err
		}
	}
	out, err := execute(ctx, g, g.WriterModel, writerContract, system, payload, writerHistoryMessages(session.Turns), (*WriterOutput).Validate)
	if err != nil {
		return WriterOutput{}, err
	}
	session.Turns = append(session.Turns, store.PlayWriterTurn{Card: in.Card, Speaker: out.Speaker, Text: out.Text})
	if err := g.saveWriterSession(session); err != nil {
		return WriterOutput{}, err
	}
	return out, nil
}

func execute[T any](ctx context.Context, g Generator, model agentcore.ChatModel, contract llmcontract.Contract, systemPrompt, payload string, history []agentcore.Message, validate func(*T) error) (T, error) {
	var zero T
	if model == nil {
		return zero, fmt.Errorf("play model is unavailable")
	}
	thinking := g.thinking(contract.Name)
	call := runlog.NewCall(runlog.Record{
		Mode:        runlog.ModePlay,
		Step:        contract.Name,
		PlayID:      g.PlayID,
		Thinking:    string(thinking),
		MaxTokens:   playMaxTokens,
		PromptChars: utf8.RuneCountInString(systemPrompt) + utf8.RuneCountInString(payload) + historyChars(history),
		Streaming:   true,
	})
	runlog.Start(g.Sink, call)
	transcript := runlog.NewTranscript(g.Sink, g.PlayID)
	transcript.Begin(contract.Name, call.Snapshot().CallID)
	gotThinking, gotText := false, false
	stopHB := runlog.Heartbeat(g.Sink, call, runlog.HeartbeatInterval)
	defer stopHB()
	options := []agentcore.CallOption{agentcore.WithThinking(thinking), agentcore.WithMaxTokens(playMaxTokens)}
	if key := playCacheKey(g.PlayID, contract.Name); key != "" {
		options = append(options, agentcore.WithCallPromptCacheKey(key))
	}
	out, err := llmcontract.ExecuteStream(ctx, model, llmcontract.Request[T]{
		Contract:         contract,
		SystemPrompt:     strings.TrimSpace(systemPrompt),
		Payload:          payload,
		History:          history,
		Options:          options,
		CacheLastMessage: "ephemeral",
		Validate:         validate,
		Agent:            "galplay",
		Hooks: llmcontract.Hooks{
			Resolved: func(res llmcontract.Resolution) {
				call.Update(func(rec *runlog.Record) {
					rec.Provider = res.Provider
					rec.Model = res.Model
					rec.Protocol = string(res.Mode)
				})
			},
			RequestRetry: func(ev llmretry.Event) {
				runlog.Retry(g.Sink, call, ev.Attempt, ev.Delay, ev.Err)
				transcript.Note(fmt.Sprintf("request retry attempt=%d", ev.Attempt))
				gotThinking, gotText = false, false
			},
			Correction: func(item llmcontract.Correction) {
				errText := ""
				if item.Err != nil {
					errText = item.Err.Error()
				}
				runlog.Correction(g.Sink, call, item.Attempt, item.Layer, errText, utf8.RuneCountInString(item.Raw))
				transcript.Note(fmt.Sprintf("correction attempt=%d layer=%s raw_chars=%d", item.Attempt, item.Layer, utf8.RuneCountInString(item.Raw)))
				gotThinking, gotText = false, false
			},
			Repair: func(item llmcontract.Repair) {
				runlog.Repaired(g.Sink, call, item.Rules, item.RawChars, item.BodyChars)
				transcript.Note(fmt.Sprintf("json repaired rules=%s raw_chars=%d", strings.Join(item.Rules, ","), item.RawChars))
			},
			Response: func(resp *agentcore.LLMResponse) {
				call.ApplyResponse(resp)
				transcript.FillMissing(resp, !gotThinking, !gotText)
			},
			Stream: func(ev agentcore.StreamEvent) {
				if ev.Delta == "" {
					return
				}
				if ev.Type == agentcore.StreamEventThinkingDelta {
					gotThinking = true
					runlog.MarkFirstToken(g.Sink, call)
					transcript.Feed(ev)
					return
				}
				if ev.Type == agentcore.StreamEventTextDelta {
					gotText = true
					runlog.MarkFirstToken(g.Sink, call)
					transcript.Feed(ev)
				}
			},
		},
	})
	stopHB()
	runlog.Finish(g.Sink, call, err)
	if err != nil {
		return zero, fmt.Errorf("play %s: %w", contract.Name, err)
	}
	return out, nil
}

func (g Generator) thinking(name string) agentcore.ThinkingLevel {
	switch name {
	case spineContract.Name, architectContract.Name:
		return g.ArchitectThinking
	case plannerContract.Name:
		return g.PlannerThinking
	case writerContract.Name:
		return g.WriterThinking
	default:
		return ""
	}
}
