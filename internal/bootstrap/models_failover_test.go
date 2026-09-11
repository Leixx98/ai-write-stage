package bootstrap

import (
	"context"
	"fmt"
	"testing"

	"github.com/voocel/agentcore"
)

type failoverCapabilityModel struct {
	supportsTools bool
	err           error
	calls         int
}

func (m *failoverCapabilityModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	m.calls++
	if m.err != nil {
		return nil, m.err
	}
	return &agentcore.LLMResponse{}, nil
}

func (m *failoverCapabilityModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	return nil, m.err
}

func (m *failoverCapabilityModel) SupportsTools() bool { return m.supportsTools }

func TestFailoverSkipsModelWithoutRequiredToolSupport(t *testing.T) {
	primaryModel := &failoverCapabilityModel{supportsTools: true, err: fmt.Errorf("rate limit")}
	fallbackModel := &failoverCapabilityModel{supportsTools: false}
	primary := NewSwappableModel("primary", "p", primaryModel, nil)
	set := &ModelSet{fallbacks: map[string][]modelTarget{
		"writer": {{provider: "fallback", name: "f", model: fallbackModel}},
	}}
	model := &failoverModel{role: "writer", primary: primary, set: set}
	if _, err := model.Generate(context.Background(), nil, []agentcore.ToolSpec{{Name: "write"}}); err == nil {
		t.Fatal("expected the primary error when no tool-capable fallback exists")
	}
	if fallbackModel.calls != 0 {
		t.Fatalf("tool-incompatible fallback was called %d times", fallbackModel.calls)
	}
}
