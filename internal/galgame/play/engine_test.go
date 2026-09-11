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

func testRichStation(id, pressure string) store.PlayStation {
	return store.PlayStation{
		ID: id, Title: id, Pressure: pressure,
		Summary:    pressure + " 的现场。冲突被摊开。局势转向。",
		MustHappen: []string{pressure},
		Forks: []store.PlayFork{
			{Tint: "默认推进"},
			{IfFacts: []string{"stayed"}, Tint: "留下后压力加重"},
		},
		Seeds:   []string{"secret"},
		Payoffs: []string{},
	}
}

func testRichSpine() SpineOutput {
	return SpineOutput{
		Throughline: "雨夜必须兑现重逢的代价",
		Stations: []store.PlayStation{
			testRichStation("meet", "第一次必须表态"),
			testRichStation("cost", "代价开始反噬"),
			testRichStation("end", "必须做终局决定"),
		},
		Threads: []store.PlayThread{{ID: "secret", Hint: "她跟踪过玩家", Status: store.ThreadOpen, PlantAt: "meet", PayoffAt: "cost"}},
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
			return testRichSpine(), nil
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
		body, readErr := os.ReadFile(filepath.Join(dir, store.TavernDirName, "plays", playID, "runtime.log"))
		return readErr == nil && strings.Contains(string(body), "等待玩家选项")
	}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, store.TavernDirName, "plays", playID, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, want := range []string{"剧场引擎启动", "开始生成路线图", "开始规划当前站", "规划完成", "开始写拍", "已写拍", "等待玩家选项"} {
		if !strings.Contains(text, want) {
			t.Fatalf("runtime.log missing %q:\n%s", want, text)
		}
	}
	index, err := os.ReadFile(filepath.Join(dir, store.TavernDirName, "runtime.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(index), "play="+playID) {
		t.Fatalf("tavern/runtime.log missing play index:\n%s", index)
	}
}

func TestEnginePurePacingWritesThroughStations(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	char := store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "女主角"}
	if err := tavern.SaveCharacter(char); err != nil {
		t.Fatal(err)
	}
	playID := "rain_night"
	if err := tavern.SavePlay(store.PlayMeta{ID: playID, Name: "雨夜", CharacterID: char.ID, Premise: "雨夜重逢", Pacing: store.PlayPacingPure, Status: store.PlayIdle}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(playID, store.PlayProgress{}); err != nil {
		t.Fatal(err)
	}
	cards := make([]store.PlayBeatCard, 6)
	for i := range cards {
		cards[i] = store.PlayBeatCard{Kind: store.BeatDialogue, Speaker: "林晚", Location: "巷口", CG: store.PlayCGKeep, RequiredBeats: []string{fmt.Sprintf("拍%d", i+1)}}
	}
	engine := New(Config{
		Store: tavern, PlayID: playID, TextAhead: 20,
		Spine: func(context.Context, SpineInput) (SpineOutput, error) {
			return testRichSpine(), nil
		},
		Architect: func(_ context.Context, in ArchitectInput) (ArchitectOutput, error) {
			return ArchitectOutput{SegmentID: in.CurrentStation.ID, Goal: in.CurrentStation.Pressure}, nil
		},
		Planner: func(_ context.Context, in PlannerInput) (PlannerOutput, error) {
			out := append([]store.PlayBeatCard(nil), cards...)
			return PlannerOutput{SegmentID: in.Architect.SegmentID, Cards: out}, nil
		},
		Writer: func(_ context.Context, in WriterInput) (WriterOutput, error) {
			text := "……"
			if len(in.Card.RequiredBeats) > 0 {
				text = in.Card.RequiredBeats[0]
			}
			return WriterOutput{Speaker: in.Card.Speaker, Text: text}, nil
		},
	})
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		meta, _ := tavern.LoadPlay(playID)
		return meta.Status == store.PlayCompleted
	}); err != nil {
		t.Fatal(err)
	}
	progress, err := tavern.LoadProgress(playID)
	if err != nil {
		t.Fatal(err)
	}
	if progress.WriteHead != 18 || progress.GateOrdinal != 0 {
		t.Fatalf("pure play should write through without a gate, got %+v", progress)
	}
}

