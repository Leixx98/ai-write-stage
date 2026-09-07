package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/Leixx98/ai-write-stage/internal/domain"
	"github.com/Leixx98/ai-write-stage/internal/errs"
	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore/schema"
)

// WriteChapterUnitTool 把本地模型的一次正文输出保存为独立幂等工件，再按计划顺序
// 重建 draft.md。模型不能跳写或覆盖别的 unit。
type WriteChapterUnitTool struct {
	store *store.Store
}

func NewWriteChapterUnitTool(store *store.Store) *WriteChapterUnitTool {
	return &WriteChapterUnitTool{store: store}
}

func (t *WriteChapterUnitTool) Name() string  { return "write_chapter_unit" }
func (t *WriteChapterUnitTool) Label() string { return "写作片段" }
func (t *WriteChapterUnitTool) Description() string {
	return "按章节计划写入当前待完成的一个正文片段。每次只提交 writing_progress.next 指定的 unit；工具独立落盘并自动拼装草稿"
}
func (t *WriteChapterUnitTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *WriteChapterUnitTool) ConcurrencySafe(json.RawMessage) bool { return false }
func (t *WriteChapterUnitTool) StrictSchema() bool                   { return true }

func (t *WriteChapterUnitTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("unit_id", schema.String("必须等于 writing_progress.next.unit.id")).Required(),
		schema.Property("content", schema.String("本片段纯正文；不重复章节标题，不输出解释或小结")).Required(),
	)
}

func (t *WriteChapterUnitTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Chapter int    `json:"chapter"`
		UnitID  string `json:"unit_id"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if input.Chapter <= 0 || strings.TrimSpace(input.UnitID) == "" || strings.TrimSpace(input.Content) == "" {
		return nil, fmt.Errorf("chapter, unit_id and content are required: %w", errs.ErrToolArgs)
	}
	completed, err := t.store.Progress.IsChapterCompleted(input.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if completed {
		return nil, fmt.Errorf("write_chapter_unit only writes new chapters; use edit_chapter/draft_chapter for rewrites: %w", errs.ErrToolPrecondition)
	}
	if err := t.store.Progress.ValidateChapterWork(input.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, input.Chapter); err != nil {
		return nil, err
	}

	progress, err := t.store.Drafts.LoadWritingProgress(input.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load writing progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil || progress.TotalUnits == 0 {
		return nil, fmt.Errorf("chapter %d has no scene/unit plan; use legacy draft_chapter only for an existing legacy plan: %w", input.Chapter, errs.ErrToolPrecondition)
	}
	plan, err := t.store.Drafts.LoadChapterPlan(input.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter plan: %w: %w", errs.ErrStoreRead, err)
	}
	if plan == nil {
		return nil, fmt.Errorf("chapter %d plan not found: %w", input.Chapter, errs.ErrToolPrecondition)
	}
	assignments := plan.WritingUnits()
	ordinal := 0
	for _, assignment := range assignments {
		if assignment.Unit.ID == input.UnitID {
			ordinal = assignment.Ordinal
			break
		}
	}
	if ordinal == 0 {
		return nil, fmt.Errorf("unknown unit_id %q in chapter plan: %w", input.UnitID, errs.ErrToolArgs)
	}
	completedRetry := ordinal <= progress.CompletedUnits
	unitChars := utf8.RuneCountInString(strings.TrimSpace(input.Content))
	target := assignments[ordinal-1].Unit.TargetChars
	minimum := (target*3 + 9) / 10
	if !completedRetry && target > 0 && unitChars < minimum {
		return nil, fmt.Errorf("本片段正文过短：计划目标约 %d 字，至少需要 %d 字，当前只有 %d 字；请保留现有正文并补全 required_beats 与 end_anchor 后重新调用 write_chapter_unit: %w",
			target, minimum, unitChars, errs.ErrToolArgs)
	}
	if !completedRetry && (progress.Next == nil || input.UnitID != progress.Next.Unit.ID) {
		want := "<none>"
		if progress.Next != nil {
			want = progress.Next.Unit.ID
		}
		return nil, fmt.Errorf("unit_id must be current next unit %q, got %q: %w", want, input.UnitID, errs.ErrToolConflict)
	}
	if err := t.store.Progress.StartChapter(input.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w: %w", errs.ErrStoreWrite, err)
	}

	rebuildThrough := progress.CompletedUnits
	if !completedRetry {
		rebuildThrough++
	}
	alreadyExists, assembled, err := t.store.Drafts.SaveWritingUnit(
		input.Chapter, ordinal, len(assignments), rebuildThrough, strings.TrimSpace(input.Content),
	)
	if err != nil {
		return nil, fmt.Errorf("save writing unit: %w: %w", errs.ErrStoreWrite, err)
	}
	artifact := fmt.Sprintf("drafts/%02d.units/%03d.md", input.Chapter, ordinal)
	if _, err := t.store.Checkpoints.AppendArtifact(domain.ChapterScope(input.Chapter), "draft_unit", artifact); err != nil {
		return nil, fmt.Errorf("checkpoint writing unit: %w", err)
	}

	next, err := t.store.Drafts.LoadWritingProgress(input.Chapter)
	if err != nil {
		return nil, fmt.Errorf("reload writing progress: %w: %w", errs.ErrStoreRead, err)
	}
	if next.Complete {
		if _, err := t.store.Checkpoints.AppendArtifact(domain.ChapterScope(input.Chapter), "draft", fmt.Sprintf("drafts/%02d.draft.md", input.Chapter)); err != nil {
			return nil, fmt.Errorf("checkpoint completed draft: %w", err)
		}
	}

	result := map[string]any{
		"written":         true,
		"chapter":         input.Chapter,
		"unit_id":         input.UnitID,
		"already_existed": alreadyExists,
		"unit_chars":      utf8.RuneCountInString(input.Content),
		"draft_chars":     utf8.RuneCountInString(assembled),
		"completed_units": next.CompletedUnits,
		"total_units":     next.TotalUnits,
		"complete":        next.Complete,
	}
	if next.Next != nil {
		result["next_unit_id"] = next.Next.Unit.ID
	}
	if target > 0 && unitChars > target*8/5 {
		result["length_warning"] = fmt.Sprintf("本片段目标约 %d 字，实际 %d 字；后续片段不要为机械补字破坏节奏", target, unitChars)
	}
	if next.Complete {
		result["next_step"] = "本轮到此结束；Engine 将直接合并并自动提交本章，不再调用任何审核或收尾模型"
	} else {
		result["next_step"] = "本轮到此结束；Engine 将为 next_unit_id 启动全新的 Writer 会话"
	}
	return json.Marshal(result)
}
