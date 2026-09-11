package agents

import (
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/voocel/agentcore"
	"github.com/voocel/agentcore/llm"
)

func TestDynamicRoleModelFollowsAddedAndRemovedOverride(t *testing.T) {
	base := bootstrap.Config{
		Provider: "proxy", ModelName: "default-model",
		Providers: map[string]bootstrap.ProviderConfig{
			"proxy": {Type: "openai", APIKey: "test", BaseURL: "https://example.com/v1", Models: []bootstrap.ModelConfig{
				{Name: "default-model", ContextWindow: 128000},
				{Name: "writer-model", ContextWindow: 8192},
			}},
		},
	}
	models, err := bootstrap.NewModelSet(base)
	if err != nil {
		t.Fatal(err)
	}
	dynamic := newDynamicRoleModel(func() agentcore.ChatModel {
		return models.ForRoleWithFailover("writer", nil)
	}).(interface{ Info() llm.ModelInfo })
	if got := dynamic.Info().Name; got != "default-model" {
		t.Fatalf("initial model = %q", got)
	}

	withWriter := base
	withWriter.Roles = map[string]bootstrap.RoleConfig{"writer": {Provider: "proxy", Model: "writer-model"}}
	candidate, err := bootstrap.NewModelSet(withWriter)
	if err != nil {
		t.Fatal(err)
	}
	models.ApplyPrepared(candidate)
	if got := dynamic.Info().Name; got != "writer-model" {
		t.Fatalf("added override model = %q", got)
	}

	withoutWriter, err := bootstrap.NewModelSet(base)
	if err != nil {
		t.Fatal(err)
	}
	models.ApplyPrepared(withoutWriter)
	if got := dynamic.Info().Name; got != "default-model" {
		t.Fatalf("removed override model = %q", got)
	}
}