func TestEngineSpineHasThroughlineAndThreads(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	spine, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if spine.Throughline == "" || len(spine.Threads) == 0 {
		t.Fatalf("spine missing throughline/threads: %+v", spine)
	}
	if n := len(spine.Stations); n < 3 || n > 6 {
		t.Fatalf("station count = %d", n)
	}
	for _, station := range spine.Stations {
		if station.Summary == "" || len(station.MustHappen) == 0 || len(station.Forks) < 2 {
			t.Fatalf("thin station: %+v", station)
		}
	}
}

func TestChooseRevisesNextStationOnly(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
	engine.reviseNext = func(_ context.Context, in ReviseNextInput) (ReviseNextOutput, error) {
		next := in.NextStation
		next.Summary = "选择离开后，代价改走暗线反噬。"
		next.MustHappen = []string{"暗线被点破"}
		next.Forks = []store.PlayFork{{Tint: "暗线升温"}, {IfFacts: []string{"left"}, Tint: "她不再挽留"}}
		return ReviseNextOutput{
			NextStation: next,
			Threads:     []store.PlayThread{{ID: "secret", Hint: "她跟踪过玩家", Status: store.ThreadPlanted}},
		}, nil
	}
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	before, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Choose("leave"); err != nil {
		t.Fatal(err)
	}
	after, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Stations[1].ID != "cost" || after.Stations[2].ID != "end" {
		t.Fatalf("far station ids changed: %+v", after.Stations)
	}
	if after.Stations[1].Summary == before.Stations[1].Summary || !strings.Contains(after.Stations[1].Summary, "暗线") {
		t.Fatalf("next station should change, before=%q after=%q", before.Stations[1].Summary, after.Stations[1].Summary)
	}
	if after.Stations[2].Summary != before.Stations[2].Summary {
		t.Fatalf("far station should stay: %+v", after.Stations[2])
	}
	if len(after.Threads) == 0 || after.Threads[0].Status != store.ThreadPlanted {
		t.Fatalf("thread status = %+v", after.Threads)
	}
	beats, err := tavern.ListBeats(playID)
	if err != nil || len(beats) < 4 {
		t.Fatalf("played beats missing: %+v %v", beats, err)
	}
	ledger, err := tavern.LoadLedger(playID)
	if err != nil || len(ledger.Facts) != 1 || ledger.Facts[0].ID != "left" {
		t.Fatalf("facts should come from the choice only: %+v %v", ledger, err)
	}
}

