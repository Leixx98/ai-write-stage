package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	"github.com/voocel/ainovel-cli/internal/errs"
	"github.com/voocel/ainovel-cli/internal/store"
)

// AutoCommitPlannedChapter commits a completed unit draft without another LLM
// pass. Metadata is a deterministic projection of the cloud-authored chapter
// plan; no attempt is made to review or reinterpret the local prose.
func AutoCommitPlannedChapter(ctx context.Context, st *store.Store, styleStats *StyleStatsIndex, chapter int) (json.RawMessage, error) {
	if st == nil || styleStats == nil {
		return nil, fmt.Errorf("auto commit dependencies are nil: %w", errs.ErrToolPrecondition)
	}
	if chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	plan, err := st.Drafts.LoadChapterPlan(chapter)
	if err != nil {
		return nil, fmt.Errorf("load chapter plan: %w: %w", errs.ErrStoreRead, err)
	}
	if plan == nil {
		return nil, fmt.Errorf("chapter %d plan not found: %w", chapter, errs.ErrToolPrecondition)
	}
	writing, err := st.Drafts.LoadWritingProgress(chapter)
	if err != nil {
		return nil, fmt.Errorf("load writing progress: %w: %w", errs.ErrStoreRead, err)
	}
	if writing == nil || writing.TotalUnits == 0 || !writing.Complete {
		return nil, fmt.Errorf("chapter %d writing units are not complete: %w", chapter, errs.ErrToolPrecondition)
	}
	// Unit files are the source of truth. Rebuild the draft immediately before
	// commit so a stale draft or an older Finalizer rewrite cannot replace the
	// local Writer's completed units. Generated images are inserted immediately
	// after their source units to keep the final chapter Markdown self-contained.
	assignments := plan.WritingUnits()
	last := len(assignments)
	if _, err := st.Drafts.RebuildWritingUnitDraft(chapter, last); err != nil {
		return nil, fmt.Errorf("rebuild completed unit draft: %w: %w", errs.ErrStoreWrite, err)
	}

	args := deterministicCommitArgs(*plan)
	payload, err := json.Marshal(args)
	if err != nil {
		return nil, fmt.Errorf("marshal automatic commit: %w", err)
	}
	return NewCommitChapterTool(st, styleStats).Execute(ctx, payload)
}

func deterministicCommitArgs(plan domain.ChapterPlan) commitArgs {
	title := strings.TrimSpace(plan.Title)
	if title == "" {
		title = fmt.Sprintf("第%d章", plan.Chapter)
	}

	characters := make([]string, 0, 8)
	keyEvents := make([]string, 0, len(plan.Scenes))
	summaryParts := make([]string, 0, len(plan.Scenes)+2)
	if goal := strings.TrimSpace(plan.Goal); goal != "" {
		summaryParts = append(summaryParts, goal)
	}
	for _, scene := range plan.Scenes {
		characters = appendUniqueNonEmpty(characters, scene.Characters...)
		event := strings.TrimSpace(scene.Turn)
		if event == "" {
			event = strings.TrimSpace(scene.Purpose)
		}
		if event != "" {
			keyEvents = appendUniqueNonEmpty(keyEvents, truncateRunes(event, 160))
			summaryParts = appendUniqueNonEmpty(summaryParts, truncateRunes(event, 120))
		}
	}
	if len(keyEvents) == 0 {
		fallback := strings.TrimSpace(plan.Goal)
		if fallback == "" {
			fallback = title
		}
		keyEvents = []string{truncateRunes(fallback, 160)}
	}
	if hook := strings.TrimSpace(plan.Hook); hook != "" {
		summaryParts = appendUniqueNonEmpty(summaryParts, truncateRunes(hook, 120))
	}
	summary := strings.Join(summaryParts, "；")
	if summary == "" {
		summary = fmt.Sprintf("第%d章按章节计划完成。", plan.Chapter)
	}

	return commitArgs{
		Chapter:    plan.Chapter,
		Title:      title,
		Summary:    truncateRunes(summary, 200),
		Characters: characters,
		KeyEvents:  keyEvents,
	}
}

func appendUniqueNonEmpty(dst []string, values ...string) []string {
	seen := make(map[string]struct{}, len(dst)+len(values))
	for _, value := range dst {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		dst = append(dst, value)
	}
	return dst
}
