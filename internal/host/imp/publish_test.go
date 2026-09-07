package imp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Leixx98/ai-write-stage/internal/domain"
	"github.com/Leixx98/ai-write-stage/internal/store"
)

// spyCommitter 记录 Execute 调用次数，供发布幂等/恢复路径测试。
type spyCommitter struct{ calls int }

func (s *spyCommitter) Execute(context.Context, json.RawMessage) (json.RawMessage, error) {
	s.calls++
	return json.RawMessage(`{}`), nil
}

// TestPublishChapterHandlesStalePendingCommit 守护发布崩溃窗口的恢复：崩溃落在
// MarkChapterComplete 与 ClearPendingCommit 之间会残留指向本章的 pending_commit。
// 已完成章若直接跳过会绕开 commit 工具的清理分支，下一章 Execute 以 ErrToolConflict
// 拒绝，导入每次重跑死在同一处——命中残留时必须仍走一次工具幂等路径。
func TestPublishChapterHandlesStalePendingCommit(t *testing.T) {
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
	if err := st.Progress.StartChapter(1); err != nil {
		t.Fatal(err)
	}
	if err := st.Progress.MarkChapterComplete(1, 100, "mystery", "quest"); err != nil {
		t.Fatal(err)
	}
	f := ImportedChapterFacts{Chapter: 1, Summary: "s", CoreEvent: "c", HookType: "mystery", DominantStrand: "quest"}

	// 无残留：已完成章零成本跳过，不触发 commit。
	spy := &spyCommitter{}
	if err := publishChapter(context.Background(), st, spy, 1, "正文", f); err != nil {
		t.Fatalf("已完成章应幂等跳过：%v", err)
	}
	if spy.calls != 0 {
		t.Fatalf("无残留不应调用 commit，得 %d 次", spy.calls)
	}

	// 残留指向本章：必须走一次 commit 幂等路径完成清理。
	if err := st.Signals.SavePendingCommit(domain.PendingCommit{Chapter: 1}); err != nil {
		t.Fatal(err)
	}
	if err := publishChapter(context.Background(), st, spy, 1, "正文", f); err != nil {
		t.Fatalf("残留清理路径不应失败：%v", err)
	}
	if spy.calls != 1 {
		t.Fatalf("命中残留应恰好调用 commit 一次，得 %d 次", spy.calls)
	}
}

func TestCommitArgsOmitsEmptyDeepFields(t *testing.T) {
	light := ImportedChapterFacts{Chapter: 1, Title: "一", Summary: "s", CoreEvent: "c", HookType: "mystery", DominantStrand: "quest"}
	args := commitArgs(1, light)
	for _, key := range []string{"timeline_events", "foreshadow_updates", "relationship_changes", "state_changes"} {
		if _, ok := args[key]; ok {
			t.Fatalf("轻事实不应提交 %s", key)
		}
	}
	deep := light
	deep.TimelineEvents = []domain.TimelineEvent{{Chapter: 1, Time: "夜", Event: "走"}}
	if _, ok := commitArgs(1, deep)["timeline_events"]; !ok {
		t.Fatal("近窗深字段应随 commit 写出")
	}
}

func TestMaterializeDraftsWritesAllChapters(t *testing.T) {
	st := store.NewStore(t.TempDir())
	if err := st.Init(); err != nil {
		t.Fatal(err)
	}
	norm, seg := analyzeFixture(t, 2)
	if err := materializeDrafts(st, norm, seg); err != nil {
		t.Fatal(err)
	}
	for i, ch := range seg.Chapters {
		got, err := st.Drafts.LoadDraft(ch.Number)
		if err != nil {
			t.Fatalf("第 %d 章草稿：%v", ch.Number, err)
		}
		if got != seg.Content(norm, i) {
			t.Fatalf("第 %d 章草稿应等于切分正文", ch.Number)
		}
	}
}
