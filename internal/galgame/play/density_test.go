package play

import (
	"strings"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

func TestProfileComposesDensityAndPacing(t *testing.T) {
	choice := profileFor(store.PlayDensityCompact, store.PlayPacingChoice)
	if choice.MinCards != 3 || choice.MaxCards != 6 || !choice.FillEmptyCG {
		t.Fatalf("choice compact = %+v", choice)
	}
	if choice.WriterTurns != 6 || choice.RecentBeats != 4 {
		t.Fatalf("choice compact memory = %+v", choice)
	}
	if !strings.Contains(choice.PromptHint, "精简") || !strings.Contains(choice.PromptHint, "选择多") {
		t.Fatalf("choice hint = %s", choice.PromptHint)
	}
	story := profileFor(store.PlayDensityCompact, store.PlayPacingStory)
	if story.MinCards != 10 || story.MaxCards != 15 || !story.FillEmptyCG {
		t.Fatalf("story compact = %+v", story)
	}
	if story.WriterTurns != 15 || story.RecentBeats != 7 {
		t.Fatalf("story compact should raise memory, got %+v", story)
	}
	rich := profileFor(store.PlayDensityRich, store.PlayPacingChoice)
	if rich.FillEmptyCG || rich.WriterTurns != 20 || rich.RecentBeats != 8 || rich.MaxCards != 6 {
		t.Fatalf("rich choice = %+v", rich)
	}
	pure := profileFor(store.PlayDensityCompact, store.PlayPacingPure)
	if pure.MinCards != 5 || pure.MaxCards != 10 || pure.WriterTurns != 10 || !strings.Contains(pure.PromptHint, "纯剧情") {
		t.Fatalf("pure compact = %+v", pure)
	}
}
