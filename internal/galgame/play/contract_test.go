package play

import (
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

func TestPlannerValidateRequiresSetFacts(t *testing.T) {
	out := PlannerOutput{SegmentID: "meet", Cards: []store.PlayBeatCard{
		{Kind: store.BeatDialogue, CG: store.PlayCGKeep},
		{Kind: store.BeatDialogue, CG: store.PlayCGKeep},
		{Kind: store.BeatChoice, CG: store.PlayCGKeep, Choices: []store.PlayChoice{
			{ID: "a", Label: "A"},
			{ID: "b", Label: "B", SetFacts: []string{"left"}},
		}},
	}}
	if err := out.Validate(); err == nil {
		t.Fatal("expected set_facts error")
	}
	out.Cards[2].Choices[0].SetFacts = []string{"stayed"}
	if err := out.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCardCountFollowsDensity(t *testing.T) {
	compact := profileFor(store.PlayDensityCompact)
	if err := validateCardCount(2, compact); err == nil {
		t.Fatal("compact should reject 2 cards")
	}
	if err := validateCardCount(4, compact); err != nil {
		t.Fatal(err)
	}
	rich := profileFor(store.PlayDensityRich)
	if err := validateCardCount(4, rich); err == nil {
		t.Fatal("rich should reject 4 cards")
	}
	if err := validateCardCount(7, rich); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlannerAgainstArchitectLastStation(t *testing.T) {
	arch := ArchitectOutput{SegmentID: "end", Goal: "收束"}
	plan := PlannerOutput{SegmentID: "end", Cards: []store.PlayBeatCard{
		{Kind: store.BeatNarration, CG: store.PlayCGKeep},
		{Kind: store.BeatDialogue, CG: store.PlayCGKeep},
		{Kind: store.BeatDialogue, CG: store.PlayCGKeep},
	}}
	if err := validatePlannerAgainstArchitect(plan, arch, true); err != nil {
		t.Fatal(err)
	}
	if err := validatePlannerAgainstArchitect(plan, arch, false); err == nil {
		t.Fatal("open station needs a choice")
	}
}
