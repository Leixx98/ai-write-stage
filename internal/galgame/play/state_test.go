package play

import (
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/store"
)

func TestNormalizeAndApplyFacts(t *testing.T) {
	ledger := applyFacts(store.PlayLedger{}, []string{"Fought", "fought", "sanity-low", ""})
	if len(ledger.Facts) != 2 || ledger.Facts[0].ID != "fought" || ledger.Facts[1].ID != "sanity_low" {
		t.Fatalf("facts = %+v", ledger.Facts)
	}
	ledger = applyFacts(ledger, []string{"fought", "evacuated"})
	if len(ledger.Facts) != 3 || ledger.Facts[2].ID != "evacuated" {
		t.Fatalf("append = %+v", ledger.Facts)
	}
}

func TestSimilarChoiceDetectsRepeatLabel(t *testing.T) {
	history := []store.PlayChoiceRecord{{ChoiceID: "fight", Label: "让大凤META战斗"}}
	cards := []store.PlayBeatCard{{
		Kind:    store.BeatChoice,
		Choices: []store.PlayChoice{{ID: "again", Label: "让大凤META战斗", SetFacts: []string{"fought"}}},
	}}
	if err := similarChoice(history, cards); err == nil {
		t.Fatal("expected repeat label")
	}
	cards[0].Choices[0].Label = "带她撤离战场"
	if err := similarChoice(history, cards); err != nil {
		t.Fatalf("new label should pass: %v", err)
	}
}

func TestRepairPlannerCardsFixesCGChoice(t *testing.T) {
	cards := repairPlannerCards([]store.PlayBeatCard{{
		Kind: store.BeatDialogue, CG: "choice",
		Choices: []store.PlayChoice{{ID: "a", Label: "A", SetFacts: []string{"Fought"}}},
	}}, true)
	if cards[0].Kind != store.BeatChoice || cards[0].CG != store.PlayCGKeep {
		t.Fatalf("repaired = %+v", cards[0])
	}
	if cards[0].Choices[0].SetFacts[0] != "fought" {
		t.Fatalf("facts = %+v", cards[0].Choices[0].SetFacts)
	}
}

func TestCapWriterTurns(t *testing.T) {
	turns := []store.PlayWriterTurn{{Text: "1"}, {Text: "2"}, {Text: "3"}, {Text: "4"}}
	got := capWriterTurns(turns, 2)
	if len(got) != 2 || got[0].Text != "3" || got[1].Text != "4" {
		t.Fatalf("capped = %+v", got)
	}
}
