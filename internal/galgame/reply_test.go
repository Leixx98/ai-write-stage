package galgame

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/voocel/agentcore"
	"github.com/voocel/ainovel-cli/internal/store"
)

func TestReplyAssemblesSupportedFieldsInOrder(t *testing.T) {
	var got []agentcore.Message
	generate := func(_ context.Context, messages []agentcore.Message) (string, error) {
		got = messages
		return "  在。  ", nil
	}
	character := store.GalgameCharacter{
		Name: "林晚", Description: "情报员", Personality: "冷静",
		Scenario: "雨夜码头", ExampleDialogue: "{{user}}: 你好\n{{char}}: 嗯。",
		SystemPrompt: "扮演{{char}}。{{original}}", PostHistoryInstructions: "只回复{{char}}的行动和台词。",
	}
	session := store.GalgameSession{
		UserPersona: "旅人",
		Messages:    []store.GalgameMessage{{Role: "user", Content: "你是谁"}, {Role: "assistant", Content: "林晚。"}},
	}
	text, err := Reply(context.Background(), generate, character, session, "看那边", ReplyOptions{ContextWindow: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if text != "在。" {
		t.Fatalf("reply = %q", text)
	}
	system := got[0].TextContent()
	for _, expected := range []string{"扮演林晚", "Write 林晚's next reply", "情报员", "冷静", "雨夜码头", "旅人"} {
		if !strings.Contains(system, expected) {
			t.Fatalf("system prompt missing %q:\n%s", expected, system)
		}
	}
	if got[1].Role != agentcore.RoleSystem || !strings.Contains(got[1].TextContent(), "旅人: 你好") {
		t.Fatalf("examples = %#v", got[1])
	}
	if got[len(got)-2].Role != agentcore.RoleSystem || !strings.Contains(got[len(got)-2].TextContent(), "林晚") {
		t.Fatalf("post-history instructions = %#v", got[len(got)-2])
	}
	if got[len(got)-1].Role != agentcore.RoleUser || got[len(got)-1].TextContent() != "看那边" {
		t.Fatalf("last message = %#v", got[len(got)-1])
	}
}

func TestReplyDoesNotDuplicateCurrentUserInput(t *testing.T) {
	var got []agentcore.Message
	_, err := Reply(context.Background(), func(_ context.Context, messages []agentcore.Message) (string, error) {
		got = messages
		return "reply", nil
	}, store.GalgameCharacter{Name: "林晚", Description: "情报员"}, store.GalgameSession{}, "本轮输入", ReplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, message := range got {
		if message.TextContent() == "本轮输入" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("current input count = %d, messages = %#v", count, got)
	}
}

func TestComposeMessagesKeepsNewestHistoryWithinBudget(t *testing.T) {
	history := make([]store.GalgameMessage, 0, 30)
	for index := 0; index < 30; index++ {
		history = append(history, store.GalgameMessage{Role: "user", Content: strings.Repeat("旧", 300) + fmt.Sprintf("<history-%02d>", index)})
	}
	messages := composeMessages(
		store.GalgameCharacter{Name: "林晚", Description: "情报员"},
		store.GalgameSession{Messages: history}, "现在", ReplyOptions{ContextWindow: 8192},
	)
	joined := ""
	for _, message := range messages {
		joined += message.TextContent()
	}
	if !strings.Contains(joined, "<history-29>") {
		t.Fatal("newest history was removed")
	}
	if strings.Contains(joined, "<history-00>") {
		t.Fatal("oldest history should have been trimmed")
	}
}

func TestReplyRejectsEmptyInput(t *testing.T) {
	_, err := Reply(context.Background(), func(context.Context, []agentcore.Message) (string, error) {
		return "x", nil
	}, store.GalgameCharacter{Description: "角色"}, store.GalgameSession{}, "  ", ReplyOptions{})
	if err == nil {
		t.Fatal("expected empty input error")
	}
}
