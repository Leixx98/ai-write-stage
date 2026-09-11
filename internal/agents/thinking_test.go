package agents

import (
	"context"
	"testing"

	"github.com/Leixx98/ai-write-stage/assets"
	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/Leixx98/ai-write-stage/internal/tools"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type thinkingCapsModel struct {
	caps llm.Capabilities
}

func TestBuildWorkersAppliesExplicitOffToEveryWorker(t *testing.T) {
	cfg := bootstrap.Config{
		Provider: "proxy", ModelName: "model", ReasoningEffort: "off",
		Providers: map[string]bootstrap.ProviderConfig{
			"proxy": {Type: "openai", APIKey: "test", BaseURL: "https://example.com/v1", Models: []bootstrap.ModelConfig{{Name: "model", ContextWindow: 8192}}},
		},
	}
	models, err := bootstrap.NewModelSet(cfg)
	if err != nil {
		t.Fatal(err)
	}
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	runner, _, _ := BuildWorkers(cfg, st, tools.NewStyleStatsIndex(st), models, assets.Bundle{Prompts: assets.Prompts{ChapterPlanner: "planner-base"}}, nil, nil)
	for _, name := range []string{"architect_short", "architect_long", "chapter_planner", "writer"} {
		worker, ok := runner.AgentConfig(name)
		if !ok {
			t.Fatalf("worker %s is not registered", name)
		}
		if worker.ThinkingLevel != agentcore.ThinkingOff {
			t.Fatalf("worker %s thinking = %q, want off", name, worker.ThinkingLevel)
		}
	}
	planner, _ := runner.AgentConfig("chapter_planner")
	if planner.SystemPrompt != "planner-base" {
		t.Fatalf("chapter planner prompt contains stale dynamic budget: %q", planner.SystemPrompt)
	}
}

func (m thinkingCapsModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return nil, nil
}

func (m thinkingCapsModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m thinkingCapsModel) SupportsTools() bool { return false }

func (m thinkingCapsModel) Capabilities() llm.Capabilities { return m.caps }

func TestEffectiveThinkingKeepsExplicitOff(t *testing.T) {
	chat := thinkingCapsModel{caps: llm.Capabilities{Thinking: llm.ThinkingCapabilities{Supported: llm.SupportNo}}}
	if got, ok := ResolveThinkingForModel(chat, agentcore.ThinkingOff); got != "" || ok {
		t.Fatalf("catalog clamp off → auto, got %q ok=%v", got, ok)
	}
	if got := EffectiveThinking(chat, agentcore.ThinkingOff); got != agentcore.ThinkingOff {
		t.Fatalf("user off must be forwarded, got %q", got)
	}
	if got := EffectiveThinking(chat, ""); got != "" {
		t.Fatalf("empty should stay auto, got %q", got)
	}
	if opts := ThinkingCallOptions(agentcore.ThinkingOff); len(opts) != 1 {
		t.Fatalf("off should send a thinking option, got %d", len(opts))
	}
	if opts := ThinkingCallOptions(""); len(opts) != 0 {
		t.Fatalf("auto should omit thinking option, got %d", len(opts))
	}
	available := AvailableThinkingForModel(chat)
	if len(available) != 2 || available[0] != agentcore.ThinkingAuto || available[1] != agentcore.ThinkingOff {
		t.Fatalf("support-no gateway should still expose explicit off, got %v", available)
	}
}
