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

// PlanChapterTool 保存章节构思，Agent 自主决定规划粒度。
type PlanChapterTool struct {
	store        *store.Store
	writerWindow func() int
}

func NewPlanChapterTool(store *store.Store) *PlanChapterTool {
	return &PlanChapterTool{store: store}
}

func NewPlanChapterToolWithWriterWindow(store *store.Store, writerWindow func() int) *PlanChapterTool {
	return &PlanChapterTool{store: store, writerWindow: writerWindow}
}

func (t *PlanChapterTool) planningBudget() WriterPlanningBudget {
	window := 0
	if t.writerWindow != nil {
		window = t.writerWindow()
	}
	return WriterPlanningBudgetForContext(window)
}

func (t *PlanChapterTool) Name() string { return "plan_chapter" }
func (t *PlanChapterTool) Description() string {
	budget := t.planningBudget()
	return fmt.Sprintf("保存章节执行计划：叙事场景 scenes 与每次可在 %d-%d 字内按情节密度动态分配的生成片段 units 分层声明。%s",
		budget.MinUnitChars, budget.MaxUnitChars, budget.Instruction())
}
func (t *PlanChapterTool) Label() string { return "规划章节" }

// 写工具，禁止并发。
func (t *PlanChapterTool) ReadOnly(_ json.RawMessage) bool        { return false }
func (t *PlanChapterTool) ConcurrencySafe(_ json.RawMessage) bool { return false }

func (t *PlanChapterTool) Schema() map[string]any {
	budget := t.planningBudget()
	unit := schema.Object(
		schema.Property("id", schema.String("全章唯一的写作片段 ID，如 12-2-1")).Required(),
		schema.Property("target_chars", schema.Int(fmt.Sprintf("本次正文生成目标字数，必须在 %d-%d 之间并按情节密度决定；低密度通常 200-400，高密度通常 800-1000", budget.MinUnitChars, budget.MaxUnitChars))).Required(),
		schema.Property("required_beats", schema.Array("本片段必须写出的动作或信息，按因果顺序排列", schema.String(""))).Required(),
		schema.Property("forbidden_moves", schema.Array("本片段不得提前发生的事件；无则为空数组", schema.String(""))).Required(),
		schema.Property("end_anchor", schema.String("本次写到哪里停止；不是章末钩子")).Required(),
		schema.Property("transition", schema.String("与前一片段的衔接方式；首片段填开场方式")).Required(),
	)
	scene := schema.Object(
		schema.Property("id", schema.String("全章唯一的场景 ID，如 12-2")).Required(),
		schema.Property("purpose", schema.String("场景承担的叙事功能")).Required(),
		schema.Property("location", schema.String("地点和必要的时间信息")).Required(),
		schema.Property("pov", schema.String("视角角色")).Required(),
		schema.Property("characters", schema.Array("本场景出场角色", schema.String(""))).Required(),
		schema.Property("entry_state", schema.String("场景开始时人物认知、目标和关键物品状态")).Required(),
		schema.Property("conflict", schema.String("场景中的即时阻力或对抗")).Required(),
		schema.Property("turn", schema.String("改变场景走向的关键转折")).Required(),
		schema.Property("exit_state", schema.String("场景结束后新增事实和人物状态")).Required(),
		schema.Property("target_chars", schema.Int("场景预计字数，由本场景实际情节承载量决定，应约等于其 units 目标字数之和；不得套用固定档位")).Required(),
		schema.Property("units", schema.Array("按执行顺序排列的写作片段；长场景可拆成多个片段", unit)).Required(),
	)
	return schema.Object(
		schema.Property("chapter", schema.Int("章节号")).Required(),
		schema.Property("title", schema.String("暂定章节标题；写作后可按正文调整")).Required(),
		schema.Property("goal", schema.String("本章目标")).Required(),
		schema.Property("conflict", schema.String("核心冲突")).Required(),
		schema.Property("hook", schema.String("章末钩子")).Required(),
		schema.Property("opening_state", schema.String("本章开始时的地点、时间、人物认知和关键状态")).Required(),
		schema.Property("target_chars", schema.Int("全章预计字数，由所有动态规划 units 的目标字数汇总得出")).Required(),
		schema.Property("emotion_arc", schema.String("情绪曲线")),
		schema.Property("notes", schema.String("自由备忘（任何你觉得写作时需要记住的东西）")),
		schema.Property("creative_freedom", schema.Array("明确留给 Writer 自由发挥的部分", schema.String(""))).Required(),
		schema.Property("scenes", schema.Array("2-4 个叙事场景；场景不是固定 1000 字的生成片段", scene)).Required(),
		schema.Property("required_beats", schema.Array("本章必须完成的推进项", schema.String(""))),
		schema.Property("forbidden_moves", schema.Array("本章明确不能发生的推进", schema.String(""))),
		schema.Property("continuity_checks", schema.Array("本章需特别核对的连续性点", schema.String(""))),
		schema.Property("evaluation_focus", schema.Array("Editor 重点检查项", schema.String(""))),
		schema.Property("emotion_target", schema.String("可选：本章希望读者主要感受到的情绪")),
		schema.Property("payoff_points", schema.Array("可选：关键章希望回应的情节点或兑现点", schema.String(""))),
		schema.Property("hook_goal", schema.String("可选：章末希望驱动的追读欲望或悬念目标")),
	)
}

