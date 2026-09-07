package agents

import (
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
)

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
