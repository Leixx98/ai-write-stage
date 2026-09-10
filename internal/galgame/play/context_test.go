package play

import (
	"strings"
	"testing"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

func testCharacter() store.GalgameCharacter {
	return store.GalgameCharacter{
		ID:          "char_1",
		Name:        "林晚",
		Description: "情报员",
		Personality: "冷静",
		Scenario:    "雨夜码头",
		Extensions:  map[string]any{"world": "secret"},
		CreatedAt:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
	}
}

func TestWriterRequestKeepsStableSystemAndOmitsVolatileFields(t *testing.T) {
	character := testCharacter()
	first := WriterInput{
		Card:        store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "码头"},
		Character:   character,
		Premise:     "雨夜",
		UserPersona: "旅人",
	}
	second := first
	second.Card = store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "仓库"}
	system1, user1, err := writerRequest("写台词", first)
	if err != nil {
		t.Fatal(err)
	}
	system2, user2, err := writerRequest("写台词", second)
	if err != nil {
		t.Fatal(err)
	}
	if system1 != system2 {
		t.Fatalf("system changed\n%s\n%s", system1, system2)
	}
	if !strings.Contains(system1, "情报员") || !strings.Contains(system1, "雨夜") || !strings.Contains(system1, "旅人") {
		t.Fatalf("system missing static fields: %s", system1)
	}
	for _, leaked := range []string{"extensions", "char_1", "job_1", "job_2", "created_at"} {
		if strings.Contains(system1, leaked) || strings.Contains(user1, leaked) || strings.Contains(user2, leaked) {
			t.Fatalf("volatile field %q leaked\nsystem=%s\nuser1=%s\nuser2=%s", leaked, system1, user1, user2)
		}
	}
	if strings.Contains(user1, "recent_beats") || strings.Contains(user2, "recent_beats") {
		t.Fatalf("writer user still carries recent_beats: user1=%s user2=%s", user1, user2)
	}
	if !strings.Contains(user2, "仓库") || strings.Contains(system2, "仓库") {
		t.Fatalf("card leaked into system or missing from user: system=%s user=%s", system2, user2)
	}
	if strings.Index(system1, staticContextHeading) != 0 || strings.Index(system1, "写台词") < strings.Index(system1, `"character"`) {
		t.Fatalf("character card must lead system: %s", system1)
	}
}

func TestPlayRequestsShareCharacterCardPrefix(t *testing.T) {
	character := testCharacter()
	writerSys, _, err := writerRequest("写台词", WriterInput{Character: character, Premise: "雨夜", UserPersona: "旅人", Card: store.PlayBeatCard{Kind: store.BeatDialogue}})
	if err != nil {
		t.Fatal(err)
	}
	archSys, _, err := architectRequest("规划", ArchitectInput{Character: character, Premise: "雨夜", UserPersona: "旅人"})
	if err != nil {
		t.Fatal(err)
	}
	planSys, _, err := plannerRequest("分镜", PlannerInput{Character: character, Premise: "雨夜", UserPersona: "旅人"})
	if err != nil {
		t.Fatal(err)
	}
	writerPrefix := systemCardPrefix(writerSys)
	if writerPrefix == "" || writerPrefix != systemCardPrefix(archSys) || writerPrefix != systemCardPrefix(planSys) {
		t.Fatalf("shared prefix diverged\nwriter=%s\narch=%s\nplan=%s", writerSys, archSys, planSys)
	}
	if !strings.Contains(writerPrefix, "情报员") || !strings.Contains(writerPrefix, "旅人") {
		t.Fatalf("prefix missing card fields: %s", writerPrefix)
	}
}

func systemCardPrefix(system string) string {
	idx := strings.Index(system, "\n\n")
	if idx < 0 {
		return system
	}
	return system[:idx]
}

func TestArchitectRequestPutsStationBeforeFacts(t *testing.T) {
	system, user, err := architectRequest("规划", ArchitectInput{
		Character:      testCharacter(),
		Premise:        "雨夜",
		UserPersona:    "旅人",
		CurrentStation: store.PlayStation{ID: "meet", Pressure: "第一次必须表态"},
		Facts:          []store.PlayFact{{ID: "arrived"}},
		ChoiceHistory:  []store.PlayChoiceRecord{{Ordinal: 3, ChoiceID: "stay", Label: "留下"}},
		RecentBeats:    []store.PlayBeat{{Ordinal: 3, Text: "对峙", ImageJobID: "img"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "情报员") || strings.Contains(system, "第一次必须表态") {
		t.Fatalf("static/dynamic mix: %s", system)
	}
	stationAt := strings.Index(user, "current_station")
	factsAt := strings.Index(user, "facts")
	choiceAt := strings.Index(user, "choice_history")
	beatsAt := strings.Index(user, "recent_beats")
	if stationAt < 0 || factsAt < 0 || choiceAt < 0 || beatsAt < 0 || !(stationAt < factsAt && factsAt < choiceAt && choiceAt < beatsAt) {
		t.Fatalf("turn field order: %s", user)
	}
	if strings.Contains(user, "img") || strings.Contains(user, "extensions") {
		t.Fatalf("volatile field leaked: %s", user)
	}
	if !strings.Contains(system, "精简") || !strings.Contains(system, "选择多") {
		t.Fatalf("system missing play hints: %s", system)
	}
	if !strings.Contains(user, `"pacing":"choice"`) {
		t.Fatalf("user missing default pacing: %s", user)
	}
}

func TestPlannerRequestKeepsCharacterInSystem(t *testing.T) {
	system, user, err := plannerRequest("分镜", PlannerInput{
		Architect:   ArchitectOutput{SegmentID: "meet", Goal: "重逢", Notes: "对质"},
		Character:   testCharacter(),
		Premise:     "雨夜",
		UserPersona: "旅人",
		Location:    "码头",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(system, "情报员") || strings.Contains(system, "重逢") {
		t.Fatalf("static/dynamic mix: %s", system)
	}
	if strings.Index(user, "location") > strings.Index(user, "architect") {
		t.Fatalf("architect must be last: %s", user)
	}
	if strings.Contains(user, "extensions") {
		t.Fatalf("character leaked into user: %s", user)
	}
}

func TestPlayCacheKey(t *testing.T) {
	if got := playCacheKey("rain", "play_writer"); got != "play-rain-writer" {
		t.Fatalf("key = %q", got)
	}
	if playCacheKey("  ", "play_writer") != "" {
		t.Fatal("empty play id should omit key")
	}
}
