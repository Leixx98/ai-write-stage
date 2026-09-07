package tools

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/domain"
)

// projectWriterUnitContext turns the broad chapter context into one explicit
// execution packet. It is deliberately lossy: the cloud planner has already
// made chapter-level decisions, while the local Writer only needs the current
// unit, its immediate continuity anchors, and enforceable constraints.
func projectWriterUnitContext(result map[string]any, budget int) bool {
	working, _ := result["working_memory"].(map[string]any)
	plan, ok := chapterPlanValue(working["chapter_plan"])
	if !ok || plan == nil || len(plan.Scenes) == 0 {
		return false
	}
	progress, ok := writingProgressValue(working["writing_progress"])
	if !ok || progress == nil || progress.TotalUnits == 0 {
		return false
	}

	execution := buildWriterExecutionPacket(plan, progress)
	compactWorking := map[string]any{"execution": execution}
	copyContextValue(compactWorking, working, "user_rules")
	copyContextValue(compactWorking, working, "rewrite_brief")
	copyContextValue(compactWorking, working, "chapter_draft")
	copyContextValue(compactWorking, working, "finale")
	copyContextValue(compactWorking, working, "simulation_profile")

	projected := map[string]any{
		"context_mode":     "writer_unit",
		"working_memory":   compactWorking,
		"_loading_summary": writerExecutionSummary(plan.Chapter, progress),
	}

	if chars := compactRelevantCharacters(result["characters"], executionCharacterNames(progress)); len(chars) > 0 {
		projected["relevant_characters"] = chars
	}
	if rules := compactWorldRules(result["world_rules"]); len(rules) > 0 {
		projected["world_rules"] = rules
	}
	if selected, ok := result["selected_memory"].(map[string]any); ok && len(selected) > 0 {
		projected["selected_memory"] = selected
	}
	if pack, ok := result["reference_pack"].(map[string]any); ok {
		if styleRules, exists := pack["style_rules"]; exists {
			projected["style_rules"] = styleRules
		}
	}
	if warnings, exists := result["_warnings"]; exists {
		projected["_warnings"] = warnings
	}

	trimProjectedWriterContext(projected, budget)
	clear(result)
	for key, value := range projected {
		result[key] = value
	}
	return true
}

func buildWriterExecutionPacket(plan *domain.ChapterPlan, progress *domain.WritingProgress) map[string]any {
	chapter := map[string]any{
		"number": plan.Chapter,
		"title":  truncateRunes(plan.Title, 80),
		"goal":   truncateRunes(plan.Goal, 240),
	}
	if progress.CompletedUnits == 0 && plan.OpeningState != "" {
		chapter["opening_state"] = truncateRunes(plan.OpeningState, 240)
	}
	if plan.EmotionArc != "" {
		chapter["emotion_arc"] = truncateRunes(plan.EmotionArc, 180)
	}

	packet := map[string]any{
		"chapter":         chapter,
		"completed_units": progress.CompletedUnits,
		"total_units":     progress.TotalUnits,
	}
	if progress.Next == nil {
		packet["phase"] = "finalize_chapter"
		packet["required_action"] = "回读整章草稿，检查一致性，然后提交；不要重写已完成 writing unit"
		packet["continuity_checks"] = compactStrings(plan.Contract.ContinuityChecks, 6, 120)
		return packet
	}

	next := progress.Next
	forbidden := append([]string(nil), next.Unit.ForbiddenMoves...)
	forbidden = append(forbidden, plan.Contract.ForbiddenMoves...)
	forbidden = uniqueCompactStrings(forbidden, 12, 120)

	current := map[string]any{
		"ordinal":         next.Ordinal,
		"unit_id":         next.Unit.ID,
		"target_chars":    next.Unit.TargetChars,
		"final_unit":      next.FinalUnit,
		"required_beats":  compactStrings(next.Unit.RequiredBeats, 10, 140),
		"forbidden_moves": forbidden,
		"end_anchor":      truncateRunes(next.Unit.EndAnchor, 180),
		"transition":      truncateRunes(next.Unit.Transition, 180),
		"scene": map[string]any{
			"id":          next.SceneID,
			"purpose":     truncateRunes(next.ScenePurpose, 180),
			"location":    truncateRunes(next.Location, 100),
			"pov":         truncateRunes(next.POV, 60),
			"characters":  compactStrings(next.Characters, 8, 40),
			"entry_state": truncateRunes(next.EntryState, 160),
			"conflict":    truncateRunes(next.Conflict, 160),
			"turn":        truncateRunes(next.Turn, 160),
			"exit_state":  truncateRunes(next.ExitState, 160),
		},
	}
	packet["phase"] = "write_one_unit"
	packet["current_unit"] = current
	packet["required_action"] = "只写 current_unit，调用 write_chapter_unit 时只传 content；不得写后续 unit"
	if progress.PreviousTail != "" {
		packet["previous_tail"] = truncateRunes(progress.PreviousTail, 600)
	}
	if len(plan.CreativeFreedom) > 0 {
		packet["creative_freedom"] = compactStrings(plan.CreativeFreedom, 3, 120)
	}
	if len(plan.Contract.ContinuityChecks) > 0 {
		packet["continuity_checks"] = compactStrings(plan.Contract.ContinuityChecks, 6, 120)
	}
	if next.FinalUnit && plan.Hook != "" {
		packet["chapter_hook"] = truncateRunes(plan.Hook, 220)
	}
	return packet
}