func TestChooseEndingDoesNotRevise(t *testing.T) {
	_, tavern, engine, playID := newTestPlay(t, 8)
	called := false
	engine.reviseNext = func(context.Context, ReviseNextInput) (ReviseNextOutput, error) {
		called = true
		return ReviseNextOutput{}, fmt.Errorf("should not revise")
	}
	runEngine(t, engine)
	if err := waitUntil(context.Background(), func() bool {
		progress, _ := tavern.LoadProgress(playID)
		return progress.GateOrdinal == 4
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Choose("stay"); err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("ending choice should not revise next station")
	}
}

func TestReplanKeepsPlayedBeatsAndFacts(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	char := store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "女主角"}
	if err := tavern.SaveCharacter(char); err != nil {
		t.Fatal(err)
	}
	playID := "rain_night"
	if err := tavern.SavePlay(store.PlayMeta{ID: playID, Name: "雨夜", CharacterID: char.ID, Premise: "雨夜重逢", Status: store.PlayPaused}); err != nil {
		t.Fatal(err)
	}
	spine := prepareSpine(testRichSpine())
	spine.Stations[0].Status = store.StationActive
	if err := tavern.SaveSpine(playID, spine); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveLedger(playID, store.PlayLedger{Facts: []store.PlayFact{{ID: "left"}}}); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(playID, store.PlayProgress{
		PlayHead: 2, WriteHead: 4, SegmentID: "meet",
		ChoiceHistory: []store.PlayChoiceRecord{{Ordinal: 4, ChoiceID: "leave", Label: "离开", SetFacts: []string{"left"}}},
	}); err != nil {
		t.Fatal(err)
	}
	for i, text := range []string{"见面", "试探", "未看一", "未看二"} {
		if err := tavern.SaveBeat(playID, store.PlayBeat{Ordinal: i + 1, SegmentID: "meet", Kind: store.BeatDialogue, Speaker: "林晚", Text: text, CG: store.PlayCGKeep}); err != nil {
			t.Fatal(err)
		}
	}
	if err := tavern.SaveOutline(playID, store.PlayOutline{SegmentID: "meet", StationID: "meet", Cards: []store.PlayBeatCard{{Kind: store.BeatDialogue}}, NextCard: 2}); err != nil {
		t.Fatal(err)
	}
	engine := New(Config{
		Store: tavern, PlayID: playID,
		Replan: func(_ context.Context, in ReplanInput) (ReplanOutput, error) {
			if !strings.Contains(in.Instruction, "暗线") {
				t.Fatalf("instruction = %q", in.Instruction)
			}
			tail := make([]store.PlayStation, 0, len(in.RemainingStations))
			for _, station := range in.RemainingStations {
				station.Summary = "刚才那条线降温，改走暗线。"
				station.MustHappen = []string{"暗线被点破"}
				station.Forks = []store.PlayFork{{Tint: "暗线升温"}, {Tint: "关系降温"}}
				tail = append(tail, station)
			}
			return ReplanOutput{Throughline: "改走暗线仍要兑现代价", Stations: tail, Threads: []store.PlayThread{{ID: "secret", Hint: "暗线回收", Status: store.ThreadOpen}}}, nil
		},
	})
	if err := engine.Replan(context.Background(), "刚才那条线降温，改走暗线"); err != nil {
		t.Fatal(err)
	}
	beats, err := tavern.ListBeats(playID)
	if err != nil || len(beats) != 2 || beats[0].Text != "见面" || beats[1].Text != "试探" {
		t.Fatalf("played beats should remain: %+v %v", beats, err)
	}
	progress, err := tavern.LoadProgress(playID)
	if err != nil || progress.PlayHead != 2 || progress.WriteHead != 2 || progress.SegmentID != "" {
		t.Fatalf("progress = %+v %v", progress, err)
	}
	if len(progress.ChoiceHistory) != 1 || progress.ChoiceHistory[0].ChoiceID != "leave" {
		t.Fatalf("choice history changed: %+v", progress.ChoiceHistory)
	}
	ledger, err := tavern.LoadLedger(playID)
	if err != nil || len(ledger.Facts) != 1 || ledger.Facts[0].ID != "left" {
		t.Fatalf("facts changed: %+v %v", ledger, err)
	}
	outline, err := tavern.LoadOutline(playID)
	if err != nil || len(outline.Cards) != 0 {
		t.Fatalf("outline should be cleared: %+v %v", outline, err)
	}
	got, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Stations[0].Summary, "暗线") || !strings.Contains(got.Stations[1].Summary, "暗线") {
		t.Fatalf("remaining stations should follow instruction: %+v", got.Stations)
	}
	if got.Stations[0].Status != store.StationActive {
		t.Fatalf("current station status = %s", got.Stations[0].Status)
	}
}

