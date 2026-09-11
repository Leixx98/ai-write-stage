package host

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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
		station := func(id, pressure string) store.PlayStation {
			return store.PlayStation{
				ID: id, Title: id, Pressure: pressure,
				Summary:    pressure + " 的现场。冲突被摊开。局势转向。",
				MustHappen: []string{pressure},
				Forks:      []store.PlayFork{{Tint: "默认推进"}, {Tint: "另一条近处分叉"}},
			}
		}
		return play.SpineOutput{
			Throughline: "雨夜必须兑现重逢的代价",
			Stations:    []store.PlayStation{station("meet", "第一次必须表态"), station("cost", "代价开始反噬"), station("end", "必须做终局决定")},
			Threads:     []store.PlayThread{{ID: "secret", Hint: "她跟踪过玩家", Status: store.ThreadOpen}},
		}, nil
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

func waitForPlay(t *testing.T, h *Host, id string, ready func(store.PlayMeta, store.PlayProgress) bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		meta, metaErr := h.roots.Tavern.LoadPlay(id)
		progress, progressErr := h.roots.Tavern.LoadProgress(id)
		if metaErr == nil && progressErr == nil && ready(meta, progress) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	meta, _ := h.roots.Tavern.LoadPlay(id)
	progress, _ := h.roots.Tavern.LoadProgress(id)
	t.Fatalf("timeout waiting for play: meta=%+v progress=%+v", meta, progress)
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

func TestReplanPlayKeepsFactsAndPlayedBeats(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayPaused)
	attachFakePlay(h)
	station := func(id, pressure string) store.PlayStation {
		return store.PlayStation{
			ID: id, Title: id, Pressure: pressure,
			Summary: pressure + " 的现场。", MustHappen: []string{pressure},
			Forks: []store.PlayFork{{Tint: "默认"}, {Tint: "分叉"}},
		}
	}
	if err := h.roots.Tavern.SaveSpine(id, store.PlaySpine{
		Throughline: "兑现重逢",
		Stations: []store.PlayStation{
			station("meet", "第一次必须表态"),
			station("cost", "代价开始反噬"),
			station("end", "必须做终局决定"),
		},
		Threads: []store.PlayThread{{ID: "secret", Hint: "跟踪", Status: store.ThreadOpen}},
	}); err != nil {
		t.Fatal(err)
	}
	spine, _ := h.roots.Tavern.LoadSpine(id)
	spine.Stations[0].Status = store.StationDone
	spine.Stations[1].Status = store.StationActive
	if err := h.roots.Tavern.SaveSpine(id, spine); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.SaveLedger(id, store.PlayLedger{Facts: []store.PlayFact{{ID: "left"}}}); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.SaveProgress(id, store.PlayProgress{PlayHead: 4, WriteHead: 6, ChoiceHistory: []store.PlayChoiceRecord{{Ordinal: 4, ChoiceID: "leave", SetFacts: []string{"left"}}}}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 6; i++ {
		if err := h.roots.Tavern.SaveBeat(id, store.PlayBeat{Ordinal: i, Text: "拍", CG: store.PlayCGKeep}); err != nil {
			t.Fatal(err)
		}
	}
	h.playReplan = func(_ context.Context, in play.ReplanInput) (play.ReplanOutput, error) {
		tail := make([]store.PlayStation, 0, len(in.RemainingStations))
		for _, item := range in.RemainingStations {
			item.Summary = "按指令改走暗线。"
			item.MustHappen = []string{"暗线"}
			item.Forks = []store.PlayFork{{Tint: "降温"}, {Tint: "摊牌"}}
			tail = append(tail, item)
		}
		return play.ReplanOutput{Throughline: in.Throughline, Stations: tail, Threads: in.Threads}, nil
	}
	if err := h.ReplanPlay(id, "改走暗线"); err != nil {
		t.Fatal(err)
	}
	beats, err := h.roots.Tavern.ListBeats(id)
	if err != nil || len(beats) != 4 {
		t.Fatalf("beats = %d %v", len(beats), err)
	}
	ledger, err := h.roots.Tavern.LoadLedger(id)
	if err != nil || len(ledger.Facts) != 1 || ledger.Facts[0].ID != "left" {
		t.Fatalf("facts = %+v %v", ledger, err)
	}
	got, err := h.roots.Tavern.LoadSpine(id)
	if err != nil || !strings.Contains(got.Stations[1].Summary, "暗线") {
		t.Fatalf("spine = %+v %v", got, err)
	}
}

func TestReplanLiveChoiceKeepsGateAndCurrentStation(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	attachFakePlay(h)
	h.playReplan = func(_ context.Context, in play.ReplanInput) (play.ReplanOutput, error) {
		if len(in.KeptStations) != 1 || in.KeptStations[0].ID != "meet" {
			t.Fatalf("kept stations = %+v", in.KeptStations)
		}
		tail := append([]store.PlayStation{}, in.RemainingStations...)
		for i := range tail {
			tail[i].Summary = "当前选项之后改走暗线。"
			tail[i].MustHappen = []string{"暗线推进"}
			tail[i].Forks = []store.PlayFork{{Tint: "默认"}, {Tint: "摊牌"}}
		}
		return play.ReplanOutput{Throughline: in.Throughline, Stations: tail, Threads: in.Threads}, nil
	}
	if err := h.StartPlay(id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.PausePlay() })
	waitForPlay(t, h, id, func(_ store.PlayMeta, progress store.PlayProgress) bool {
		return progress.GateOrdinal != 0
	})
	before, err := h.roots.Tavern.LoadSpine(id)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ReplanPlay(id, "后续改走暗线"); err != nil {
		t.Fatal(err)
	}
	waitForPlay(t, h, id, func(meta store.PlayMeta, progress store.PlayProgress) bool {
		return meta.Status == store.PlayAwaitingChoice && progress.GateOrdinal != 0
	})
	after, err := h.roots.Tavern.LoadSpine(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stations[0].Summary != before.Stations[0].Summary {
		t.Fatalf("current choice station changed: before=%q after=%q", before.Stations[0].Summary, after.Stations[0].Summary)
	}
	if !strings.Contains(after.Stations[1].Summary, "暗线") {
		t.Fatalf("next station was not replanned: %+v", after.Stations[1])
	}
}

func TestReplanFailureRestartsLivePlay(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	attachFakePlay(h)
	h.playReplan = func(context.Context, play.ReplanInput) (play.ReplanOutput, error) {
		return play.ReplanOutput{}, fmt.Errorf("temporary model failure")
	}
	if err := h.StartPlay(id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.PausePlay() })
	waitForPlay(t, h, id, func(_ store.PlayMeta, progress store.PlayProgress) bool {
		return progress.GateOrdinal != 0
	})
	if err := h.ReplanPlay(id, "改走暗线"); err == nil || !strings.Contains(err.Error(), "temporary model failure") {
		t.Fatalf("replan error = %v", err)
	}
	waitForPlay(t, h, id, func(meta store.PlayMeta, progress store.PlayProgress) bool {
		return meta.Status == store.PlayAwaitingChoice && progress.GateOrdinal != 0
	})
	h.mu.Lock()
	running := h.playEngine
	h.mu.Unlock()
	if running == nil || running.PlayID() != id {
		t.Fatal("live play was not restarted after replan failure")
	}
}

