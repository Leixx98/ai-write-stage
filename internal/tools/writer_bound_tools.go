package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// WriterContextTool binds novel_context to the chapter selected by the host.
// The local model cannot request the full-book or planner context by mistake.
type WriterContextTool struct {
	base          *ContextTool
	store         *store.Store
	contextWindow func() int
}

func NewWriterContextTool(base *ContextTool, st *store.Store, contextWindow func() int) *WriterContextTool {
	return &WriterContextTool{base: base, store: st, contextWindow: contextWindow}
}

func (t *WriterContextTool) Name() string  { return "novel_context" }
func (t *WriterContextTool) Label() string { return "加载当前写作单元" }
func (t *WriterContextTool) Description() string {
	return "加载宿主已确定的当前章节和当前 writing unit。无需参数；章节号、上下文档位和裁剪预算均由程序强制绑定"
}

// Deliberately not StrictSchema: older/smaller models may still emit the old
// chapter/context_mode fields. Local validation accepts them and Execute
// ignores them, preserving the host binding instead of wasting a retry.
func (t *WriterContextTool) Schema() map[string]any               { return schema.Object() }
func (t *WriterContextTool) ReadOnly(json.RawMessage) bool        { return true }
func (t *WriterContextTool) ConcurrencySafe(json.RawMessage) bool { return true }

func (t *WriterContextTool) Execute(ctx context.Context, _ json.RawMessage) (json.RawMessage, error) {
	chapter, err := resolveWriterChapter(t.store)
	if err != nil {
		return nil, err
	}
	window := 0
	if t.contextWindow != nil {
		window = t.contextWindow()
	}
	return t.base.execute(ctx, chapterContextArgs(chapter, "writer_unit"), writerUnitContextBudgetBytes(window))
}

// WriterReadChapterTool preserves read access for legacy chapter rewrites, but
// blocks the expensive current-draft read while a bounded writing unit is
// pending. The projected novel_context packet already contains previous_tail.
type WriterReadChapterTool struct {
	base  *ReadChapterTool
	store *store.Store
}

func NewWriterReadChapterTool(base *ReadChapterTool, st *store.Store) *WriterReadChapterTool {
	return &WriterReadChapterTool{base: base, store: st}
}

func (t *WriterReadChapterTool) Name() string  { return t.base.Name() }
func (t *WriterReadChapterTool) Label() string { return t.base.Label() }
func (t *WriterReadChapterTool) Description() string {
	return "读取已提交章节或返工目标原文；逐 writing unit 写新章时禁止回读当前整章草稿，应使用 novel_context 返回的 previous_tail"
}
func (t *WriterReadChapterTool) Schema() map[string]any { return t.base.Schema() }
func (t *WriterReadChapterTool) ReadOnly(args json.RawMessage) bool {
	return t.base.ReadOnly(args)
}
func (t *WriterReadChapterTool) ConcurrencySafe(args json.RawMessage) bool {
	return t.base.ConcurrencySafe(args)
}

