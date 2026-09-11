package agents

import (
	"context"

	"github.com/Leixx98/ai-write-stage/internal/llmcontract"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

// dynamicRoleModel resolves the effective role model for every request. This
// keeps long-lived workers correct when a role override is added or removed.
type dynamicRoleModel struct {
	resolve func() agentcore.ChatModel
}

func newDynamicRoleModel(resolve func() agentcore.ChatModel) agentcore.ChatModel {
	return &dynamicRoleModel{resolve: resolve}
}

func (m *dynamicRoleModel) current() agentcore.ChatModel {
	if m == nil || m.resolve == nil {
		return nil
	}
	return m.resolve()
}

func (m *dynamicRoleModel) Generate(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	current := m.current()
	if current == nil {
		return nil, agentcore.ErrNoModel
	}
	return current.Generate(ctx, messages, tools, opts...)
}

func (m *dynamicRoleModel) GenerateStream(ctx context.Context, messages []agentcore.Message, tools []agentcore.ToolSpec, opts ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	current := m.current()
	if current == nil {
		return nil, agentcore.ErrNoModel
	}
	return current.GenerateStream(ctx, messages, tools, opts...)
}

func (m *dynamicRoleModel) SupportsTools() bool {
	current := m.current()
	return current != nil && current.SupportsTools()
}

func (m *dynamicRoleModel) ProviderName() string {
	if current, ok := m.current().(agentcore.ProviderNamer); ok {
		return current.ProviderName()
	}
	return ""
}

func (m *dynamicRoleModel) ModelName() string {
	if current, ok := m.current().(agentcore.ModelNamer); ok {
		return current.ModelName()
	}
	return ""
}

func (m *dynamicRoleModel) Info() llm.ModelInfo {
	if current, ok := m.current().(interface{ Info() llm.ModelInfo }); ok {
		return current.Info()
	}
	return llm.ModelInfo{}
}

func (m *dynamicRoleModel) Capabilities() llm.Capabilities {
	if current, ok := m.current().(llm.CapabilityProvider); ok {
		return current.Capabilities()
	}
	return llm.Capabilities{}
}

func (m *dynamicRoleModel) JSONSchemaOverride() *bool {
	if current, ok := m.current().(interface{ JSONSchemaOverride() *bool }); ok {
		return current.JSONSchemaOverride()
	}
	return nil
}

func (m *dynamicRoleModel) StructuredOutputFacts() llmcontract.ModelFacts {
	if current, ok := m.current().(interface {
		StructuredOutputFacts() llmcontract.ModelFacts
	}); ok {
		return current.StructuredOutputFacts()
	}
	return llmcontract.ModelFacts{
		Capabilities:       m.Capabilities(),
		Info:               m.Info(),
		JSONSchemaOverride: m.JSONSchemaOverride(),
	}
}