func (t *PlanChapterTool) Execute(_ context.Context, args json.RawMessage) (json.RawMessage, error) {
	plan, err := decodeChapterPlanArgs(args)
	if err != nil {
		return nil, fmt.Errorf("invalid args: %w: %w", errs.ErrToolArgs, err)
	}
	if plan.Chapter <= 0 {
		return nil, fmt.Errorf("chapter must be > 0: %w", errs.ErrToolArgs)
	}
	completed, err := t.store.Progress.IsChapterCompleted(plan.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load progress: %w: %w", errs.ErrStoreRead, err)
	}
	if completed {
		return json.Marshal(map[string]any{
			"chapter":   plan.Chapter,
			"skipped":   true,
			"completed": true,
			"reason":    fmt.Sprintf("第 %d 章已提交完成，不能重新规划", plan.Chapter),
		})
	}
	existing, err := t.store.Drafts.LoadChapterPlan(plan.Chapter)
	if err != nil {
		return nil, fmt.Errorf("load existing chapter plan: %w: %w", errs.ErrStoreRead, err)
	}
	if existing != nil {
		if _, err := t.store.Checkpoints.AppendArtifact(
			domain.ChapterScope(plan.Chapter), "plan",
			fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
		); err != nil {
			return nil, fmt.Errorf("repair chapter plan checkpoint: %w", err)
		}
		return json.Marshal(map[string]any{
			"chapter": plan.Chapter,
			"skipped": true,
			"planned": true,
			"scenes":  len(existing.Scenes),
			"units":   len(existing.WritingUnits()),
			"reason":  "章节计划已存在，拒绝覆盖",
		})
	}
	if err := validateWritingPlanWithBudget(plan, t.planningBudget()); err != nil {
		return nil, err
	}
	if err := t.store.Progress.ValidateChapterWork(plan.Chapter); err != nil {
		return nil, err
	}
	if err := EnsureChapterExpanded(t.store, plan.Chapter); err != nil {
		return nil, err
	}

	if err := t.store.Drafts.SaveChapterPlan(plan); err != nil {
		return nil, fmt.Errorf("save chapter plan: %w", err)
	}
	if err := t.store.Progress.StartChapter(plan.Chapter); err != nil {
		return nil, fmt.Errorf("mark chapter in progress: %w", err)
	}

	if _, err := t.store.Checkpoints.AppendArtifact(
		domain.ChapterScope(plan.Chapter), "plan",
		fmt.Sprintf("drafts/%02d.plan.json", plan.Chapter),
	); err != nil {
		return nil, fmt.Errorf("checkpoint chapter plan: %w", err)
	}

	return json.Marshal(map[string]any{
		"planned":   true,
		"chapter":   plan.Chapter,
		"scenes":    len(plan.Scenes),
		"units":     len(plan.WritingUnits()),
		"next_step": "章节计划已落盘并结束本轮；Engine 将另行派发 Writer 按 unit 写作",
	})
}

