package play

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/voocel/ainovel-cli/internal/store"
)

func runEngine(t *testing.T, engine *Engine) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- engine.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("engine did not stop")
		}
	})
	return cancel, done
}

func waitUntil(ctx context.Context, cond func() bool) error {
	deadline := time.Now().Add(3 * time.Second)
	for {
		if cond() {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for play engine")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func newTestPlay(t *testing.T, ahead int) (*store.GalgameStore, *Engine, string) {
	t.Helper()
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	char := store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "女主角"}
	if err := tavern.SaveCharacter(char); err != nil {
		t.Fatal(err)
	}
	playID := "rain_night"
	if err := tavern.SavePlay(store.PlayMeta{ID: playID, Name: "雨夜", CharacterID: char.ID, Premise: "雨夜重逢", Status: store.PlayIdle}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(playID, store.PlayProgress{}); err != nil {
		t.Fatal(err)
	}
	archCount := 0
	engine := New(Config{
		Store: tavern, PlayID: playID, TextAhead: ahead,
		Architect: func(context.Context, ArchitectInput) (ArchitectOutput, error) {
			archCount++
			if archCount == 1 {
				return ArchitectOutput{SegmentID: "meet", Goal: "重逢并做出选择", Notes: ""}, nil
			}
			return ArchitectOutput{SegmentID: "end", Goal: "收束", CompleteAfterSegment: true}, nil
		},
		Planner: func(_ context.Context, in PlannerInput) (PlannerOutput, error) {
			if in.Architect.CompleteAfterSegment {
				return PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
					{Kind: store.BeatNarration, Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{"雨停"}},
					{Kind: store.BeatDialogue, Speaker: "林晚", Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{"告别"}},
				}}, nil
			}
			return PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGNew, CGIntent: "雨中车站", RequiredBeats: []string{"见面"}},
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"试探"}},
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"沉默"}},
				{Kind: store.BeatChoice, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"选择"}, Choices: []store.PlayChoice{
					{ID: "stay", Label: "留下来", Consequence: "一起避雨"},
					{ID: "leave", Label: "离开", Consequence: "各自走"},
				}},
			}}, nil
		},
		Writer: func(_ context.Context, in WriterInput) (WriterOutput, error) {
			text := "……"
			if len(in.Card.RequiredBeats) > 0 {
				text = in.Card.RequiredBeats[0]
			}
			return WriterOutput{Speaker: in.Card.Speaker, Text: text}, nil
		},
	})
	return tavern, engine, playID
}

func TestEngineStopsAtChoiceThenContinuesAfterChoose(t *testing.T) {
	tavern, engine, playID := newTestPlay(t, 8)
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := tavern.LoadProgress(playID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.WriteHead != 4 || progress.PlayHead != 1 {
		t.Fatalf("progress = %+v", progress)
	}
	meta, _ := tavern.LoadPlay(playID)
	if meta.Status != store.PlayAwaitingChoice {
		t.Fatalf("status = %s", meta.Status)
	}
	if _, err := engine.Choose("stay"); err != nil {
		t.Fatal(err)
	}
	progress, _ = tavern.LoadProgress(playID)
	if progress.PlayHead != 4 || progress.GateOrdinal != 0 {
		t.Fatalf("choose should stay on the resolved gate, got %+v", progress)
	}
	if err := waitUntil(context.Background(), func() bool {
		meta, _ := tavern.LoadPlay(playID)
		return meta.Status == store.PlayCompleted
	}); err != nil {
		t.Fatal(err)
	}
	progress, _ = tavern.LoadProgress(playID)
	if progress.WriteHead != 6 {
		t.Fatalf("expected 6 beats, got %+v", progress)
	}
}

func TestEngineStopsWhenBufferIsFull(t *testing.T) {
	tavern, engine, playID := newTestPlay(t, 2)
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.WriteHead >= 3
	}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)
	progress, _ := tavern.LoadProgress(playID)
	if progress.WriteHead != 3 {
		t.Fatalf("buffer should stop at write_head=3, got %+v", progress)
	}
	if progress.GateOrdinal != 0 {
		t.Fatal("should not have reached the choice yet")
	}
	if _, err := engine.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEngineCGKeepDoesNotStartImage(t *testing.T) {
	tavern, engine, playID := newTestPlay(t, 8)
	var started []int
	engine.startImage = func(_ context.Context, _ string, beat *store.PlayBeat) error {
		started = append(started, beat.Ordinal)
		beat.ImageJobID = "job_1"
		return nil
	}
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	if len(started) != 1 || started[0] != 1 {
		t.Fatalf("started = %v", started)
	}
	beat, _ := tavern.LoadBeat(playID, 1)
	if beat.ImageJobID != "job_1" {
		t.Fatalf("job id = %q", beat.ImageJobID)
	}
	beat, _ = tavern.LoadBeat(playID, 2)
	if beat.ImageJobID != "" {
		t.Fatalf("keep beat should not have job, got %q", beat.ImageJobID)
	}
}

func TestEngineImageStartFailureDoesNotStopWriting(t *testing.T) {
	tavern, engine, playID := newTestPlay(t, 8)
	engine.startImage = func(_ context.Context, _ string, _ *store.PlayBeat) error {
		return fmt.Errorf("comfy down")
	}
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	beat, err := tavern.LoadBeat(playID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if beat.ImageJobID != "" || beat.ImageError == "" {
		t.Fatalf("new beat should record image error without a job, got %+v", beat)
	}
	meta, _ := tavern.LoadPlay(playID)
	if meta.LastError != "" || meta.Status != store.PlayAwaitingChoice {
		t.Fatalf("writing should continue after image failure, meta = %+v", meta)
	}
}
