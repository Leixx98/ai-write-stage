package tools

import "fmt"

// WriterPlanningBudget bounds the part of a chapter plan projected into one
// local Writer request. Smaller context windows require shorter, denser cards.
type WriterPlanningBudget struct {
	ContextWindow            int
	MinUnitChars             int
	MaxUnitChars             int
	MaxRequiredBeats         int
	MaxUnitForbiddenMoves    int
	MaxChapterListItems      int
	MaxChapterForbiddenMoves int
	MaxCreativeFreedom       int
	MaxItemRunes             int
	MaxSceneFieldRunes       int
	MaxUnitFieldRunes        int
}

// WriterPlanningBudgetForContext returns conservative limits for the Writer's
// configured window. Zero-valued list/text limits mean that legacy large-window
// behavior is preserved.
func WriterPlanningBudgetForContext(window int) WriterPlanningBudget {
	budget := WriterPlanningBudget{
		ContextWindow: window,
		MinUnitChars:  200,
		MaxUnitChars:  1000,
	}
	switch {
	case window > 0 && window <= 8192:
		budget.MaxRequiredBeats = 5
		budget.MaxUnitForbiddenMoves = 5
		budget.MaxChapterListItems = 6
		budget.MaxChapterForbiddenMoves = 4
		budget.MaxCreativeFreedom = 3
		budget.MaxItemRunes = 72
		budget.MaxSceneFieldRunes = 120
		budget.MaxUnitFieldRunes = 100
	case window > 0 && window <= 16384:
		budget.MaxRequiredBeats = 6
		budget.MaxUnitForbiddenMoves = 6
		budget.MaxChapterListItems = 8
		budget.MaxChapterForbiddenMoves = 6
		budget.MaxCreativeFreedom = 4
		budget.MaxItemRunes = 100
		budget.MaxSceneFieldRunes = 160
		budget.MaxUnitFieldRunes = 140
	}
	return budget
}

// Instruction is appended to the cloud planner prompt. The tool schema and
// Execute validation remain authoritative if a model ignores this text.
func (b WriterPlanningBudget) Instruction() string {
	if b.ContextWindow <= 0 || b.MaxRequiredBeats == 0 {
		return fmt.Sprintf("Writer 当前采用大窗口档位；每个 unit 可在 %d-%d 字内按情节密度动态规划，仍应使用简洁、可执行的场景卡。", b.MinUnitChars, b.MaxUnitChars)
	}
	return fmt.Sprintf(
		"Writer 上下文窗口为 %d tokens。为保证本地模型可执行：每个 unit 可在 %d-%d 字内按实际情节密度动态规划，低密度承接/过渡通常 200-400 字，高密度冲突/转折通常 800-1000 字，中等密度取其间；不得把所有 unit 机械设为同一字数。required_beats 最多 %d 项；unit forbidden_moves 最多 %d 项；每个条目最多 %d 字；场景状态/冲突/转折等字段最多 %d 字；end_anchor/transition 最多 %d 字；creative_freedom 最多 %d 项；本章 forbidden_moves 最多 %d 项。不得用增加字段长度代替拆分 unit。",
		b.ContextWindow, b.MinUnitChars, b.MaxUnitChars, b.MaxRequiredBeats,
		b.MaxUnitForbiddenMoves, b.MaxItemRunes, b.MaxSceneFieldRunes,
		b.MaxUnitFieldRunes, b.MaxCreativeFreedom, b.MaxChapterForbiddenMoves,
	)
}
