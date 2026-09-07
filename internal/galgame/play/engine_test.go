package play

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Leixx98/ai-write-stage/internal/store"
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

func newTestPlay(t *testing.T, ahead int) (string, *store.GalgameStore, *Engine, string) {
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
	engine := New(Config{
		Store: tavern, PlayID: playID, TextAhead: ahead,
		Spine: func(context.Context, SpineInput) (SpineOutput, error) {
			return SpineOutput{Stations: []store.PlayStation{
				{ID: "meet", Pressure: "第一次必须表态"},
				{ID: "cost", Pressure: "代价开始反噬"},
				{ID: "end", Pressure: "必须做终局决定"},
			}}, nil
		},
		Architect: func(_ context.Context, in ArchitectInput) (ArchitectOutput, error) {
			return ArchitectOutput{SegmentID: in.CurrentStation.ID, Goal: in.CurrentStation.Pressure, Notes: ""}, nil
		},
		Planner: func(_ context.Context, in PlannerInput) (PlannerOutput, error) {
			if in.LastStation {
				return PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
					{Kind: store.BeatNarration, Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{"雨停"}},
					{Kind: store.BeatDialogue, Speaker: "林晚", Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{"告别"}},
					{Kind: store.BeatDialogue, Speaker: "林晚", Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{"余味"}},
				}}, nil
			}
			return PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: []store.PlayBeatCard{
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGNew, CGIntent: "雨中车站", RequiredBeats: []string{"见面"}},
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"试探"}},
				{Kind: store.BeatDialogue, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"沉默"}},
				{Kind: store.BeatChoice, Speaker: "林晚", Location: "车站", CG: store.PlayCGKeep, RequiredBeats: []string{"选择"}, Choices: []store.PlayChoice{
					{ID: "stay", Label: "留下来", Consequence: "一起避雨", SetFacts: []string{"stayed"}, Ending: true},
					{ID: "leave", Label: "离开", Consequence: "各自走", SetFacts: []string{"left"}},
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
	return dir, tavern, engine, playID
}

func TestEngineStopsAtChoiceThenContinuesAfterChoose(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
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
	if progress.WriteHead != 4 {
		t.Fatalf("ending choice should complete without extra beats, got %+v", progress)
	}
	ledger, err := tavern.LoadLedger(playID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Facts) != 1 || ledger.Facts[0].ID != "stayed" {
		t.Fatalf("facts = %+v", ledger.Facts)
	}
	spine, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if spineOpen(spine) {
		t.Fatalf("ending should skip remaining stations: %+v", spine.Stations)
	}
}

func TestEngineStopsWhenBufferIsFull(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 2)
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
	_, tavern, engine, playID := newTestPlay(t, 8)
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

func TestEngineStartsImageBeforeWriterWithRequiredBeats(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
	var imageText string
	imageStarted := false
	writerSawImage := false
	engine.startImage = func(_ context.Context, _ string, beat *store.PlayBeat) error {
		imageStarted = true
		imageText = beat.Text
		beat.ImageJobID = "job_intent"
		return nil
	}
	engine.writer = func(_ context.Context, in WriterInput) (WriterOutput, error) {
		writerSawImage = imageStarted
		return WriterOutput{Speaker: in.Card.Speaker, Text: "台词正文"}, nil
	}
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.WriteHead >= 1
	}); err != nil {
		t.Fatal(err)
	}
	if !writerSawImage {
		t.Fatal("image should start before writer")
	}
	if imageText != "见面" {
		t.Fatalf("image text should be required beats, got %q", imageText)
	}
	beat, err := tavern.LoadBeat(playID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if beat.Text != "台词正文" || beat.ImageJobID != "job_intent" {
		t.Fatalf("saved beat = %+v", beat)
	}
}

func TestEngineImageStartFailureDoesNotStopWriting(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
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

func TestEngineWritesPlayRuntimeLog(t *testing.T) {
	dir, tavern, engine, playID := newTestPlay(t, 8)
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		if progress.GateOrdinal != 4 {
			return false
		}
		body, readErr := os.ReadFile(filepath.Join(dir, "galgame", "plays", playID, "runtime.log"))
		return readErr == nil && strings.Contains(string(body), "等待玩家选项")
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "galgame", "plays", playID, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"剧场引擎启动", "开始生成路线图", "开始规划当前站", "规划完成", "开始写拍", "已写拍", "等待玩家选项"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime.log missing %q:\n%s", want, text)
		}
	}
	index, err := os.ReadFile(filepath.Join(dir, "galgame", "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "play="+playID) {
		t.Fatalf("galgame/runtime.log missing play index:\n%s", index)
	}
}
