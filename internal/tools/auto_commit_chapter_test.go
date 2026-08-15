package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/store"
)

func seededAutoCommitStore(t *testing.T) (*store.Store, domain.ChapterPlan) {
	t.Helper()
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.Init("自动提交测试", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.UpdatePhase(domain.PhaseWriting); err != nil {
		t.Fatal(err)
	}
	if err := st.Outline.SaveOutline([]domain.OutlineEntry{{Chapter: 1, Title: "暗门", CoreEvent: "发现暗门"}}); err != nil {
		t.Fatal(err)
	}
	plan := domain.ChapterPlan{
		Chapter: 1, Title: "暗门", Goal: "主角确认走廊尽头藏有入口", Hook: "门后传来脚步声",
		Scenes: []domain.ScenePlan{
			{ID: "1-1", Purpose: "寻找入口", Turn: "发现墙上的暗门", Characters: []string{"主角", "向导"}, Units: []domain.WritingUnit{{ID: "1-1-1"}}},
			{ID: "1-2", Purpose: "确认危险", Turn: "向导认出门后的脚步", Characters: []string{"向导", "主角"}, Units: []domain.WritingUnit{{ID: "1-2-1"}}},
		},
	}
	if err := st.Drafts.SaveChapterPlan(plan); err != nil {
		t.Fatal(err)
	}
	return st, plan
}

func TestAutoCommitPlannedChapterCommitsCompletedUnits(t *testing.T) {
	st, plan := seededAutoCommitStore(t)
	first := "主角沿着墙面摸索，终于在灰尘下找到一道笔直的缝隙。"
	second := "向导贴近暗门辨认片刻，门后随即响起逐渐逼近的脚步声。"
	if _, _, err := st.Drafts.SaveWritingUnit(1, 1, 2, 1, first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Drafts.SaveWritingUnit(1, 2, 2, 2, second); err != nil {
		t.Fatal(err)
	}
	if err := st.Drafts.SaveDraft(1, "旧 Finalizer 覆盖过的正文，不应进入终稿。"); err != nil {
		t.Fatal(err)
	}

	raw, err := AutoCommitPlannedChapter(context.Background(), st, NewStyleStatsIndex(st), 1)
	if err != nil {
		t.Fatalf("auto commit: %v", err)
	}
	if strings.Contains(string(raw), `"review_required":true`) {
		t.Fatalf("automatic commit must not request a review: %s", raw)
	}
	content, err := st.Drafts.LoadChapterText(1)
	if err != nil || content != first+"\n\n"+second {
		t.Fatalf("final chapter mismatch: %q err=%v", content, err)
	}
	summary, err := st.Summaries.LoadSummary(1)
	if err != nil || summary == nil {
		t.Fatalf("load summary: %+v err=%v", summary, err)
	}
	if summary.Title != plan.Title || !strings.Contains(summary.Summary, plan.Goal) || len(summary.KeyEvents) != 2 {
		t.Fatalf("summary was not projected from plan: %+v", summary)
	}
	if len(summary.Characters) != 2 || summary.Characters[0] != "主角" || summary.Characters[1] != "向导" {
		t.Fatalf("characters must be stable and deduplicated: %v", summary.Characters)
	}
	progress, err := st.Progress.Load()
	if err != nil || progress == nil || len(progress.CompletedChapters) != 1 || progress.CompletedChapters[0] != 1 {
		t.Fatalf("chapter was not marked complete: %+v err=%v", progress, err)
	}
	if progress.Phase != domain.PhaseComplete {
		t.Fatalf("single-chapter book should be complete, phase=%s", progress.Phase)
	}
	if cp := st.Checkpoints.LatestByStep(domain.ChapterScope(1), "commit"); cp == nil {
		t.Fatal("commit checkpoint missing")
	}
}

func TestAutoCommitPlannedChapterInterleavesUnitImages(t *testing.T) {
	st, _ := seededAutoCommitStore(t)
	first := "第一段正文。"
	second := "第二段正文。"
	if _, _, err := st.Drafts.SaveWritingUnit(1, 1, 2, 1, first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.Drafts.SaveWritingUnit(1, 2, 2, 2, second); err != nil {
		t.Fatal(err)
	}
	imageDir := filepath.Join(st.Dir(), "drafts", "01.units")
	if err := os.WriteFile(filepath.Join(imageDir, "001.png"), []byte("image-one"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(imageDir, "002.png"), []byte("image-two"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := AutoCommitPlannedChapter(context.Background(), st, NewStyleStatsIndex(st), 1); err != nil {
		t.Fatalf("auto commit: %v", err)
	}
	content, err := st.Drafts.LoadChapterText(1)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		first,
		"![第 1 章 Unit 1 插图](../drafts/01.units/001.png)",
		second,
		"![第 1 章 Unit 2 插图](../drafts/01.units/002.png)",
	}, "\n\n")
	if content != want {
		t.Fatalf("rich chapter mismatch:\n%s\nwant:\n%s", content, want)
	}
}

func TestAutoCommitPlannedChapterRejectsIncompleteUnits(t *testing.T) {
	st, _ := seededAutoCommitStore(t)
	if _, _, err := st.Drafts.SaveWritingUnit(1, 1, 2, 1, "只完成了第一个片段。"); err != nil {
		t.Fatal(err)
	}
	if _, err := AutoCommitPlannedChapter(context.Background(), st, NewStyleStatsIndex(st), 1); err == nil || !strings.Contains(err.Error(), "not complete") {
		t.Fatalf("incomplete units must be rejected, got %v", err)
	}
	if text, err := st.Drafts.LoadChapterText(1); err != nil || text != "" {
		t.Fatalf("incomplete chapter must not create a final file: %q err=%v", text, err)
	}
}