func decodeChapterPlanArgs(args json.RawMessage) (domain.ChapterPlan, error) {
	var a struct {
		Chapter          int                `json:"chapter"`
		Title            string             `json:"title"`
		Goal             string             `json:"goal"`
		Conflict         string             `json:"conflict"`
		Hook             string             `json:"hook"`
		OpeningState     string             `json:"opening_state"`
		TargetChars      int                `json:"target_chars"`
		EmotionArc       string             `json:"emotion_arc"`
		Notes            string             `json:"notes"`
		CreativeFreedom  []string           `json:"creative_freedom"`
		Scenes           []domain.ScenePlan `json:"scenes"`
		RequiredBeats    []string           `json:"required_beats"`
		ForbiddenMoves   []string           `json:"forbidden_moves"`
		ContinuityChecks []string           `json:"continuity_checks"`
		EvaluationFocus  []string           `json:"evaluation_focus"`
		EmotionTarget    string             `json:"emotion_target"`
		PayoffPoints     []string           `json:"payoff_points"`
		HookGoal         string             `json:"hook_goal"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return domain.ChapterPlan{}, err
	}

	return domain.ChapterPlan{
		Chapter:         a.Chapter,
		Title:           a.Title,
		Goal:            a.Goal,
		Conflict:        a.Conflict,
		Hook:            a.Hook,
		OpeningState:    a.OpeningState,
		TargetChars:     a.TargetChars,
		EmotionArc:      a.EmotionArc,
		Notes:           a.Notes,
		CreativeFreedom: a.CreativeFreedom,
		Scenes:          a.Scenes,
		Contract: domain.ChapterContract{
			RequiredBeats:    a.RequiredBeats,
			ForbiddenMoves:   a.ForbiddenMoves,
			ContinuityChecks: a.ContinuityChecks,
			EvaluationFocus:  a.EvaluationFocus,
			EmotionTarget:    a.EmotionTarget,
			PayoffPoints:     a.PayoffPoints,
			HookGoal:         a.HookGoal,
		},
	}, nil
}

func validateWritingPlan(plan domain.ChapterPlan) error {
	return validateWritingPlanWithBudget(plan, WriterPlanningBudgetForContext(0))
}

func validateWritingPlanWithBudget(plan domain.ChapterPlan, budget WriterPlanningBudget) error {
	// 旧书和旧版测试可能已有无 scenes 的计划；继续允许 Writer 走 legacy 整章工具。
	// 新 chapter_planner 的工具 schema 强制包含 scenes，不会产生这种形态。
	if len(plan.Scenes) == 0 {
		return nil
	}
	if strings.TrimSpace(plan.Title) == "" || strings.TrimSpace(plan.Goal) == "" ||
		strings.TrimSpace(plan.Conflict) == "" || strings.TrimSpace(plan.Hook) == "" {
		return fmt.Errorf("title, goal, conflict and hook are required for scene plans: %w", errs.ErrToolArgs)
	}
	if strings.TrimSpace(plan.OpeningState) == "" || plan.TargetChars <= 0 {
		return fmt.Errorf("opening_state and target_chars are required for scene plans: %w", errs.ErrToolArgs)
	}
	if len(plan.CreativeFreedom) == 0 || hasBlankString(plan.CreativeFreedom) {
		return fmt.Errorf("creative_freedom must contain non-empty writing choices: %w", errs.ErrToolArgs)
	}
	if err := validateBoundedStrings("creative_freedom", plan.CreativeFreedom, budget.MaxCreativeFreedom, budget.MaxItemRunes); err != nil {
		return err
	}
	chapterLists := []struct {
		name  string
		items []string
		max   int
	}{
		{"required_beats", plan.Contract.RequiredBeats, budget.MaxChapterListItems},
		{"forbidden_moves", plan.Contract.ForbiddenMoves, budget.MaxChapterForbiddenMoves},
		{"continuity_checks", plan.Contract.ContinuityChecks, budget.MaxChapterListItems},
		{"evaluation_focus", plan.Contract.EvaluationFocus, budget.MaxChapterListItems},
		{"payoff_points", plan.Contract.PayoffPoints, budget.MaxChapterListItems},
	}
	for _, list := range chapterLists {
		if err := validateBoundedStrings(list.name, list.items, list.max, budget.MaxItemRunes); err != nil {
			return err
		}
	}
	if len(plan.Scenes) > 6 {
		return fmt.Errorf("scenes must contain at most 6 scenes: %w", errs.ErrToolArgs)
	}
	sceneIDs := make(map[string]struct{}, len(plan.Scenes))
	unitIDs := make(map[string]struct{})
	unitCount := 0
	chapterUnitChars := 0
	for si, scene := range plan.Scenes {
		if strings.TrimSpace(scene.ID) == "" || strings.TrimSpace(scene.Purpose) == "" ||
			strings.TrimSpace(scene.Location) == "" || strings.TrimSpace(scene.POV) == "" ||
			strings.TrimSpace(scene.EntryState) == "" || strings.TrimSpace(scene.ExitState) == "" ||
			strings.TrimSpace(scene.Conflict) == "" || strings.TrimSpace(scene.Turn) == "" {
			return fmt.Errorf("scenes[%d] lacks executable purpose/location/pov/state/conflict/turn fields: %w", si, errs.ErrToolArgs)
		}
		if scene.TargetChars <= 0 {
			return fmt.Errorf("scenes[%d].target_chars must be > 0: %w", si, errs.ErrToolArgs)
		}
		for name, value := range map[string]string{
			"purpose": scene.Purpose, "entry_state": scene.EntryState, "conflict": scene.Conflict,
			"turn": scene.Turn, "exit_state": scene.ExitState,
		} {
			if budget.MaxSceneFieldRunes > 0 && utf8.RuneCountInString(value) > budget.MaxSceneFieldRunes {
				return fmt.Errorf("scenes[%d].%s must be at most %d characters for Writer context window %d: %w",
					si, name, budget.MaxSceneFieldRunes, budget.ContextWindow, errs.ErrToolArgs)
			}
		}
		if len(scene.Characters) == 0 || hasBlankString(scene.Characters) {
			return fmt.Errorf("scenes[%d].characters must contain non-empty names: %w", si, errs.ErrToolArgs)
		}
		if _, exists := sceneIDs[scene.ID]; exists {
			return fmt.Errorf("duplicate scene id %q: %w", scene.ID, errs.ErrToolArgs)
		}
		sceneIDs[scene.ID] = struct{}{}
		if len(scene.Units) == 0 {
			return fmt.Errorf("scenes[%d].units must not be empty: %w", si, errs.ErrToolArgs)
		}
		sceneUnitChars := 0
		for ui, unit := range scene.Units {
			unitCount++
			if strings.TrimSpace(unit.ID) == "" || strings.TrimSpace(unit.EndAnchor) == "" ||
				strings.TrimSpace(unit.Transition) == "" || len(unit.RequiredBeats) == 0 || hasBlankString(unit.RequiredBeats) {
				return fmt.Errorf("scenes[%d].units[%d] lacks id/required_beats/end_anchor/transition: %w", si, ui, errs.ErrToolArgs)
			}
			if hasBlankString(unit.ForbiddenMoves) {
				return fmt.Errorf("scenes[%d].units[%d].forbidden_moves contains an empty item: %w", si, ui, errs.ErrToolArgs)
			}
			if err := validateBoundedStrings(fmt.Sprintf("scenes[%d].units[%d].required_beats", si, ui), unit.RequiredBeats, budget.MaxRequiredBeats, budget.MaxItemRunes); err != nil {
				return err
			}
			if err := validateBoundedStrings(fmt.Sprintf("scenes[%d].units[%d].forbidden_moves", si, ui), unit.ForbiddenMoves, budget.MaxUnitForbiddenMoves, budget.MaxItemRunes); err != nil {
				return err
			}
			for name, value := range map[string]string{"end_anchor": unit.EndAnchor, "transition": unit.Transition} {
				if budget.MaxUnitFieldRunes > 0 && utf8.RuneCountInString(value) > budget.MaxUnitFieldRunes {
					return fmt.Errorf("scenes[%d].units[%d].%s must be at most %d characters for Writer context window %d: %w",
						si, ui, name, budget.MaxUnitFieldRunes, budget.ContextWindow, errs.ErrToolArgs)
				}
			}
			if unit.TargetChars < budget.MinUnitChars || unit.TargetChars > budget.MaxUnitChars {
				return fmt.Errorf("unit %q target_chars must be %d-%d for Writer context window %d, got %d: %w",
					unit.ID, budget.MinUnitChars, budget.MaxUnitChars, budget.ContextWindow, unit.TargetChars, errs.ErrToolArgs)
			}
			if _, exists := unitIDs[unit.ID]; exists {
				return fmt.Errorf("duplicate writing unit id %q: %w", unit.ID, errs.ErrToolArgs)
			}
			unitIDs[unit.ID] = struct{}{}
			sceneUnitChars += unit.TargetChars
		}
		if !targetCharsClose(sceneUnitChars, scene.TargetChars, 200) {
			return fmt.Errorf("scene %q target_chars %d does not approximately match unit total %d: %w",
				scene.ID, scene.TargetChars, sceneUnitChars, errs.ErrToolArgs)
		}
		chapterUnitChars += sceneUnitChars
	}
	if unitCount > 10 {
		return fmt.Errorf("writing units must contain at most 10 units, got %d: %w", unitCount, errs.ErrToolArgs)
	}
	if !targetCharsClose(chapterUnitChars, plan.TargetChars, 300) {
		return fmt.Errorf("chapter target_chars %d does not approximately match unit total %d: %w",
			plan.TargetChars, chapterUnitChars, errs.ErrToolArgs)
	}
	return nil
}

func validateBoundedStrings(name string, items []string, maxItems, maxRunes int) error {
	if maxItems > 0 && len(items) > maxItems {
		return fmt.Errorf("%s must contain at most %d items, got %d: %w", name, maxItems, len(items), errs.ErrToolArgs)
	}
	if maxRunes > 0 {
		for i, item := range items {
			if utf8.RuneCountInString(item) > maxRunes {
				return fmt.Errorf("%s[%d] must be at most %d characters: %w", name, i, maxRunes, errs.ErrToolArgs)
			}
		}
	}
	return nil
}

func hasBlankString(items []string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == "" {
			return true
		}
	}
	return false
}

// 规划字数是创作预算而非精确计数；允许 15% 或固定下限内的估算误差。
func targetCharsClose(actual, target, minimumTolerance int) bool {
	tolerance := target * 15 / 100
	if tolerance < minimumTolerance {
		tolerance = minimumTolerance
	}
	delta := actual - target
	if delta < 0 {
		delta = -delta
	}
	return delta <= tolerance
}
