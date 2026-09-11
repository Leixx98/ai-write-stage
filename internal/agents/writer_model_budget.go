package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"unicode"

	"github.com/Leixx98/ai-write-stage/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// writerBudgetModel applies a request-local output ceiling after accounting
// for the current prompt. It is the final wrapper before the Writer model, so
// its WithMaxTokens option overrides a larger provider default.
type writerBudgetModel struct {
	inner         agentcore.ChatModel
	contextWindow func() int
}

func newWriterBudgetModel(inner agentcore.ChatModel, contextWindow func() int) agentcore.ChatModel {
	return &writerBudgetModel{inner: inner, contextWindow: contextWindow}
}

func (m *writerBudgetModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	limited, err := m.limitOptions(messages, tools, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.Generate(ctx, messages, tools, limited...)
}

func (m *writerBudgetModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	limited, err := m.limitOptions(messages, tools, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.GenerateStream(ctx, messages, tools, limited...)
}

func (m *writerBudgetModel) limitOptions(messages []agentcore.Message, tools []agentcore.ToolSpec, opts []agentcore.CallOption) ([]agentcore.CallOption, error) {
	window := 0
	if m.contextWindow != nil {
		window = m.contextWindow()
	}
	if window <= 0 {
		return opts, nil
	}
	input := estimateWriterRequestTokens(messages, tools)
	limit, err := writerOutputTokenLimit(window, input)
	if err != nil {
		slog.Error("Writer 请求超过安全输入上限", "module", "agent.writer_budget", "context_window", window, "estimated_input", input, "err", err)
		return nil, err
	}
	if configured := agentcore.ResolveCallConfig(opts).MaxTokens; configured > 0 && configured < limit {
		limit = configured
	}
	limited := append([]agentcore.CallOption(nil), opts...)
	limited = append(limited, agentcore.WithMaxTokens(limit))
	slog.Debug("应用 Writer 动态输出预算", "module", "agent.writer_budget", "context_window", window, "estimated_input", input, "max_tokens", limit)
	return limited, nil
}

func writerOutputTokenLimit(window, estimatedInput int) (int, error) {
	maxOutput, minOutput, reserve := 6000, 1500, 2000
	switch {
	case window <= 8192:
		maxOutput, minOutput, reserve = 1800, 1000, 700
	case window <= 12288:
		maxOutput, minOutput, reserve = 2200, 1100, 900
	case window <= 16384:
		maxOutput, minOutput, reserve = 2400, 1200, 1000
	}
	available := window - estimatedInput - reserve
	if available < minOutput {
		return 0, fmt.Errorf("Writer 输入估算为 %d/%d tokens，预留 %d tokens 安全空间后仅剩 %d tokens 可输出；完整 unit 至少需要 %d tokens，请缩短规划卡或上下文后重试",
			estimatedInput, window, reserve, available, minOutput)
	}
	if available < maxOutput {
		return available, nil
	}
	return maxOutput, nil
}

// estimateWriterRequestTokens deliberately overestimates mixed Chinese/ASCII
// prompts. Qwen tokenization is close to one token per CJK rune and roughly one
// per four ASCII characters; a 20% CJK margin covers JSON/chat-template costs.
func estimateWriterRequestTokens(messages []agentcore.Message, tools []agentcore.ToolSpec) int {
	// After the first call, llama.cpp's reported usage is a substantially better
	// baseline than retokenizing the stable system prompt and tool schemas.
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Usage == nil || messages[i].Usage.Input <= 0 {
			continue
		}
		base := messages[i].Usage.Input + messages[i].Usage.Output
		trailingJSON, _ := json.Marshal(messages[i+1:])
		return base + estimateMixedTokens(trailingJSON) + len(messages[i+1:])*12 + 32
	}
	messageJSON, _ := json.Marshal(messages)
	toolJSON, _ := json.Marshal(tools)
	return estimateMixedTokens(messageJSON) + estimateMixedTokens(toolJSON) + 64 + len(messages)*12 + len(tools)*16
}

func estimateMixedTokens(data []byte) int {
	ascii, nonASCII := 0, 0
	for _, r := range string(data) {
		if r <= unicode.MaxASCII {
			ascii++
		} else {
			nonASCII++
		}
	}
	return (ascii+3)/4 + (nonASCII*6+4)/5
}

func (m *writerBudgetModel) SupportsTools() bool { return m.inner.SupportsTools() }

func (m *writerBudgetModel) ProviderName() string {
	if provider, ok := m.inner.(agentcore.ProviderNamer); ok {
		return provider.ProviderName()
	}
	return ""
}

func (m *writerBudgetModel) ModelName() string {
	if model, ok := m.inner.(agentcore.ModelNamer); ok {
		return model.ModelName()
	}
	return ""
}

func (m *writerBudgetModel) Info() llm.ModelInfo {
	if provider, ok := m.inner.(interface{ Info() llm.ModelInfo }); ok {
		return provider.Info()
	}
	return llm.ModelInfo{}
}

func (m *writerBudgetModel) Capabilities() llm.Capabilities {
	if provider, ok := m.inner.(llm.CapabilityProvider); ok {
		return provider.Capabilities()
	}
	return llm.Capabilities{}
}

func (m *writerBudgetModel) JSONSchemaOverride() *bool {
	if provider, ok := m.inner.(interface{ JSONSchemaOverride() *bool }); ok {
		return provider.JSONSchemaOverride()
	}
	return nil
}

func (m *writerBudgetModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if provider, ok := m.inner.(interface {
		StructuredOutputFacts() llmcontract.ModelFacts
	}); ok {
		return provider.StructuredOutputFacts()
	}
	return llmcontract.ModelFacts{
		Capabilities:       m.Capabilities(),
		Info:               m.Info(),
		JSONSchemaOverride: m.JSONSchemaOverride(),
	}
}