func TestChooseStalePlayRevisesNextStation(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayAwaitingChoice)
	attachFakePlay(h)
	spineOut, err := h.playSpine(context.Background(), play.SpineInput{})
	if err != nil {
		t.Fatal(err)
	}
	spine := store.PlaySpine{Throughline: spineOut.Throughline, Stations: spineOut.Stations, Threads: spineOut.Threads}
	spine.Stations[0].Status = store.StationActive
	if err := h.roots.Tavern.SaveSpine(id, spine); err != nil {
		t.Fatal(err)
	}
	choice := store.PlayChoice{ID: "a", Label: "A", Consequence: "a", SetFacts: []string{"chose_a"}}
	if err := h.roots.Tavern.SaveBeat(id, store.PlayBeat{Ordinal: 1, SegmentID: "meet", Kind: store.BeatChoice, Text: "选择", Choices: []store.PlayChoice{choice}, CG: store.PlayCGKeep}); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.SaveProgress(id, store.PlayProgress{PlayHead: 1, WriteHead: 1, GateOrdinal: 1, SegmentID: "meet"}); err != nil {
		t.Fatal(err)
	}
	called := false
	h.playReviseNext = func(_ context.Context, in play.ReviseNextInput) (play.ReviseNextOutput, error) {
		called = true
		next := in.NextStation
		next.Summary = "重启后仍按选择修订。"
		return play.ReviseNextOutput{NextStation: next}, nil
	}
	if _, err := h.ChoosePlay(id, "a"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.PausePlay() })
	if !called {
		t.Fatal("stale play choice did not invoke next-station revision")
	}
	got, err := h.roots.Tavern.LoadSpine(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stations[1].Summary != "重启后仍按选择修订。" {
		t.Fatalf("next station summary = %q", got.Stations[1].Summary)
	}
}

func TestReplanPlayHonorsExclusiveWork(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayPaused)
	h.exclusive = "导入"
	called := false
	h.playReplan = func(context.Context, play.ReplanInput) (play.ReplanOutput, error) {
		called = true
		return play.ReplanOutput{}, nil
	}
	if err := h.ReplanPlay(id, "改走暗线"); err == nil || !strings.Contains(err.Error(), "导入") {
		t.Fatalf("exclusive error = %v", err)
	}
	if called {
		t.Fatal("replan ran while another exclusive operation was active")
	}
}

func TestPlayLogReturnsRuntimeAndStream(t *testing.T) {
	h := newPlayHost(t)
	id := seedPlay(t, h, store.PlayIdle)
	if err := h.roots.Tavern.AppendText("plays/"+id+"/runtime.log", "START play"); err != nil {
		t.Fatal(err)
	}
	if err := h.roots.Tavern.AppendRaw("plays/"+id+"/stream.log", "[thinking]\n先想"); err != nil {
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
