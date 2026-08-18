package host

import (
	"context"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/galgame/play"
	"github.com/voocel/ainovel-cli/internal/store"
)

func newPlayHost(t *testing.T) *Host {
	t.Helper()
	dir := t.TempDir()
	roots := store.Open(dir, dir)
	if err := roots.Facts.Init(); err != nil {
		t.Fatal(err)
	}
	h := newFlagTestHost(lifecycleIdle, false)
	h.roots = roots
	h.store = roots.Facts
	h.runCtx = context.Background()
	h.runCancel = func() {}
	h.closed = make(chan struct{})
	return h
}

func seedPlay(t *testing.T, h *Host, status store.PlayStatus) string {
	t.Helper()
	if err := h.roots.Tavern.SaveCharacter(store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "主角"}); err != nil {
		t.Fatal(err)
	}
	id := "rain_night"
	if err := h.roots.Tavern.SavePlay(store.PlayMeta{ID: id, Name: "雨夜", CharacterID: "linwan", Premise: "重逢", Status: status}); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.SaveProgress(id, store.PlayProgress{}); err != nil {
		t.Fatal(err)
	}
	return id
}

func attachFakePlay(h *Host) {
	h.playArchitect = func(context.Context, play.ArchitectInput) (play.ArchitectOutput, error) {
		return play.ArchitectOutput{SegmentID: "meet", Goal: "见面"}, nil
	}
	h.playPlanner = func(_ context.Context, in play.PlannerInput) (play.PlannerOutput, error) {
		return play.PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
			{Kind: store.BeatDialogue, Speaker: "林晚", Location: "站台", CG: store.PlayCGKeep, RequiredBeats: []string{"你好"}},
			{Kind: store.BeatChoice, Speaker: "林晚", Location: "站台", CG: store.PlayCGKeep, RequiredBeats: []string{"选择"}, Choices: []store.PlayChoice{
				{ID: "a", Label: "A", Consequence: "a"},
				{ID: "b", Label: "B", Consequence: "b"},
			}},
		}}, nil
	}
	h.playWriter = func(_ context.Context, in play.WriterInput) (play.WriterOutput, error) {
		text := "……"
		if len(in.Card.RequiredBeats) > 0 {
			text = in.Card.RequiredBeats[0]
		}
		return play.WriterOutput{Speaker: in.Card.Speaker, Text: text}, nil
	}
}

func TestPlayActiveErrorBlocksContinue(t *testing.T) {
	h := newPlayHost(t)
	seedPlay(t, h, store.PlayRunning)
	if err := h.Continue("继续写"); err == nil || !strings.Contains(err.Error(), "剧场进行中") {
		t.Fatalf("got %v", err)
	}
}

func TestStartPlayBlocksNovelEntries(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	attachFakePlay(h)
	if err := h.StartPlay(id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.PausePlay() })
	if err := h.Continue("继续写"); err == nil || !strings.Contains(err.Error(), "剧场") {
		t.Fatalf("continue: %v", err)
	}
	if _, err := h.Resume(); err == nil || !strings.Contains(err.Error(), "剧场") {
		t.Fatalf("resume: %v", err)
	}
	if err := h.PausePlay(); err != nil {
		t.Fatal(err)
	}
	if err := h.acquireExclusive("导入"); err != nil {
		t.Fatalf("after pause exclusive should be free: %v", err)
	}
	h.releaseExclusive()
}

func TestEngineRunningBlocksStartPlay(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	h.lifecycle = lifecycleRunning
	if err := h.StartPlay(id); err == nil || !strings.Contains(err.Error(), "创作引擎") {
		t.Fatalf("got %v", err)
	}
}

func TestStartEngineRefusesActivePlayOnDisk(t *testing.T) {
	h := newPlayHost(t)
	seedPlay(t, h, store.PlayAwaitingChoice)
	if h.startEngine(nil) {
		t.Fatal("startEngine should refuse active play")
	}
}

func TestPausedPlayDoesNotBlockNovel(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayAwaitingChoice)
	if err := h.playActiveError(); err == nil {
		t.Fatal("awaiting_choice should block novel")
	}
	meta, err := h.roots.Tavern.LoadPlay(id)
	if err != nil {
		t.Fatal(err)
	}
	meta.Status = store.PlayPaused
	if err := h.roots.Tavern.SavePlay(meta); err != nil {
		t.Fatal(err)
	}
	if err := h.playActiveError(); err != nil {
		t.Fatalf("paused play should not block novel: %v", err)
	}
}
