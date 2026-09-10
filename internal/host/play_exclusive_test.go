package host

import (
	"context"
	"strings"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/galgame/play"
	"github.com/Leixx98/ai-write-stage/internal/store"
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

func TestUpdatePlayPersistsImageProfileWithoutResettingRuntimeState(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayPaused)
	updated, err := h.UpdatePlay(id, store.PlayMeta{
		Name: "雨夜重逢", Premise: "在站台再次见面", UserPersona: "旅人", ImageProfileID: "cinematic", Density: store.PlayDensityRich, Pacing: store.PlayPacingStory,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ImageProfileID != "cinematic" || updated.Status != store.PlayPaused || updated.CharacterID != "linwan" || updated.Density != store.PlayDensityRich || updated.Pacing != store.PlayPacingStory {
		t.Fatalf("updated play = %#v", updated)
	}
	stored, err := h.roots.Tavern.LoadPlay(id)
	if err != nil || stored.Name != "雨夜重逢" || stored.Premise != "在站台再次见面" || stored.UserPersona != "旅人" || stored.Density != store.PlayDensityRich || stored.Pacing != store.PlayPacingStory {
		t.Fatalf("stored play = %#v, %v", stored, err)
	}
}

func attachFakePlay(h *Host) {
	h.playSpine = func(context.Context, play.SpineInput) (play.SpineOutput, error) {
		return play.SpineOutput{Stations: []store.PlayStation{
			{ID: "meet", Pressure: "第一次必须表态"},
			{ID: "cost", Pressure: "代价开始反噬"},
			{ID: "end", Pressure: "必须做终局决定"},
		}}, nil
	}
	h.playArchitect = func(_ context.Context, in play.ArchitectInput) (play.ArchitectOutput, error) {
		return play.ArchitectOutput{SegmentID: in.CurrentStation.ID, Goal: in.CurrentStation.Pressure}, nil
	}
	h.playPlanner = func(_ context.Context, in play.PlannerInput) (play.PlannerOutput, error) {
		return play.PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
			{Kind: store.BeatDialogue, Speaker: "林晚", Location: "站台", CG: store.PlayCGKeep, RequiredBeats: []string{"你好"}},
			{Kind: store.BeatDialogue, Speaker: "林晚", Location: "站台", CG: store.PlayCGKeep, RequiredBeats: []string{"试探"}},
			{Kind: store.BeatChoice, Speaker: "林晚", Location: "站台", CG: store.PlayCGKeep, RequiredBeats: []string{"选择"}, Choices: []store.PlayChoice{
				{ID: "a", Label: "A", Consequence: "a", SetFacts: []string{"chose_a"}},
				{ID: "b", Label: "B", Consequence: "b", SetFacts: []string{"chose_b"}},
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
	id := seedPlay(t, h, store.PlayIdle)
	attachFakePlay(h)
	if err := h.StartPlay(id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.PausePlay() })
	if err := h.playActiveError(); err == nil || !strings.Contains(err.Error(), "剧场进行中") {
		t.Fatalf("live play should block novel: %v", err)
	}
}

func TestStaleActivePlayDoesNotBlockNovel(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayAwaitingChoice)
	if err := h.playActiveError(); err != nil {
		t.Fatalf("stale disk status should not block: %v", err)
	}
	meta, err := h.roots.Tavern.LoadPlay(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.PlayPaused {
		t.Fatalf("stale active play should be paused, got %s", meta.Status)
	}
}

func TestPausePlayPersistsWhenEngineNotRunning(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayRunning)
	if err := h.PausePlay(); err != nil {
		t.Fatal(err)
	}
	meta, err := h.roots.Tavern.LoadPlay(id)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Status != store.PlayPaused {
		t.Fatalf("pause without engine should persist paused, got %s", meta.Status)
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

func TestStartEngineAllowsStalePlayOnDisk(t *testing.T) {
	h := newPlayHost(t)
	seedPlay(t, h, store.PlayAwaitingChoice)
	if err := h.playActiveError(); err != nil {
		t.Fatalf("stale awaiting_choice should not block: %v", err)
	}
}

func TestPausedPlayDoesNotBlockNovel(t *testing.T) {
	h := newPlayHost(t)
	seedPlay(t, h, store.PlayPaused)
	if err := h.playActiveError(); err != nil {
		t.Fatalf("paused play should not block novel: %v", err)
	}
}

func TestPlayLogReturnsRuntimeAndStream(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	if err := h.roots.Tavern.AppendText("galgame/plays/"+id+"/runtime.log", "START play"); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.AppendRaw("galgame/plays/"+id+"/stream.log", "[thinking]\n先想"); err != nil {
		t.Fatal(err)
	}
	log, err := h.PlayLog(id)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.Events, "START play") || !strings.Contains(log.Stream, "先想") {
		t.Fatalf("log = %+v", log)
	}
}
