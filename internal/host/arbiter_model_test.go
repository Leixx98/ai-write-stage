package host

import (
	"context"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

type plainTrackedTestModel struct{}

func (*plainTrackedTestModel) Generate(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (*agentcore.LLMResponse, error) {
	return &agentcore.LLMResponse{}, nil
}

func (*plainTrackedTestModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent)
	close(ch)
	return ch, nil
}

func (*plainTrackedTestModel) SupportsTools() bool { return true }

type capableTrackedTestModel struct {
	*plainTrackedTestModel
	caps llm.Capabilities
}

func (m *capableTrackedTestModel) Capabilities() llm.Capabilities { return m.caps }

func TestUsageTrackedModelPreservesOptionalCapabilities(t *testing.T) {
	want := llm.Capabilities{
		Provider: "openai",
		Model:    "gpt-chat",
		Thinking: llm.ThinkingCapabilities{Supported: llm.SupportNo},
	}
	inner := &capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}, caps: want}
	wrapped := newUsageTrackedModel(inner, "arbiter", func(string, string, agentcore.AgentMessage) {})
	cp, ok := wrapped.(llm.CapabilityProvider)
	if !ok {
		t.Fatal("usage wrapper dropped CapabilityProvider")
	}
	if got := cp.Capabilities(); got.Provider != want.Provider || got.Model != want.Model || got.Thinking.Supported != llm.SupportNo {
		t.Fatalf("capabilities changed through wrapper: %+v", got)
	}

	plain := newUsageTrackedModel(&plainTrackedTestModel{}, "arbiter", func(string, string, agentcore.AgentMessage) {})
	if _, ok := plain.(llm.CapabilityProvider); ok {
		t.Fatal("wrapper must not invent capabilities for an unknown model")
	}
}

type overrideCapableTestModel struct {
	*capableTrackedTestModel
	override *bool
}

func (m *overrideCapableTestModel) JSONSchemaOverride() *bool { return m.override }

// usage 包装器必须透传 config json_schema 覆盖值；inner 未携带时返回 nil
// （"未配置"），不伪造能力。
func TestUsageTrackedModelForwardsJSONSchemaOverride(t *testing.T) {
	tr := true
	inner := &overrideCapableTestModel{
		capableTrackedTestModel: &capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}},
		override:                &tr,
	}
	wrapped := newUsageTrackedModel(inner, "arbiter", func(string, string, agentcore.AgentMessage) {})
	o, ok := wrapped.(interface{ JSONSchemaOverride() *bool })
	if !ok {
		t.Fatal("usage wrapper dropped JSONSchemaOverride")
	}
	if v := o.JSONSchemaOverride(); v == nil || !*v {
		t.Fatalf("override 未透传: %v", v)
	}

	capsOnly := newUsageTrackedModel(&capableTrackedTestModel{plainTrackedTestModel: &plainTrackedTestModel{}}, "arbiter", func(string, string, agentcore.AgentMessage) {})
	o, ok = capsOnly.(interface{ JSONSchemaOverride() *bool })
	if !ok {
		t.Fatal("usage wrapper dropped JSONSchemaOverride")
	}
	if o.JSONSchemaOverride() != nil {
		t.Fatal("missing override must stay nil")
	}
}

func TestUsageTrackedModelRecordsStreamDone(t *testing.T) {
	inner := &streamRecordTestModel{}
	var recorded agentcore.AgentMessage
	wrapped := newUsageTrackedModel(inner, "galgame", func(_ string, _ string, msg agentcore.AgentMessage) {
		recorded = msg
	})
	ch, err := wrapped.GenerateStream(context.Background(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	if recorded == nil || recorded.TextContent() != "在" {
		t.Fatalf("stream usage not recorded: %#v", recorded)
	}
}

type streamRecordTestModel struct{ plainTrackedTestModel }

func (m *streamRecordTestModel) GenerateStream(context.Context, []agentcore.Message, []agentcore.ToolSpec, ...agentcore.CallOption) (<-chan agentcore.StreamEvent, error) {
	ch := make(chan agentcore.StreamEvent, 2)
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventTextDelta, Delta: "在"}
	ch <- agentcore.StreamEvent{Type: agentcore.StreamEventDone, Message: agentcore.Message{
		Role:    agentcore.RoleAssistant,
		Content: []agentcore.ContentBlock{agentcore.TextBlock("在")},
		Usage:   &agentcore.Usage{Input: 10, Output: 2},
	}}
	close(ch)
	return ch, nil
}
