package imagejob

import "testing"

func TestBuildPromptSchemaAndHashAreDeterministic(t *testing.T) {
	fields := []PromptField{
		{ID: "negative_prompt", ValueType: "string", Control: "textarea", Exposed: true, Source: "canvas"},
		{ID: "positive_prompt", ValueType: "string", Control: "textarea", Exposed: true, Source: "prompter"},
		{ID: "seed", ValueType: "integer", Control: "number", Exposed: true, Source: "default"},
	}
	first, err := BuildPromptSchema("wf", fields, "", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildPromptSchema("wf", fields, "", "")
	if err != nil || first.SchemaHash != second.SchemaHash {
		t.Fatalf("schema hash is not deterministic: %v %v", first.SchemaHash, second.SchemaHash)
	}
	if len(first.Fields) != 3 || first.Fields[0].ID != "negative_prompt" || first.Fields[1].ID != "positive_prompt" || first.Fields[2].ID != "seed" {
		t.Fatalf("unexpected fields: %#v", first.Fields)
	}
}

func TestBuildPromptSchemaRejectsInvalidID(t *testing.T) {
	_, err := BuildPromptSchema("wf", []PromptField{{
		ID: "7::text", ValueType: "string", Control: "textarea", Exposed: true, Source: "prompter",
	}}, "", "")
	if err == nil {
		t.Fatal("expected invalid field id")
	}
}
