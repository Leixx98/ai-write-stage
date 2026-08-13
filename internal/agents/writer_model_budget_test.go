package agents

import (
	"context"
	"testing"

	"github.com/voocel/agentcore"
)

type writerBudgetCaptureModel struct {
	config agentcore.CallConfig
}

func (m *writerBudgetCaptureModel) Generate(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.config = agentcore.ResolveCallConfig(opts)
	return &agentcore.LLMResponse{}, nil
}

func (m *writerBudgetCaptureModel) GenerateStream(_ context.Context, _ []agentcore.Message, _ []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	m.config = agentcore.ResolveCallConfig(opts)
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (*writerBudgetCaptureModel) SupportsTools() bool { return true }

func TestWriterOutputTokenLimitForEightKWindow(t *testing.T) {
	if got, err := writerOutputTokenLimit(8192, 5311); err != nil || got != 1800 {
		t.Fatalf("normal fresh-unit request limit = %d, err=%v; want 1800", got, err)
	}
	if got, err := writerOutputTokenLimit(8192, 6200); err != nil || got != 1292 {
		t.Fatalf("large request limit = %d, err=%v; want 1292", got, err)
	}
	if _, err := writerOutputTokenLimit(8192, 6500); err == nil {
		t.Fatal("request without room for one complete unit must be rejected")
	}
}

func TestWriterStopsAfterOneUnit(t *testing.T) {
	if !writerStopAfterToolResult("write_chapter_unit", nil) {
		t.Fatal("write_chapter_unit must end the current Writer run")
	}
	if writerStopAfterToolResult("novel_context", nil) || writerStopAfterToolResult("commit_chapter", nil) {
		t.Fatal("non-terminal Writer tools must not end the run")
	}
}

func TestWriterBudgetModelOverridesLargerProviderMaxTokens(t *testing.T) {
	capture := &writerBudgetCaptureModel{}
	model := newWriterBudgetModel(capture, func() int { return 8192 })
	_, err := model.Generate(context.Background(), []agentcore.Message{agentcore.UserMsg("写当前片段")}, nil, agentcore.WithMaxTokens(4096))
	if err != nil {
		t.Fatal(err)
	}
	if capture.config.MaxTokens != 1800 {
		t.Fatalf("Writer max_tokens = %d, want dynamic cap 1800", capture.config.MaxTokens)
	}
}

func TestWriterBudgetModelPreservesLargeCloudWindowOutputSetting(t *testing.T) {
	capture := &writerBudgetCaptureModel{}
	model := newWriterBudgetModel(capture, func() int { return 200000 })
	_, err := model.Generate(context.Background(), []agentcore.Message{agentcore.UserMsg("重写整章")}, nil, agentcore.WithMaxTokens(12000))
	if err != nil {
		t.Fatal(err)
	}
	if capture.config.MaxTokens != 12000 {
		t.Fatalf("large cloud Writer max_tokens = %d, want unchanged 12000", capture.config.MaxTokens)
	}
}

func TestEstimateWriterRequestTokensUsesReportedUsageBaseline(t *testing.T) {
	messages := []agentcore.Message{
		{
			Role:    agentcore.RoleAssistant,
			Content: []agentcore.ContentBlock{agentcore.TextBlock("调用上下文工具")},
			Usage:   &agentcore.Usage{Input: 4328, Output: 43},
		},
		agentcore.ToolResultMsg("context-call", []byte(`{"current_unit":"简洁执行卡"}`), false),
	}
	got := estimateWriterRequestTokens(messages, []agentcore.ToolSpec{{Name: "ignored-after-real-usage"}})
	if got <= 4371 || got >= 5000 {
		t.Fatalf("usage-based estimate = %d, want reported baseline plus only the short trailing result", got)
	}
}
