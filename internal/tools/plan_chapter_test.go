package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func planArgs(chapter int) json.RawMessage {
	b, _ := json.Marshal(map[string]any{
		"chapter":     chapter,
		"title":       "测试章",
		"goal":        "推进剧情",
		"conflict":    "外部阻力",
		"hook":        "留下悬念",
		"emotion_arc": "紧张到期待",
	})
	return b
}

func executableChapterPlan() domain.ChapterPlan {
	return domain.ChapterPlan{
		Chapter: 1, Title: "测试章", Goal: "确认线索", Conflict: "追兵逼近", Hook: "门后有人",
		OpeningState: "主角进入仓库", TargetChars: 1600,
		CreativeFreedom: []string{"对白措辞和感官细节"},
		Scenes: []domain.ScenePlan{{
			ID: "1-1", Purpose: "找到证据", Location: "旧仓库", POV: "主角",
			Characters: []string{"主角", "同伴"}, EntryState: "尚未找到证据", Conflict: "追兵逼近",
			Turn: "同伴发现夹层", ExitState: "两人取得账册", TargetChars: 1600,
			Units: []domain.WritingUnit{
				{ID: "1-1-1", TargetChars: 800, RequiredBeats: []string{"搜索仓库"}, ForbiddenMoves: []string{"离开仓库"}, EndAnchor: "听见脚步", Transition: "从推门进入开始"},
				{ID: "1-1-2", TargetChars: 800, RequiredBeats: []string{"找到夹层", "取得账册"}, EndAnchor: "门外影子停下", Transition: "承接逼近的脚步声"},
			},
		}},
	}
}

