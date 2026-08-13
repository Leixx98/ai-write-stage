package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func seededUnitStore(t *testing.T) *store.Store {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("unit-test", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "第一章", CoreEvent: "推进"}}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1, Title: "第一章", Goal: "推进", Conflict: "受阻", Hook: "发现",
		OpeningState: "主角进入后堂", TargetChars: 1600,
		Scenes: []domain.ScenePlan{{
			ID: "1-1", Purpose: "发现线索", Location: "后堂", POV: "主角",
			Characters: []string{"主角"}, EntryState: "尚不知情", Conflict: "账本被藏",
			Turn: "找到暗页", ExitState: "取得证据", TargetChars: 1600,
			Units: []domain.WritingUnit{
				{ID: "1-1-1", TargetChars: 800, RequiredBeats: []string{"寻找账本"}, EndAnchor: "听见脚步"},
				{ID: "1-1-2", TargetChars: 800, RequiredBeats: []string{"藏起暗页"}, EndAnchor: "离开后堂"},
			},
		}},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	return st
}

func unitArgs(chapter int, id, content string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"chapter": chapter, "unit_id": id, "content": content})
	return raw
}

func fullUnitText(label string) string {
	return strings.Repeat(label+"推进情节并保持叙事连贯。", 60)
}

func TestWriteChapterUnitBuildsDraftInPlanOrder(t *testing.T) {
	st := seededUnitStore(t)
	tool := NewWriteChapterUnitTool(st)
	first := fullUnitText("第一片段")
	second := fullUnitText("第二片段")

	firstResult, err := tool.Execute(context.Background(), unitArgs(1, "1-1-1", first))
	if err != nil {
		t.Fatalf("write first unit: %v", err)
	}
	if strings.Contains(string(firstResult), `"progress"`) || strings.Contains(string(firstResult), "previous_tail") || strings.Contains(string(firstResult), "required_beats") {
		t.Fatalf("unit result must stay compact and must not return the next execution card: %s", firstResult)
	}
	if !strings.Contains(string(firstResult), `"next_unit_id":"1-1-2"`) || !strings.Contains(string(firstResult), `"completed_units":1`) {
		t.Fatalf("unit result lacks compact progress facts: %s", firstResult)
	}
	progress, err := st.Drafts.LoadWritingProgress(1)
	if err != nil || progress == nil || progress.CompletedUnits != 1 || progress.Next == nil || progress.Next.Unit.ID != "1-1-2" {
		t.Fatalf("progress after first unit: %+v err=%v", progress, err)
	}

	// checkpoint 写入失败后的同 unit 重试不得覆盖或重复追加已冻结正文。
	if _, err := tool.Execute(context.Background(), unitArgs(1, "1-1-1", "不同的重试正文。")); err != nil {
		t.Fatalf("retry first unit: %v", err)
	}
	orphanPath := filepath.Join(st.Dir(), "drafts", "01.units", "003.md")
	if err := os.WriteFile(orphanPath, []byte("计划外遗留正文。"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Execute(context.Background(), unitArgs(1, "1-1-2", second)); err != nil {
		t.Fatalf("write second unit: %v", err)
	}

	draft, err := st.Drafts.LoadDraft(1)
	if err != nil {
		t.Fatal(err)
	}
	if draft != first+"\n\n"+second || strings.Contains(draft, "不同的重试正文") || strings.Contains(draft, "计划外遗留正文") {
		t.Fatalf("assembled draft mismatch: %q", draft)
	}
	progress, _ = st.Drafts.LoadWritingProgress(1)
	if progress == nil || !progress.Complete || progress.CompletedUnits != 2 {
		t.Fatalf("final progress: %+v", progress)
	}
}

func TestWriterBoundToolsForceCurrentChapterAndUnit(t *testing.T) {
	st := seededUnitStore(t)
	contextTool := NewWriterContextTool(
		newTestContextTool(st, References{}, "default"),
		st,
		func() int { return 8192 },
	)

	// A weak model may copy the old schema or explicitly request full context.
	// The Writer wrapper must ignore both and return only chapter 1 / unit 1-1-1.
	raw, err := contextTool.Execute(context.Background(), json.RawMessage(`{"chapter":999,"context_mode":"full"}`))
	if err != nil {
		t.Fatalf("writer context: %v", err)
	}
	if len(raw) > writerUnitContextBudgetBytes(8192) {
		t.Fatalf("8K writer packet too large: %d bytes", len(raw))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("decode writer context: %v", err)
	}
	if payload["context_mode"] != "writer_unit" {
		t.Fatalf("context mode was not forced: %#v", payload["context_mode"])
	}
	if _, leaked := payload["outline"]; leaked {
		t.Fatal("writer packet leaked full-book outline")
	}
	working := payload["working_memory"].(map[string]any)
	execution := working["execution"].(map[string]any)
	current := execution["current_unit"].(map[string]any)
	if current["unit_id"] != "1-1-1" || execution["phase"] != "write_one_unit" {
		t.Fatalf("unexpected bound execution: %#v", execution)
	}

	readTool := NewWriterReadChapterTool(NewReadChapterTool(st), st)
	if _, err := readTool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"draft"}`)); err == nil || !strings.Contains(err.Error(), "禁止回读") {
		t.Fatalf("pending unit must block current whole-draft read, got %v", err)
	}

	writeTool := NewWriterWriteChapterUnitTool(st)
	firstArgs := unitArgs(999, "future", fullUnitText("第一片段"))
	if _, err := writeTool.Execute(context.Background(), firstArgs); err != nil {
		t.Fatalf("bound first unit: %v", err)
	}
	progress, _ := st.Drafts.LoadWritingProgress(1)
	if progress.CompletedUnits != 1 || progress.Next == nil || progress.Next.Unit.ID != "1-1-2" {
		t.Fatalf("host did not bind first unit: %+v", progress)
	}

	// Repeating the same tool call is idempotent and must not consume unit 2.
	if _, err := writeTool.Execute(context.Background(), firstArgs); err != nil {
		t.Fatalf("bound retry: %v", err)
	}
	progress, _ = st.Drafts.LoadWritingProgress(1)
	if progress.CompletedUnits != 1 || progress.Next == nil || progress.Next.Unit.ID != "1-1-2" {
		t.Fatalf("retry advanced to a future unit: %+v", progress)
	}

	if _, err := writeTool.Execute(context.Background(), unitArgs(0, "", fullUnitText("第二片段"))); err != nil {
		t.Fatalf("bound second unit: %v", err)
	}
	progress, _ = st.Drafts.LoadWritingProgress(1)
	if !progress.Complete || progress.CompletedUnits != 2 {
		t.Fatalf("second unit was not completed: %+v", progress)
	}
	if raw, err := readTool.Execute(context.Background(), json.RawMessage(`{"chapter":1,"source":"draft"}`)); err != nil || !strings.Contains(string(raw), "第一片段推进") {
		t.Fatalf("completed units should release draft read for finalization: raw=%s err=%v", raw, err)
	}
}

func TestWriteChapterUnitRejectsSkippingAhead(t *testing.T) {
	st := seededUnitStore(t)
	_, err := NewWriteChapterUnitTool(st).Execute(context.Background(), unitArgs(1, "1-1-2", fullUnitText("跳写正文")))
	if err == nil || !strings.Contains(err.Error(), "current next unit") {
		t.Fatalf("expected skip rejection, got %v", err)
	}
}

func TestWriteChapterUnitUsesThirtyPercentMinimum(t *testing.T) {
	t.Run("rejects below thirty percent", func(t *testing.T) {
		st := seededUnitStore(t)
		content := strings.Repeat("字", 239)
		_, err := NewWriteChapterUnitTool(st).Execute(context.Background(), unitArgs(1, "1-1-1", content))
		if err == nil || !strings.Contains(err.Error(), "至少需要 240 字") {
			t.Fatalf("239/800 characters must be rejected by the 30%% floor, got %v", err)
		}
	})

	t.Run("accepts exactly thirty percent", func(t *testing.T) {
		st := seededUnitStore(t)
		content := strings.Repeat("字", 240)
		if _, err := NewWriteChapterUnitTool(st).Execute(context.Background(), unitArgs(1, "1-1-1", content)); err != nil {
			t.Fatalf("240/800 characters must satisfy the 30%% floor: %v", err)
		}
	})
}

func TestIncompleteWritingUnitsBlockWholeChapterTools(t *testing.T) {
	st := seededUnitStore(t)
	ctx := context.Background()

	draftArgs, _ := json.Marshal(map[string]any{"chapter": 1, "mode": "write", "content": "绕过片段计划的整章正文"})
	if _, err := NewDraftChapterTool(st).Execute(ctx, draftArgs); err == nil || !strings.Contains(err.Error(), "writing unit 未完成") {
		t.Fatalf("draft_chapter should reject incomplete units, got %v", err)
	}
	if _, err := NewCheckConsistencyTool(st).Execute(ctx, json.RawMessage(`{"chapter":1}`)); err == nil || !strings.Contains(err.Error(), "writing units incomplete") {
		t.Fatalf("check_consistency should reject incomplete units, got %v", err)
	}
	if _, err := newTestCommitChapterTool(st).Execute(ctx, json.RawMessage(`{"chapter":1}`)); err == nil || !strings.Contains(err.Error(), "片段尚未完成") {
		t.Fatalf("commit_chapter should reject incomplete units, got %v", err)
	}
}
