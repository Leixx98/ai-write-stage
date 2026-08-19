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
	text, err := Reply(context.Background(), generate, character, &session, "看那边", ReplyOptions{ContextWindow: 32768})
	if err != nil {
		t.Fatal(err)
	}
	if text != "在。" {
		t.Fatalf("reply = %q", text)
	}
	if len(got) < 2 || got[0].Role != agentcore.RoleSystem {
		t.Fatalf("messages = %#v", got)
	}
	system := got[0].TextContent()
	for _, expected := range []string{"扮演林晚", "Write 林晚's next reply", "情报员", "冷静", "雨夜码头", "旅人", "旅人: 你好", "只回复林晚"} {
		if !strings.Contains(system, expected) {
			t.Fatalf("system prompt missing %q:\n%s", expected, system)
		}
	}
	for index, message := range got[1:] {
		if message.Role == agentcore.RoleSystem {
			t.Fatalf("extra system at %d: %#v", index+1, message)
		}
	}
	if got[len(got)-1].Role != agentcore.RoleUser || got[len(got)-1].TextContent() != "看那边" {
		t.Fatalf("last message = %#v", got[len(got)-1])
	}
}

func TestReplyDoesNotDuplicateCurrentUserInput(t *testing.T) {
	var got []agentcore.Message
	session := store.GalgameSession{}
	_, err := Reply(context.Background(), func(_ context.Context, messages []agentcore.Message) (string, error) {
		got = messages
		return "reply", nil
	}, store.GalgameCharacter{Name: "林晚", Description: "情报员"}, &session, "本轮输入", ReplyOptions{})
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

func TestReplyStreamEmitsTextDeltas(t *testing.T) {
	var kinds []string
	session := store.GalgameSession{}
	text, err := ReplyStream(context.Background(), func(_ context.Context, _ []agentcore.Message, emit func(Delta)) (string, error) {
		emit(Delta{Kind: "thinking", Text: "想一下"})
		emit(Delta{Kind: "text", Text: "你好"})
		return "你好", nil
	}, store.GalgameCharacter{Name: "林晚", Description: "情报员"}, &session, "嗨", ReplyOptions{}, func(delta Delta) {
		kinds = append(kinds, delta.Kind+":"+delta.Text)
	})
	if err != nil || text != "你好" {
		t.Fatalf("reply = %q %v", text, err)
	}
	if strings.Join(kinds, ",") != "thinking:想一下,text:你好" {
		t.Fatalf("deltas = %#v", kinds)
	}
}

func TestComposeMessagesKeepsNewestHistoryWithinBudget(t *testing.T) {
	history := make([]store.GalgameMessage, 0, 30)
	for index := 0; index < 30; index++ {
		history = append(history, store.GalgameMessage{Role: "user", Content: strings.Repeat("旧", 300) + fmt.Sprintf("<history-%02d>", index)})
	}
	messages, cutoff := composeMessages(
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
	if cutoff <= 0 {
		t.Fatalf("cutoff = %d, want a committed trim", cutoff)
	}
}

func TestComposeMessagesFreezesSystemAcrossUserLength(t *testing.T) {
	character := store.GalgameCharacter{
		Name: "林晚", Description: "情报员", Personality: "冷静",
		Scenario: "雨夜码头", ExampleDialogue: "例子", PostHistoryInstructions: "只回台词",
	}
	session := store.GalgameSession{UserPersona: "旅人"}
	short, _ := composeMessages(character, session, "短", ReplyOptions{ContextWindow: 8192})
	long, _ := composeMessages(character, session, strings.Repeat("长输入", 200), ReplyOptions{ContextWindow: 8192})
	if short[0].TextContent() != long[0].TextContent() {
		t.Fatalf("system changed with user length\n%s\n%s", short[0].TextContent(), long[0].TextContent())
	}
}

func TestComposeMessagesCommitsHistoryCutoff(t *testing.T) {
	history := make([]store.GalgameMessage, 0, 20)
	for index := 0; index < 20; index++ {
		history = append(history, store.GalgameMessage{Role: "user", Content: strings.Repeat("旧", 200) + fmt.Sprintf("<history-%02d>", index)})
	}
	session := store.GalgameSession{Messages: history}
	first, cutoff := composeMessages(store.GalgameCharacter{Name: "林晚", Description: "情报员"}, session, "现在", ReplyOptions{ContextWindow: 8192})
	if cutoff <= 0 || len(first) < 3 {
		t.Fatalf("first cutoff=%d messages=%d", cutoff, len(first))
	}
	head := first[1].TextContent()
	session.HistoryCutoff = cutoff
	session.Messages = append(session.Messages, store.GalgameMessage{Role: "assistant", Content: "短回复"})
	second, next := composeMessages(store.GalgameCharacter{Name: "林晚", Description: "情报员"}, session, "继续", ReplyOptions{ContextWindow: 8192})
	if next != cutoff {
		t.Fatalf("cutoff moved from %d to %d", cutoff, next)
	}
	if second[1].TextContent() != head {
		t.Fatalf("history prefix changed\n%s\n%s", head, second[1].TextContent())
	}
}

func TestReplyRejectsEmptyInput(t *testing.T) {
	session := store.GalgameSession{}
	_, err := Reply(context.Background(), func(context.Context, []agentcore.Message) (string, error) {
		return "x", nil
	}, store.GalgameCharacter{Description: "角色"}, &session, "  ", ReplyOptions{})
	if err == nil {
		t.Fatal("expected empty input error")
	}
}

func TestChatCacheKeyAndPin(t *testing.T) {
	if ChatCacheKey("sess_1") != "gal-sess_1" {
		t.Fatalf("key = %q", ChatCacheKey("sess_1"))
	}
	if ChatCacheKey("  ") != "" {
		t.Fatal("empty session should omit key")
	}
	messages := PinPromptCache([]agentcore.Message{agentcore.SystemMsg("sys"), agentcore.UserMsg("hi")})
	if messages[0].Metadata["cache_control"] != "ephemeral" || messages[1].Metadata["cache_control"] != "ephemeral" {
		t.Fatalf("cache metadata = %#v %#v", messages[0].Metadata, messages[1].Metadata)
	}
}