func chapterPlanValue(value any) (*domain.ChapterPlan, bool) {
	switch plan := value.(type) {
	case *domain.ChapterPlan:
		return plan, plan != nil
	case domain.ChapterPlan:
		copy := plan
		return &copy, true
	default:
		return nil, false
	}
}

func writingProgressValue(value any) (*domain.WritingProgress, bool) {
	switch progress := value.(type) {
	case *domain.WritingProgress:
		return progress, progress != nil
	case domain.WritingProgress:
		copy := progress
		return &copy, true
	default:
		return nil, false
	}
}

func executionCharacterNames(progress *domain.WritingProgress) []string {
	if progress == nil || progress.Next == nil {
		return nil
	}
	names := append([]string(nil), progress.Next.Characters...)
	if progress.Next.POV != "" {
		names = append(names, progress.Next.POV)
	}
	return names
}

func compactRelevantCharacters(value any, names []string) []map[string]any {
	characters, ok := value.([]domain.Character)
	if !ok || len(characters) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(names))
	for _, name := range names {
		selected[strings.TrimSpace(name)] = struct{}{}
	}
	var out []map[string]any
	for _, character := range characters {
		_, wanted := selected[character.Name]
		if len(selected) > 0 && !wanted {
			continue
		}
		item := map[string]any{
			"name":        character.Name,
			"role":        truncateRunes(character.Role, 100),
			"description": truncateRunes(character.Description, 180),
			"traits":      compactStrings(character.Traits, 6, 60),
		}
		if len(character.Aliases) > 0 {
			item["aliases"] = compactStrings(character.Aliases, 5, 30)
		}
		if character.Arc != "" {
			item["arc"] = truncateRunes(character.Arc, 120)
		}
		out = append(out, item)
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func compactWorldRules(value any) []map[string]any {
	rules, ok := value.([]domain.WorldRule)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, min(len(rules), 12))
	for _, rule := range rules {
		out = append(out, map[string]any{
			"category": rule.Category,
			"rule":     truncateRunes(rule.Rule, 120),
			"boundary": truncateRunes(rule.Boundary, 120),
		})
		if len(out) >= 12 {
			break
		}
	}
	return out
}

func trimProjectedWriterContext(result map[string]any, budget int) {
	if budget <= 0 {
		return
	}
	var trimmed []string
	drop := func(key string) {
		if _, ok := result[key]; ok {
			delete(result, key)
			trimmed = append(trimmed, key)
		}
	}
	for _, key := range []string{"selected_memory", "style_rules", "world_rules"} {
		if jsonSize(result) <= budget {
			break
		}
		drop(key)
	}
	if jsonSize(result) > budget {
		if working, ok := result["working_memory"].(map[string]any); ok {
			if execution, ok := working["execution"].(map[string]any); ok {
				delete(execution, "creative_freedom")
				delete(execution, "continuity_checks")
				minimizeWriterExecution(execution)
				trimmed = append(trimmed, "creative_freedom", "continuity_checks")
			}
		}
	}
	if jsonSize(result) > budget {
		drop("relevant_characters")
	}
	if jsonSize(result) > budget {
		if working, ok := result["working_memory"].(map[string]any); ok {
			if userRules, ok := working["user_rules"].(map[string]any); ok {
				delete(userRules, "preferences")
				trimmed = append(trimmed, "user_rules.preferences")
			}
		}
	}
	if jsonSize(result) > budget {
		if working, ok := result["working_memory"].(map[string]any); ok {
			delete(working, "user_rules")
			trimmed = append(trimmed, "user_rules")
		}
	}
	if jsonSize(result) > budget {
		delete(result, "_warnings")
		if working, ok := result["working_memory"].(map[string]any); ok {
			if execution, ok := working["execution"].(map[string]any); ok {
				hardMinimizeWriterExecution(execution)
			}
		}
		trimmed = append(trimmed, "noncritical_execution_detail")
	}
	if len(trimmed) > 0 {
		result["_trimmed"] = slices.Compact(trimmed)
	}
	if jsonSize(result) > budget {
		delete(result, "_trimmed")
	}
}