func TestReplanAtChoiceKeepsCurrentStationBeats(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	char := store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "女主角"}
	if err := tavern.SaveCharacter(char); err != nil {
		t.Fatal(err)
	}
	playID := "rain_night"
	if err := tavern.SavePlay(store.PlayMeta{ID: playID, Name: "雨夜", CharacterID: char.ID, Premise: "雨夜重逢", Status: store.PlayAwaitingChoice}); err != nil {
		t.Fatal(err)
	}
	spine := prepareSpine(testRichSpine())
	spine.Stations[0].Status = store.StationActive
	if err := tavern.SaveSpine(playID, spine); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(playID, store.PlayProgress{PlayHead: 1, WriteHead: 4, GateOrdinal: 4, SegmentID: "meet"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 4; i++ {
		kind := store.BeatDialogue
		if i == 4 {
			kind = store.BeatChoice
		}
		if err := tavern.SaveBeat(playID, store.PlayBeat{Ordinal: i, SegmentID: "meet", Kind: kind, Speaker: "林晚", Text: fmt.Sprintf("拍%d", i), CG: store.PlayCGKeep}); err != nil {
			t.Fatal(err)
		}
	}
	engine := New(Config{
		Store: tavern, PlayID: playID,
		Replan: func(_ context.Context, in ReplanInput) (ReplanOutput, error) {
			if len(in.KeptStations) != 1 || in.KeptStations[0].ID != "meet" {
				t.Fatalf("kept = %+v", in.KeptStations)
			}
			tail := make([]store.PlayStation, 0, len(in.RemainingStations))
			for _, station := range in.RemainingStations {
				station.Summary = "选项未选时先改后续暗线。"
				station.MustHappen = []string{"暗线"}
				station.Forks = []store.PlayFork{{Tint: "降温"}, {Tint: "摊牌"}}
				tail = append(tail, station)
			}
			return ReplanOutput{Throughline: in.Throughline, Stations: tail, Threads: in.Threads}, nil
		},
	})
	if err := engine.Replan(context.Background(), "改走暗线"); err != nil {
		t.Fatal(err)
	}
	beats, err := tavern.ListBeats(playID)
	if err != nil || len(beats) != 4 || beats[3].Kind != store.BeatChoice {
		t.Fatalf("choice station beats should remain: %+v %v", beats, err)
	}
	progress, err := tavern.LoadProgress(playID)
	if err != nil || progress.PlayHead != 1 || progress.WriteHead != 4 || progress.GateOrdinal != 4 {
		t.Fatalf("choice progress should stay: %+v %v", progress, err)
	}
	got, err := tavern.LoadSpine(playID)
	if err != nil || got.Stations[0].Summary == "选项未选时先改后续暗线。" || !strings.Contains(got.Stations[1].Summary, "暗线") {
		t.Fatalf("only later stations should change: %+v %v", got, err)
	}
}

func TestReplanPurePacingChangesLaterStations(t *testing.T) {
	dir := t.TempDir()
	tavern := store.Open(dir, dir).Tavern
	char := store.GalgameCharacter{ID: "linwan", Name: "林晚", Description: "女主角"}
	if err := tavern.SaveCharacter(char); err != nil {
		t.Fatal(err)
	}
	playID := "rain_night"
	if err := tavern.SavePlay(store.PlayMeta{ID: playID, Name: "雨夜", CharacterID: char.ID, Premise: "雨夜重逢", Pacing: store.PlayPacingPure, Status: store.PlayPaused}); err != nil {
		t.Fatal(err)
	}
	spine := prepareSpine(testRichSpine())
	spine.Stations[0].Status = store.StationDone
	spine.Stations[1].Status = store.StationActive
	if err := tavern.SaveSpine(playID, spine); err != nil {
		t.Fatal(err)
	}
	if err := tavern.SaveProgress(playID, store.PlayProgress{PlayHead: 6, WriteHead: 8}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 8; i++ {
		if err := tavern.SaveBeat(playID, store.PlayBeat{Ordinal: i, SegmentID: "meet", Kind: store.BeatDialogue, Speaker: "林晚", Text: fmt.Sprintf("拍%d", i), CG: store.PlayCGKeep}); err != nil {
			t.Fatal(err)
		}
	}
	engine := New(Config{
		Store: tavern, PlayID: playID,
		Replan: func(_ context.Context, in ReplanInput) (ReplanOutput, error) {
			if len(in.KeptStations) != 1 || in.KeptStations[0].ID != "meet" {
				t.Fatalf("kept = %+v", in.KeptStations)
			}
			tail := []store.PlayStation{}
			for _, station := range in.RemainingStations {
				station.Summary = "纯剧情改走暗线收束。"
				station.MustHappen = []string{"暗线收束"}
				station.Forks = []store.PlayFork{{Tint: "降温"}, {Tint: "摊牌"}}
				tail = append(tail, station)
			}
			return ReplanOutput{Throughline: in.Throughline, Stations: tail, Threads: in.Threads}, nil
		},
	})
	if err := engine.Replan(context.Background(), "改走暗线"); err != nil {
		t.Fatal(err)
	}
	beats, err := tavern.ListBeats(playID)
	if err != nil || len(beats) != 6 {
		t.Fatalf("played beats should remain: %d %v", len(beats), err)
	}
	got, err := tavern.LoadSpine(playID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stations[0].Status != store.StationDone || !strings.Contains(got.Stations[1].Summary, "暗线") {
		t.Fatalf("later stations = %+v", got.Stations)
	}
}
