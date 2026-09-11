package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPlayCRUDAndBeatAppend(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	created := time.Date(2026, 8, 18, 2, 0, 0, 0, time.UTC)
	id := tavern.NewPlayID("林晚", "雨夜", created)
	if id != "林晚_雨夜_20260818_020000" {
		t.Fatalf("play id = %q", id)
	}
	meta := PlayMeta{ID: id, Name: "雨夜", CharacterID: "char_1", Premise: "在雨夜遇见她", Status: PlayIdle, CreatedAt: created}
	if err := tavern.SavePlay(meta); err != nil {
		t.Fatal(err)
	}
	loaded, err := tavern.LoadPlay(id)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Premise != meta.Premise || loaded.Status != PlayIdle {
		t.Fatalf("loaded meta = %+v", loaded)
	}
	if err := tavern.SaveProgress(id, PlayProgress{}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveBeat(id, PlayBeat{Ordinal: 1, Kind: BeatDialogue, Speaker: "林晚", Text: "你来了。", CG: PlayCGNew}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(id, PlayProgress{PlayHead: 1, WriteHead: 1}); err != nil {
		t.Fatal(err)
	}
	beats, err := tavern.ListBeats(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(beats) != 1 || beats[0].Speaker != "林晚" {
		t.Fatalf("beats = %+v", beats)
	}
	if err := tavern.SaveProgress(id, PlayProgress{PlayHead: 2, WriteHead: 1}); err == nil {
		t.Fatal("play_head > write_head should fail")
	}
}

func TestPlayRejectsInvalidIDAndEmptyPremise(t *testing.T) {
	tavern := Open(t.TempDir(), t.TempDir()).Tavern
	err := tavern.SavePlay(PlayMeta{ID: "../escape", Name: "x", CharacterID: "c", Premise: "p"})
	if err == nil {
		t.Fatal("unsafe id should fail")
	}
	err = tavern.SavePlay(PlayMeta{ID: "ok_id", Name: "x", CharacterID: "c"})
	if err == nil {
		t.Fatal("empty premise should fail")
	}
}

func TestActivePlayAndDelete(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	idle := PlayMeta{ID: "idle_play", Name: "a", CharacterID: "c", Premise: "p", Status: PlayIdle}
	active := PlayMeta{ID: "run_play", Name: "b", CharacterID: "c", Premise: "p", Status: PlayRunning}
	if err := tavern.SavePlay(idle); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SavePlay(active); err != nil {
		t.Fatal(err)
	}
	got, ok, err := tavern.ActivePlay()
	if err != nil || !ok || got.ID != "run_play" {
		t.Fatalf("active = %+v ok=%v err=%v", got, ok, err)
	}
	if err := tavern.SaveBeat("run_play", PlayBeat{Ordinal: 1, Text: "hi", CG: PlayCGKeep}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.DeletePlay("run_play"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, TavernDirName, "plays", "run_play")); !os.IsNotExist(err) {
		t.Fatalf("play dir still exists: %v", err)
	}
	_, ok, err = tavern.ActivePlay()
	if err != nil || ok {
		t.Fatalf("no active play expected, ok=%v err=%v", ok, err)
	}
}

func TestPlayDensitySpineAndLedger(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	id := "rain_night"
	if err := tavern.SavePlay(PlayMeta{ID: id, Name: "雨夜", CharacterID: "c", Premise: "p", Density: "丰满"}); err != nil {
		t.Fatal(err)
	}
	meta, err := tavern.LoadPlay(id)
	if err != nil || meta.Density != PlayDensityRich || meta.Pacing != PlayPacingChoice {
		t.Fatalf("density/pacing = %+v %v", meta, err)
	}
	if err := tavern.SavePlay(PlayMeta{ID: id, Name: "雨夜", CharacterID: "c", Premise: "p", Density: PlayDensityRich, Pacing: "剧情多"}); err != nil {
		t.Fatal(err)
	}
	meta, err = tavern.LoadPlay(id)
	if err != nil || meta.Pacing != PlayPacingStory {
		t.Fatalf("pacing = %+v %v", meta, err)
	}
	if err := tavern.SavePlay(PlayMeta{ID: id, Name: "雨夜", CharacterID: "c", Premise: "p", Pacing: "纯剧情"}); err != nil {
		t.Fatal(err)
	}
	meta, err = tavern.LoadPlay(id)
	if err != nil || meta.Pacing != PlayPacingPure {
		t.Fatalf("pure pacing = %+v %v", meta, err)
	}
	if err := tavern.SaveSpine(id, PlaySpine{
		Throughline: "兑现重逢",
		Stations:    []PlayStation{{ID: "meet", Title: "重逢", Pressure: "表态", Summary: "车站对质", MustHappen: []string{"表态"}, Forks: []PlayFork{{Tint: "默认"}}, Status: StationPending}},
		Threads:     []PlayThread{{ID: "secret", Hint: "跟踪", Status: ThreadOpen}},
	}); err != nil {
		t.Fatal(err)
	}
	spine, err := tavern.LoadSpine(id)
	if err != nil || spine.Throughline != "兑现重逢" || len(spine.Stations) != 1 || spine.Stations[0].Summary != "车站对质" || len(spine.Threads) != 1 {
		t.Fatalf("spine = %+v %v", spine, err)
	}
	if err := tavern.SaveLedger(id, PlayLedger{Facts: []PlayFact{{ID: "stayed"}}}); err != nil {
		t.Fatal(err)
	}
	ledger, err := tavern.LoadLedger(id)
	if err != nil || len(ledger.Facts) != 1 || ledger.Facts[0].ID != "stayed" {
		t.Fatalf("ledger = %+v %v", ledger, err)
	}
}

func TestTruncateBeatsAfterKeepsPlayed(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	id := "rain_night"
	if err := tavern.SavePlay(PlayMeta{ID: id, Name: "雨夜", CharacterID: "c", Premise: "p"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		if err := tavern.SaveBeat(id, PlayBeat{Ordinal: i, Text: "拍", CG: PlayCGKeep}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tavern.TruncateBeatsAfter(id, 2); err != nil {
		t.Fatal(err)
	}
	beats, err := tavern.ListBeats(id)
	if err != nil || len(beats) != 2 || beats[0].Ordinal != 1 || beats[1].Ordinal != 2 {
		t.Fatalf("beats = %+v %v", beats, err)
	}
}

func TestWriterSessionRoundTrip(t *testing.T) {
	dir := t.TempDir()
	tavern := Open(dir, dir).Tavern
	id := "rain_night"
	if err := tavern.SavePlay(PlayMeta{ID: id, Name: "雨夜", CharacterID: "c", Premise: "p"}); err != nil {
		t.Fatal(err)
	}
	empty, err := tavern.LoadWriterSession(id)
	if err != nil || len(empty.Turns) != 0 {
		t.Fatalf("missing session = %+v %v", empty, err)
	}
	session := PlayWriterSession{Turns: []PlayWriterTurn{{
		Card:    PlayBeatCard{Kind: BeatDialogue, Speaker: "林晚", Location: "码头"},
		Speaker: "林晚", Text: "在。",
	}}}
	if err := tavern.SaveWriterSession(id, session); err != nil {
		t.Fatal(err)
	}
	got, err := tavern.LoadWriterSession(id)
	if err != nil || len(got.Turns) != 1 || got.Turns[0].Text != "在。" {
		t.Fatalf("loaded = %+v %v", got, err)
	}
}

func TestDisplayImageJobIDWalksKeepBeats(t *testing.T) {
	beats := []PlayBeat{
		{Ordinal: 1, CG: PlayCGNew, ImageJobID: "job_a"},
		{Ordinal: 2, CG: PlayCGKeep},
		{Ordinal: 3, CG: PlayCGNew, ImageJobID: "job_b"},
		{Ordinal: 4, CG: PlayCGKeep},
	}
	if got := DisplayImageJobID(beats, 2); got != "job_a" {
		t.Fatalf("ordinal 2 = %q", got)
	}
	if got := DisplayImageJobID(beats, 4); got != "job_b" {
		t.Fatalf("ordinal 4 = %q", got)
	}
	if got := DisplayImageJobID(beats, 1); got != "job_a" {
		t.Fatalf("ordinal 1 = %q", got)
	}
	bound := DisplayBoundImage(beats, 2)
	if bound.JobID != "job_a" || bound.Ordinal != 1 {
		t.Fatalf("bound keep = %+v", bound)
	}
}

func TestDisplayBoundImageDoesNotFallBackPastUnstartedNew(t *testing.T) {
	beats := []PlayBeat{
		{Ordinal: 1, CG: PlayCGNew, ImageJobID: "job_a"},
		{Ordinal: 2, CG: PlayCGKeep},
		{Ordinal: 3, CG: PlayCGNew},
	}
	bound := DisplayBoundImage(beats, 3)
	if bound.JobID != "" || bound.Ordinal != 3 {
		t.Fatalf("unstarted new = %+v", bound)
	}
	if got := DisplayImageJobID(beats, 2); got != "job_a" {
		t.Fatalf("previous keep = %q", got)
	}
}
