package flow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

func TestLoadStateReturnsProgressReadError(t *testing.T) {
	dir := t.TempDir()
	st := storepkg.NewStore(dir)
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta", "progress.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadState(st); err == nil {
		t.Fatal("损坏的 progress 必须阻止路由")
	}
}

func TestLoadStateReportsNextChapterPlan(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(st)
	if err != nil {
		t.Fatal(err)
	}
	if state.HasNextChapterPlan {
		t.Fatal("empty draft store must not report a chapter plan")
	}
	if err := st.Drafts.SaveChapterPlan(domain.ChapterPlan{Chapter: 1, Title: "第一章"}); err != nil {
		t.Fatal(err)
	}
	state, err = LoadState(st)
	if err != nil {
		t.Fatal(err)
	}
	if !state.HasNextChapterPlan {
		t.Fatal("saved next-chapter plan was not reflected in routing state")
	}
}

func TestLoadStateReportsWritingUnitProgress(t *testing.T) {
	st := storepkg.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{Chapter: 1, Scenes: []domain.ScenePlan{{
		ID:    "1-1",
		Units: []domain.WritingUnit{{ID: "1-1-1", TargetChars: 800, EndAnchor: "落点"}},
	}}}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	state, err := LoadState(st)
	if err != nil {
		t.Fatal(err)
	}
	if state.NextChapterWriting == nil || state.NextChapterWriting.Next == nil || state.NextChapterWriting.Next.Unit.ID != "1-1-1" {
		t.Fatalf("pending unit progress missing from state: %+v", state.NextChapterWriting)
	}
	if _, _, err := st.Drafts.SaveWritingUnit(1, 1, 1, 1, "正文"); err != nil {
		t.Fatal(err)
	}
	state, err = LoadState(st)
	if err != nil {
		t.Fatal(err)
	}
	if state.NextChapterWriting == nil || !state.NextChapterWriting.Complete {
		t.Fatalf("completed unit progress missing from state: %+v", state.NextChapterWriting)
	}
}

// helper：构造一个处于 Writing 阶段、分层模式的 Progress。
func writingProgress(completed []int, flow domain.FlowState) *domain.Progress {
	return &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              flow,
		Layered:           true,
		CompletedChapters: completed,
	}
}

func TestRoute_NilProgress(t *testing.T) {
	if got := Route(State{Progress: nil}); got != nil {
		t.Fatalf("expected nil for nil progress, got %+v", got)
	}
}

func TestRoute_PhaseComplete(t *testing.T) {
	s := State{Progress: &domain.Progress{Phase: domain.PhaseComplete}}
	if got := Route(s); got != nil {
		t.Fatalf("expected nil at PhaseComplete, got %+v", got)
	}
}

func TestRoute_NonWritingPhasesDelegateToLLM(t *testing.T) {
	for _, phase := range []domain.Phase{domain.PhaseInit, domain.PhasePremise, domain.PhaseOutline} {
		s := State{Progress: &domain.Progress{Phase: phase}, FoundationMissing: []string{"premise"}}
		if got := Route(s); got != nil {
			t.Fatalf("phase %s should return nil, got %+v", phase, got)
		}
	}
}

func TestRoute_PendingRewritesFirst(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowRewriting)
	p.PendingRewrites = []int{3, 5}
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer for rewrites, got %+v", got)
	}
	if got.Task != "重写第 3 章" {
		t.Errorf("expected '重写第 3 章', got %q", got.Task)
	}
	if got.Chapter != 3 {
		t.Errorf("expected Chapter=3, got %d", got.Chapter)
	}
}

func TestRoute_PendingPolishingVerb(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowPolishing)
	p.PendingRewrites = []int{2}
	got := Route(State{Progress: p})
	if got == nil || got.Task != "打磨第 2 章" {
		t.Fatalf("expected polish verb, got %+v", got)
	}
}

func TestRoute_LegacyReviewingContinuesWithoutEditor(t *testing.T) {
	p := writingProgress([]int{1, 2}, domain.FlowReviewing)
	got := Route(State{Progress: p})
	if got == nil || got.Agent != "chapter_planner" {
		t.Fatalf("legacy reviewing state must continue normal planning, got %+v", got)
	}
}

