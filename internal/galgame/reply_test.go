package galgame

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestReplyAssemblesPromptAndReturnsText(t *testing.T) {
	var got []agentcore.Message
	generate := func(_ context.Context, msgs []agentcore.Message) (string, error) {
		got = msgs
		return "  在。  ", nil
	}
	character := store.GalgameCharacter{Name: "林晚", Prompt: "冷静的情报员", Scenario: "雨夜码头"}
	session := store.GalgameSession{
		StoryPreset: "追查失踪货柜",
		Messages: []store.GalgameMessage{
			{Role: "user", Content: "你是谁"},
			{Role: "assistant", Content: "林晚。"},
			{Role: "user", Content: "现在呢"},
		},
	}
	text, err := Reply(context.Background(), generate, character, session, "看那边")
	if err != nil {
		t.Fatal(err)
	}
	if text != "在。" {
		t.Fatalf("reply = %q", text)
	}
	if len(got) < 2 || got[0].Role != agentcore.RoleSystem {
		t.Fatalf("messages = %+v", got)
	}
	system := got[0].Content[0].Text
	if !strings.Contains(system, "冷静的情报员") || !strings.Contains(system, "雨夜码头") || !strings.Contains(system, "追查失踪货柜") {
		t.Fatalf("system prompt = %q", system)
	}
	if got[len(got)-1].Role != agentcore.RoleUser {
		t.Fatalf("last role = %s", got[len(got)-1].Role)
	}
}

func TestReplyRejectsEmptyInput(t *testing.T) {
	_, err := Reply(context.Background(), func(context.Context, []agentcore.Message) (string, error) {
		return "x", nil
	}, store.GalgameCharacter{Prompt: "p"}, store.GalgameSession{}, "  ")
	if err == nil {
		t.Fatal("expected empty input error")
	}
}