func TestValidateWritingPlanRequiresExecutableSceneCards(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*domain.ChapterPlan)
		want   string
	}{
		{name: "valid", mutate: func(*domain.ChapterPlan) {}},
		{name: "missing location", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].Location = "" }, want: "location"},
		{name: "missing creative freedom", mutate: func(p *domain.ChapterPlan) { p.CreativeFreedom = nil }, want: "creative_freedom"},
		{name: "blank character", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].Characters[0] = " " }, want: "characters"},
		{name: "blank beat", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].Units[0].RequiredBeats[0] = " " }, want: "required_beats"},
		{name: "scene budget mismatch", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].TargetChars = 2600 }, want: "approximately match"},
		{name: "chapter budget mismatch", mutate: func(p *domain.ChapterPlan) { p.TargetChars = 2600 }, want: "approximately match"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := executableChapterPlan()
			tt.mutate(&plan)
			err := validateWritingPlan(plan)
			if tt.want == "" && err != nil {
				t.Fatalf("valid plan rejected: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateWritingPlanAppliesEightKWriterBudget(t *testing.T) {
	budget := WriterPlanningBudgetForContext(8192)
	tests := []struct {
		name   string
		mutate func(*domain.ChapterPlan)
		want   string
	}{
		{name: "valid", mutate: func(*domain.ChapterPlan) {}},
		{name: "low density unit valid", mutate: func(p *domain.ChapterPlan) {
			p.Scenes[0].Units[0].TargetChars = 200
			p.Scenes[0].Units[1].TargetChars = 300
			p.Scenes[0].TargetChars = 500
			p.TargetChars = 500
		}},
		{name: "unit too short", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].Units[0].TargetChars = 199 }, want: "200-1000"},
		{name: "unit too long", mutate: func(p *domain.ChapterPlan) { p.Scenes[0].Units[0].TargetChars = 1001 }, want: "200-1000"},
		{name: "too many beats", mutate: func(p *domain.ChapterPlan) {
			p.Scenes[0].Units[0].RequiredBeats = []string{"一", "二", "三", "四", "五", "六"}
		}, want: "at most 5 items"},
		{name: "beat too verbose", mutate: func(p *domain.ChapterPlan) {
			p.Scenes[0].Units[0].RequiredBeats[0] = strings.Repeat("长", 73)
		}, want: "at most 72 characters"},
		{name: "scene field too verbose", mutate: func(p *domain.ChapterPlan) {
			p.Scenes[0].EntryState = strings.Repeat("长", 121)
		}, want: "entry_state must be at most 120"},
		{name: "too many chapter forbidden moves", mutate: func(p *domain.ChapterPlan) {
			p.Contract.ForbiddenMoves = []string{"一", "二", "三", "四", "五"}
		}, want: "forbidden_moves must contain at most 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := executableChapterPlan()
			tt.mutate(&plan)
			err := validateWritingPlanWithBudget(plan, budget)
			if tt.want == "" && err != nil {
				t.Fatalf("valid 8K plan rejected: %v", err)
			}
			if tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)) {
				t.Fatalf("expected error containing %q, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateWritingPlanKeepsDynamicRangeForLargeWindow(t *testing.T) {
	plan := executableChapterPlan()
	for i := range plan.Scenes[0].Units {
		plan.Scenes[0].Units[i].TargetChars = 1000
	}
	plan.Scenes[0].TargetChars = 2000
	plan.TargetChars = 2000
	if err := validateWritingPlanWithBudget(plan, WriterPlanningBudgetForContext(32768)); err != nil {
		t.Fatalf("large Writer window should accept the shared 200-1000 range: %v", err)
	}
	plan.Scenes[0].Units[0].TargetChars = 1001
	if err := validateWritingPlanWithBudget(plan, WriterPlanningBudgetForContext(32768)); err == nil || !strings.Contains(err.Error(), "200-1000") {
		t.Fatalf("large Writer window must not restore the old 1200-character allowance: %v", err)
	}
}

func TestPlanChapterDuplicateReturnsExistingBeforeRevalidating(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("test", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveChapterPlan(executableChapterPlan()); err != nil {
		t.Fatal(err)
	}

	malformed, _ := json.Marshal(map[string]any{"chapter": 1, "scenes": []any{map[string]any{"id": "broken"}}})
	result, err := NewPlanChapterTool(st).Execute(context.Background(), malformed)
	if err != nil {
		t.Fatalf("duplicate recovery call should return existing plan: %v", err)
	}
	if !strings.Contains(string(result), `"skipped":true`) || !strings.Contains(string(result), `"planned":true`) {
		t.Fatalf("unexpected duplicate result: %s", result)
	}
	if st.Checkpoints.LatestByStep(domain.ChapterScope(1), "plan") == nil {
		t.Fatal("duplicate recovery call should repair the missing plan checkpoint")
	}
}

func TestPlanChapterRejectsUnexpandedLayeredChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 5); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}, {
			Index:             2,
			Title:             "第二弧",
			EstimatedChapters: 3,
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), planArgs(3)); err == nil || !strings.Contains(err.Error(), "expand_arc") {
		t.Fatalf("expected unexpanded chapter rejection, got %v", err)
	}
	if p, _ := st.Progress.Load(); p != nil && p.InProgressChapter == 3 {
		t.Fatal("unexpanded chapter should not become in-progress")
	}
}

func TestPlanChapterAllowsExpandedLayeredChapter(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := st.Progress.Init("test", 2); err != nil {
		t.Fatalf("Progress.Init: %v", err)
	}
	if err := st.Outline.SaveLayeredOutline([]domain.VolumeOutline{{
		Index: 1,
		Title: "第一卷",
		Arcs: []domain.ArcOutline{{
			Index: 1,
			Title: "第一弧",
			Chapters: []domain.OutlineEntry{
				{Chapter: 1, Title: "一"},
				{Chapter: 2, Title: "二"},
			},
		}},
	}}); err != nil {
		t.Fatalf("SaveLayeredOutline: %v", err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatalf("UpdatePhase: %v", err)
	}
	if err := st.Progress.SetLayered(true); err != nil {
		t.Fatalf("SetLayered: %v", err)
	}

	tool := NewPlanChapterTool(st)
	if _, err := tool.Execute(context.Background(), planArgs(2)); err != nil {
		t.Fatalf("expected expanded chapter to plan, got %v", err)
	}
}