func TestRoute_SteeringDelegatesToLLM(t *testing.T) {
	p := writingProgress([]int{1}, domain.FlowSteering)
	if got := Route(State{Progress: p}); got != nil {
		t.Fatalf("expected nil during steering, got %+v", got)
	}
}

func TestRoute_ArcEndSkipsReviewAndContinuesPlanning(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:     true,
			Volume:       1,
			Arc:          2,
			StartChapter: 11,
			EndChapter:   22,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "chapter_planner" {
		t.Fatalf("arc end without a structural action must continue to chapter planning, got %+v", got)
	}
}

func TestRoute_ArcEndDispatchesNextUnitWithoutSummary(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd: true,
			Volume:   1,
			Arc:      2,
		},
		HasNextChapterPlan: true,
		NextChapterWriting: &domain.WritingProgress{
			Chapter: 11, TotalUnits: 1,
			Next: &domain.WritingUnitAssignment{Ordinal: 1, Unit: domain.WritingUnit{ID: "11-1-1"}},
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("arc end must dispatch the next planned unit without an Editor summary, got %+v", got)
	}
}

func TestRoute_VolumeEndSkipsSummary(t *testing.T) {
	p := writingProgress([]int{20}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 20,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:    true,
			IsVolumeEnd: true,
			Volume:      1,
			Arc:         3,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "chapter_planner" {
		t.Fatalf("volume end without a structural action must continue planning, got %+v", got)
	}
}

func TestRoute_NeedsArcExpansion(t *testing.T) {
	p := writingProgress([]int{10}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 10,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			Volume:         1,
			Arc:            2,
			NextVolume:     1,
			NextArc:        3,
			NeedsExpansion: true,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("expected architect_long for expansion, got %+v", got)
	}
	if got.Reason != "下一弧骨架待展开" {
		t.Errorf("reason mismatch: %q", got.Reason)
	}
}

func TestRoute_NeedsNewVolume(t *testing.T) {
	p := writingProgress([]int{30}, domain.FlowWriting)
	s := State{
		Progress:      p,
		LastCompleted: 30,
		ArcBoundary: &storepkg.ArcBoundary{
			IsArcEnd:       true,
			IsVolumeEnd:    true,
			Volume:         2,
			Arc:            4,
			NeedsNewVolume: true,
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "architect_long" || got.Reason != "卷末需决定追加新卷、收官卷或结束全书" {
		t.Fatalf("expected append_volume/complete_book dispatch, got %+v", got)
	}
}

func TestRoute_NormalContinue(t *testing.T) {
	p := writingProgress([]int{1, 2, 3}, domain.FlowWriting)
	p.TotalChapters = 20
	missing := Route(State{Progress: p, LastCompleted: 3})
	if missing == nil || missing.Agent != "chapter_planner" || missing.Chapter != 4 {
		t.Fatalf("expected chapter_planner before writer, got %+v", missing)
	}
	got := Route(State{
		Progress: p, LastCompleted: 3, HasNextChapterPlan: true,
		NextChapterWriting: &domain.WritingProgress{
			Chapter: 4, TotalUnits: 1,
			Next: &domain.WritingUnitAssignment{Ordinal: 1, Unit: domain.WritingUnit{ID: "4-1-1"}},
		},
	})
	if got == nil || got.Agent != "writer" {
		t.Fatalf("expected writer for next chapter, got %+v", got)
	}
	if !strings.Contains(got.Task, "unit 4-1-1") {
		t.Errorf("expected unit identity, got %q", got.Task)
	}
	if got.Chapter != 4 {
		t.Errorf("expected Chapter=4, got %d", got.Chapter)
	}
}

func TestRoute_UnitsUseDistinctWriterTasksThenEngineCommit(t *testing.T) {
	p := writingProgress(nil, domain.FlowWriting)
	p.TotalChapters = 1
	first := &domain.WritingProgress{
		Chapter: 1, TotalUnits: 2,
		Next: &domain.WritingUnitAssignment{
			Ordinal: 1,
			Unit:    domain.WritingUnit{ID: "1-1-1"},
		},
	}
	inst := Route(State{Progress: p, HasNextChapterPlan: true, NextChapterWriting: first})
	if inst == nil || inst.Agent != "writer" || inst.Chapter != 1 {
		t.Fatalf("expected unit writer, got %+v", inst)
	}
	if !strings.Contains(inst.Task, "unit 1-1-1") || !strings.Contains(inst.Task, "1/2") {
		t.Fatalf("unit identity missing from task: %q", inst.Task)
	}

	second := &domain.WritingProgress{
		Chapter: 1, TotalUnits: 2, CompletedUnits: 1,
		Next: &domain.WritingUnitAssignment{
			Ordinal: 2,
			Unit:    domain.WritingUnit{ID: "1-1-2"},
		},
	}
	next := Route(State{Progress: p, HasNextChapterPlan: true, NextChapterWriting: second})
	if next == nil || next.Agent != "writer" || next.Task == inst.Task || !strings.Contains(next.Task, "unit 1-1-2") {
		t.Fatalf("next unit must have a distinct writer task: first=%+v next=%+v", inst, next)
	}

	complete := &domain.WritingProgress{Chapter: 1, TotalUnits: 2, CompletedUnits: 2, Complete: true}
	if got := Route(State{Progress: p, HasNextChapterPlan: true, NextChapterWriting: complete}); got != nil {
		t.Fatalf("completed units are committed by Engine without another LLM dispatch, got %+v", got)
	}
}

func TestRoute_NonLayeredOutlineExhaustedDispatchesArchitect(t *testing.T) {
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		CompletedChapters: []int{1, 2, 3},
		TotalChapters:     3,
	}
	got := Route(State{Progress: p, LastCompleted: 3, PlanningTier: domain.PlanningTierShort})
	if got == nil || got.Agent != "architect_short" {
		t.Fatalf("expected architect_short at outline exhaustion, got %+v", got)
	}
	for _, want := range []string{"complete_book", "revise_outline", "第 4 章"} {
		if !strings.Contains(got.Task, want) {
			t.Errorf("task missing %q: %s", want, got.Task)
		}
	}
}

func TestRoute_ArcEndNonLayeredSkipsBoundary(t *testing.T) {
	// 非 Layered 模式即使 ArcBoundary 非 nil 也不走弧末分支
	p := &domain.Progress{
		Phase:             domain.PhaseWriting,
		Flow:              domain.FlowWriting,
		Layered:           false,
		CompletedChapters: []int{10},
		TotalChapters:     20,
	}
	s := State{
		Progress:           p,
		LastCompleted:      10,
		ArcBoundary:        &storepkg.ArcBoundary{IsArcEnd: true, Volume: 1, Arc: 2},
		HasNextChapterPlan: true,
		NextChapterWriting: &domain.WritingProgress{
			Chapter: 11, TotalUnits: 1,
			Next: &domain.WritingUnitAssignment{Ordinal: 1, Unit: domain.WritingUnit{ID: "11-1-1"}},
		},
	}
	got := Route(s)
	if got == nil || got.Agent != "writer" {
		t.Fatalf("non-layered should fall through to writer, got %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// 规划期补齐:设定缺项 + 规划师可判定 → 照缺项续派同一规划师。
func TestRoute_PlanningFillDispatchesSamePlanner(t *testing.T) {
	base := State{
		Progress:          &domain.Progress{Phase: domain.PhaseOutline},
		FoundationMissing: []string{"characters", "world_rules"},
	}

	short := base
	short.PlanningTier = domain.PlanningTierShort
	if got := Route(short); got == nil || got.Agent != "architect_short" {
		t.Fatalf("short tier 应续派 architect_short,got %+v", got)
	}

	long := base
	long.PlanningTier = domain.PlanningTierLong
	got := Route(long)
	if got == nil || got.Agent != "architect_long" {
		t.Fatalf("long tier 应续派 architect_long,got %+v", got)
	}
	for _, want := range []string{"补齐基础设定", "characters", "world_rules", "save_foundation"} {
		if !contains(got.Task, want) {
			t.Errorf("补齐任务缺少 %q: %s", want, got.Task)
		}
	}

	// 首次规划未落盘任何设定(tier 空)→ 选型是语义判断,交 LLM
	unknown := base
	if got := Route(unknown); got != nil {
		t.Fatalf("tier 未知时应交 LLM 裁定,got %+v", got)
	}

	// 缺项已齐 → 无补齐指令(等 phase 推进)
	done := base
	done.PlanningTier = domain.PlanningTierLong
	done.FoundationMissing = nil
	if got := Route(done); got != nil {
		t.Fatalf("缺项已齐时不应派补齐,got %+v", got)
	}
}
