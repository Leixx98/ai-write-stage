package play

import (
	"strings"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

func TestCompactWriterTurnsDropsOldestPairs(t *testing.T) {
	turns := []store.PlayWriterTurn{
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "一"}, Speaker: "林晚", Text: strings.Repeat("旧一屏", 400)},
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "二"}, Speaker: "林晚", Text: strings.Repeat("旧二屏", 400)},
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "三"}, Speaker: "林晚", Text: strings.Repeat("近一屏", 400)},
	}
	got := compactWriterTurns(turns, "角色卡", `{"card":{"kind":"dialogue"}}`, 20000)
	if len(got) == 0 || len(got) >= len(turns) {
		t.Fatalf("compacted = %d, original = %d", len(got), len(turns))
	}
	if got[0].Card.Location == "一" {
		t.Fatalf("oldest turn should be committed away: %+v", got)
	}
}

func TestCompactWriterTurnsKeepsWhenUnderBudget(t *testing.T) {
	turns := []store.PlayWriterTurn{
		{Card: store.PlayBeatCard{Kind: store.BeatDialogue, Location: "码头"}, Speaker: "林晚", Text: "在。"},
	}
	got := compactWriterTurns(turns, "角色卡", `{"card":{}}`, 0)
	if len(got) != 1 || got[0].Text != "在。" {
		t.Fatalf("under budget should keep turns: %+v", got)
	}
}
