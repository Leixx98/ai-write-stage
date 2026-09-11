package agents

import (
	"context"
	"strings"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

type summaryCaptureModel struct {
	calls int
}

func (m *summaryCaptureModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	return &agentcore.LLMResponse{Message: agentcore.Message{
		Role:       agentcore.RoleAssistant,
		Content:    []agentcore.ContentBlock{agentcore.TextBlock("<summary>已恢复当前写作状态</summary>")},
		StopReason: agentcore.StopReasonStop,
	}}, nil
}

func (m *summaryCaptureModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, nil
}

func (m *summaryCaptureModel) SupportsTools() bool { return false }

func TestRoleContextWindowUsesConfiguredProviderAlias(t *testing.T) {
	models, err := bootstrap.NewModelSet(bootstrap.Config{
		Provider:  "cloud",
		ModelName: "cloud-model",
		Providers: map[string]bootstrap.ProviderConfig{
			"cloud": {
				Type: "openai", APIKey: "test", BaseURL: "https://example.com/v1",
				Models: []bootstrap.ModelConfig{{Name: "cloud-model", ContextWindow: 128000}},
			},
			"llamacpp": {
				Type: "openai", APIKey: "test", BaseURL: "http://127.0.0.1:8080/v1",
				Models: []bootstrap.ModelConfig{{Name: "local-writer", ContextWindow: 8192}},
			},
		},
		Roles: map[string]bootstrap.RoleConfig{
			"writer": {Provider: "llamacpp", Model: "local-writer"},
		},
	})
	if err != nil {
		t.Fatalf("NewModelSet: %v", err)
	}
	if got := roleContextWindow(models, "writer"); got != 8192 {
		t.Fatalf("writer context window = %d, want configured llamacpp window 8192", got)
	}
}

func TestWriterContextBudgetsScaleToLocalWindow(t *testing.T) {
	keep, summary := writerContextBudgets(8192)
	if keep != 2048 || summary != 1365 {
		t.Fatalf("8K budgets = keep:%d summary:%d", keep, summary)
	}
	keep, summary = writerContextBudgets(32768)
	if keep != 12384 || summary != 7000 {
		t.Fatalf("32K budgets = keep:%d summary:%d", keep, summary)
	}
}

func TestLocalWriterOverflowSummaryBypassesUnitBudget(t *testing.T) {
	base := &summaryCaptureModel{}
	workerModel := newWriterBudgetModel(base, func() int { return 8192 })
	manager := newContextManager(contextManagerConfig{
		SummaryModel:    writerSummaryModel(workerModel),
		ContextWindow:   8192,
		ReserveTokens:   4096,
		CommitProjected: true,
		Summary:         &corecontext.FullSummaryConfig{},
	})
	messages := []agentcore.AgentMessage{
		agentcore.UserMsg(strings.Repeat("旧剧情上下文", 1500)),
		agentcore.Message{Role: agentcore.RoleAssistant, Content: []agentcore.ContentBlock{agentcore.TextBlock(strings.Repeat("旧章节正文", 1500))}},
		agentcore.UserMsg("继续当前写作片段"),
	}
	projection, err := manager.Project(context.Background(), messages)
	if err != nil {
		t.Fatalf("overflow summary: %v", err)
	}
	if base.calls == 0 || !projection.ShouldCommit {
		t.Fatalf("summary calls=%d should_commit=%t", base.calls, projection.ShouldCommit)
	}
}
