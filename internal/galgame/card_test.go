package galgame

import (
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

func TestImportCharacterJSONV2UsesAllSupportedFields(t *testing.T) {
	raw := []byte(`{"spec":"chara_card_v2","data":{"name":" 林晚 ","description":"情报员","personality":"冷静","scenario":"雨夜码头","first_mes":"{{char}}看向{{user}}。","mes_example":"<START>\n{{user}}: 你好\n{{char}}: 嗯。","system_prompt":"扮演{{char}}。{{original}}","post_history_instructions":"只回复角色台词","alternate_greetings":[" 早。 ",""],"extensions":{"depth_prompt":{"prompt":"ignored for now"}}}}`)
	character, err := ImportCharacterJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if character.Name != "林晚" || character.Description != "情报员" || character.Personality != "冷静" || character.Scenario != "雨夜码头" {
		t.Fatalf("character = %#v", character)
	}
	if character.FirstMessage == "" || character.ExampleDialogue == "" || character.SystemPrompt == "" || character.PostHistoryInstructions == "" {
		t.Fatalf("supported fields were lost: %#v", character)
	}
	if len(character.AlternateGreetings) != 1 || character.AlternateGreetings[0] != "早。" {
		t.Fatalf("alternate greetings = %#v", character.AlternateGreetings)
	}
	if character.Extensions["depth_prompt"] == nil {
		t.Fatalf("extensions = %#v", character.Extensions)
	}
}

func TestImportCharacterJSONFlatNativeShape(t *testing.T) {
	character, err := ImportCharacterJSON([]byte(`{"name":"林晚","description":"核心设定","alternate_greetings":"你好"}`))
	if err != nil {
		t.Fatal(err)
	}
	if character.Description != "核心设定" || len(character.AlternateGreetings) != 1 {
		t.Fatalf("character = %#v", character)
	}
}

func TestInitializeSessionUsesFirstMessageOnce(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	character := store.GalgameCharacter{Name: "林晚", FirstMessage: "{{char}}向{{user}}点头。"}
	session := InitializeSession(character, store.GalgameSession{UserPersona: "旅人"}, now)
	if len(session.Messages) != 1 || session.Messages[0].Content != "林晚向旅人点头。" || session.Messages[0].CreatedAt != now {
		t.Fatalf("session = %#v", session)
	}
	if again := InitializeSession(character, session, now.Add(time.Hour)); len(again.Messages) != 1 {
		t.Fatalf("greeting was duplicated: %#v", again.Messages)
	}
}

func TestInitializeSessionCanSelectAlternateGreeting(t *testing.T) {
	character := store.GalgameCharacter{Name: "林晚", FirstMessage: "默认", AlternateGreetings: []string{"备用一", "备用二"}}
	session := InitializeSession(character, store.GalgameSession{}, time.Now(), 2)
	if len(session.Messages) != 1 || session.Messages[0].Content != "备用二" {
		t.Fatalf("session = %#v", session)
	}
}
