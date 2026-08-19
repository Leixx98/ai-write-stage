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
	if _, err := os.Stat(filepath.Join(dir, "galgame", "plays", "run_play")); !os.IsNotExist(err) {
		t.Fatalf("play dir still exists: %v", err)
	}
	_, ok, err = tavern.ActivePlay()
	if err != nil || ok {
		t.Fatalf("no active play expected, ok=%v err=%v", ok, err)
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
