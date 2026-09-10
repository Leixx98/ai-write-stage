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

func TestValidateCardCountFollowsPacing(t *testing.T) {
	choice := profileFor(store.PlayDensityCompact, store.PlayPacingChoice)
	if err := validateCardCount(2, choice); err == nil {
		t.Fatal("choice should reject 2 cards")
	}
	if err := validateCardCount(4, choice); err != nil {
		t.Fatal(err)
	}
	if err := validateCardCount(6, choice); err != nil {
		t.Fatal(err)
	}
	story := profileFor(store.PlayDensityRich, store.PlayPacingStory)
	if err := validateCardCount(6, story); err == nil {
		t.Fatal("story should reject 6 cards")
	}
	if err := validateCardCount(12, story); err != nil {
		t.Fatal(err)
	}
	pure := profileFor(store.PlayDensityCompact, store.PlayPacingPure)
	if err := validateCardCount(4, pure); err == nil {
		t.Fatal("pure should reject 4 cards")
	}
	if err := validateCardCount(7, pure); err != nil {
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
	if err := validatePlannerAgainstArchitect(plan, arch, true, store.PlayPacingChoice); err != nil {
		t.Fatal(err)
	}
	if err := validatePlannerAgainstArchitect(plan, arch, false, store.PlayPacingChoice); err == nil {
		t.Fatal("open station needs a choice")
	}
	if err := validatePlannerAgainstArchitect(plan, arch, false, store.PlayPacingPure); err != nil {
		t.Fatal(err)
	}
	plan.Cards[2] = store.PlayBeatCard{Kind: store.BeatChoice, CG: store.PlayCGKeep, Choices: []store.PlayChoice{
		{ID: "a", Label: "A", SetFacts: []string{"left"}},
		{ID: "b", Label: "B", SetFacts: []string{"stayed"}},
	}}
	if err := validatePlannerAgainstArchitect(plan, arch, false, store.PlayPacingPure); err == nil {
		t.Fatal("pure pacing cannot include choice")
	}
}