func minimizeWriterExecution(execution map[string]any) {
	if chapter, ok := execution["chapter"].(map[string]any); ok {
		delete(chapter, "opening_state")
		delete(chapter, "emotion_arc")
		if goal, ok := chapter["goal"].(string); ok {
			chapter["goal"] = truncateRunes(goal, 120)
		}
	}
	if tail, ok := execution["previous_tail"].(string); ok {
		execution["previous_tail"] = truncateRunes(tail, 260)
	}
	current, ok := execution["current_unit"].(map[string]any)
	if !ok {
		return
	}
	if values, ok := current["required_beats"].([]string); ok {
		current["required_beats"] = compactStringsTotal(values, 6, 80, 240)
	}
	if values, ok := current["forbidden_moves"].([]string); ok {
		current["forbidden_moves"] = compactStringsTotal(values, 8, 80, 280)
	}
	for _, key := range []string{"end_anchor", "transition"} {
		if value, ok := current[key].(string); ok {
			current[key] = truncateRunes(value, 100)
		}
	}
	if scene, ok := current["scene"].(map[string]any); ok {
		for _, key := range []string{"purpose", "entry_state", "conflict", "turn", "exit_state"} {
			if value, ok := scene[key].(string); ok {
				scene[key] = truncateRunes(value, 80)
			}
		}
	}
}

func hardMinimizeWriterExecution(execution map[string]any) {
	if chapter, ok := execution["chapter"].(map[string]any); ok {
		delete(chapter, "goal")
	}
	if tail, ok := execution["previous_tail"].(string); ok {
		execution["previous_tail"] = truncateRunes(tail, 120)
	}
	current, ok := execution["current_unit"].(map[string]any)
	if !ok {
		return
	}
	if values, ok := current["required_beats"].([]string); ok {
		current["required_beats"] = compactStringsTotal(values, 5, 60, 160)
	}
	if values, ok := current["forbidden_moves"].([]string); ok {
		current["forbidden_moves"] = compactStringsTotal(values, 6, 60, 200)
	}
	if value, ok := current["transition"].(string); ok {
		current["transition"] = truncateRunes(value, 60)
	}
	if value, ok := current["end_anchor"].(string); ok {
		current["end_anchor"] = truncateRunes(value, 80)
	}
	if scene, ok := current["scene"].(map[string]any); ok {
		delete(scene, "purpose")
		delete(scene, "exit_state")
		for _, key := range []string{"entry_state", "conflict", "turn"} {
			if value, ok := scene[key].(string); ok {
				scene[key] = truncateRunes(value, 50)
			}
		}
	}
}

func compactStrings(values []string, maxItems, maxRunes int) []string {
	if maxItems <= 0 || maxRunes <= 0 {
		return nil
	}
	out := make([]string, 0, min(len(values), maxItems))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, truncateRunes(value, maxRunes))
		if len(out) >= maxItems {
			break
		}
	}
	return out
}

func compactStringsTotal(values []string, maxItems, maxRunes, maxTotalRunes int) []string {
	if maxTotalRunes <= 0 {
		return nil
	}
	var out []string
	remaining := maxTotalRunes
	for _, value := range compactStrings(values, maxItems, maxRunes) {
		if remaining <= 0 {
			break
		}
		runes := []rune(value)
		if len(runes) > remaining {
			value = string(runes[:remaining]) + "..."
			runes = []rune(value)
		}
		out = append(out, value)
		remaining -= len(runes)
	}
	return out
}

func uniqueCompactStrings(values []string, maxItems, maxRunes int) []string {
	seen := make(map[string]struct{}, len(values))
	var unique []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return compactStrings(unique, maxItems, maxRunes)
}

func copyContextValue(dst, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func jsonSize(value any) int {
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(raw)
}

func writerExecutionSummary(chapter int, progress *domain.WritingProgress) string {
	if progress == nil {
		return "writer_unit"
	}
	if progress.Next == nil {
		return "writer_unit ch=" + itoa(chapter) + " phase=finalize"
	}
	return "writer_unit ch=" + itoa(chapter) + " unit=" + progress.Next.Unit.ID
}

func itoa(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for value > 0 {
		i--
		buf[i] = digits[value%10]
		value /= 10
	}
	return string(buf[i:])
}