func (t *WriterReadChapterTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	var input struct {
		Chapter   int    `json:"chapter"`
		From      int    `json:"from"`
		To        int    `json:"to"`
		Source    string `json:"source"`
		Character string `json:"character"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if input.Source == "draft" && input.Character == "" {
		chapter, err := resolveWriterChapter(t.store)
		if err != nil {
			return nil, err
		}
		progress, err := t.store.Drafts.LoadWritingProgress(chapter)
		if err != nil {
			return nil, fmt.Errorf("load writing progress: %w: %w", errs.ErrStoreRead, err)
		}
		pendingUnit := progress != nil && progress.TotalUnits > 0 && !progress.Complete && progress.Next != nil
		readsCurrent := input.Chapter == chapter || (input.From > 0 && input.To >= input.From && input.From <= chapter && chapter <= input.To)
		if pendingUnit && readsCurrent {
			return nil, fmt.Errorf("当前为 write_one_unit（%s，%d/%d），禁止回读第 %d 章整章草稿；请只使用 novel_context.previous_tail 并完成 current_unit: %w",
				progress.Next.Unit.ID, progress.Next.Ordinal, progress.TotalUnits, chapter, errs.ErrToolPrecondition)
		}
	}
	return t.base.Execute(ctx, args)
}

// WriterWriteChapterUnitTool only accepts prose. The host injects the current
// chapter and unit id from persistent progress, so a small model cannot skip,
// repeat, or write a future unit through malformed arguments.
type WriterWriteChapterUnitTool struct {
	store *store.Store
	base  *WriteChapterUnitTool

	mu          sync.Mutex
	lastChapter int
	lastUnitID  string
	lastContent string
}

func NewWriterWriteChapterUnitTool(st *store.Store) *WriterWriteChapterUnitTool {
	return &WriterWriteChapterUnitTool{store: st, base: NewWriteChapterUnitTool(st)}
}

func (t *WriterWriteChapterUnitTool) Name() string  { return "write_chapter_unit" }
func (t *WriterWriteChapterUnitTool) Label() string { return "写作当前片段" }
func (t *WriterWriteChapterUnitTool) Description() string {
	return "保存当前 writing unit 的纯小说正文。只传 content；章节号和 unit_id 由程序从持久化计划中注入"
}
func (t *WriterWriteChapterUnitTool) Schema() map[string]any {
	return schema.Object(
		schema.Property("content", schema.String("当前片段纯正文；不带章节标题、解释、分析或小结")).Required(),
	)
}

// Deliberately not StrictSchema for the same compatibility reason as
// WriterContextTool. Extra chapter/unit_id fields are ignored and replaced.
func (t *WriterWriteChapterUnitTool) ReadOnly(json.RawMessage) bool        { return false }
func (t *WriterWriteChapterUnitTool) ConcurrencySafe(json.RawMessage) bool { return false }

func (t *WriterWriteChapterUnitTool) Execute(ctx context.Context, args json.RawMessage) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var input struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(args, &input); err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	input.Content = strings.TrimSpace(input.Content)
	if input.Content == "" {
		return nil, fmt.Errorf("content is required: %w", errs.ErrToolArgs)
	}

	chapter, err := resolveWriterChapter(t.store)
	if err != nil {
		return nil, err
	}
	unitID := ""
	if chapter == t.lastChapter && input.Content == t.lastContent {
		// Agent/tool retries remain idempotent after the first call advanced the
		// persistent next pointer. Reuse the previously bound unit in that case.
		unitID = t.lastUnitID
	}
	if unitID == "" {
		progress, err := t.store.Drafts.LoadWritingProgress(chapter)
		if err != nil {
			return nil, fmt.Errorf("load writing progress: %w: %w", errs.ErrStoreRead, err)
		}
		if progress == nil || progress.Next == nil {
			return nil, fmt.Errorf("chapter %d has no pending writing unit: %w", chapter, errs.ErrToolPrecondition)
		}
		unitID = progress.Next.Unit.ID
	}

	bound, err := json.Marshal(map[string]any{
		"chapter": chapter,
		"unit_id": unitID,
		"content": input.Content,
	})
	if err != nil {
		return nil, fmt.Errorf("bind writing unit args: %w", err)
	}
	result, err := t.base.Execute(ctx, bound)
	if err == nil {
		t.lastChapter = chapter
		t.lastUnitID = unitID
		t.lastContent = input.Content
	}
	return result, err
}

func resolveWriterChapter(st *store.Store) (int, error) {
	if st == nil {
		return 0, fmt.Errorf("writer store is nil: %w", errs.ErrToolPrecondition)
	}
	progress, err := st.Progress.Load()
	if err != nil {
		return 0, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if progress == nil {
		return 0, fmt.Errorf("progress is not initialized: %w", errs.ErrToolPrecondition)
	}
	if len(progress.PendingRewrites) > 0 {
		return progress.PendingRewrites[0], nil
	}
	if progress.InProgressChapter > 0 {
		return progress.InProgressChapter, nil
	}
	chapter := progress.NextChapter()
	if chapter <= 0 {
		return 0, fmt.Errorf("cannot resolve writer chapter: %w", errs.ErrToolPrecondition)
	}
	return chapter, nil
}

func chapterContextArgs(chapter int, mode string) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"chapter": chapter, "context_mode": mode})
	return raw
}

// The first Writer request currently costs roughly 4.5K tokens including the
// system prompt and tool schemas. Reserve another ~2K for one dynamically
// bounded Chinese writing unit and spend only the remainder on the packet.
func writerUnitContextBudgetBytes(window int) int {
	const (
		minBudget      = 4 * 1024
		maxBudget      = 36 * 1024
		fixedAndOutput = 6800
		bytesPerToken  = 3
	)
	if window <= 0 {
		return maxBudget
	}
	budget := (window - fixedAndOutput) * bytesPerToken
	if budget < minBudget {
		return minBudget
	}
	if budget > maxBudget {
		return maxBudget
	}
	return budget
}
