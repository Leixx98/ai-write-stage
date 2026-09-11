package domain

// ChapterPlan 是 Chapter Planner 交给 Writer 的章节执行计划。
// 新计划按叙事场景和单次生成片段分层；无 Scenes 的旧计划仍可兼容读取。
type ChapterPlan struct {
	Chapter         int             `json:"chapter"`
	Title           string          `json:"title"`
	Goal            string          `json:"goal"`
	Conflict        string          `json:"conflict"`
	Hook            string          `json:"hook"`
	OpeningState    string          `json:"opening_state,omitempty"`
	TargetChars     int             `json:"target_chars,omitempty"`
	EmotionArc      string          `json:"emotion_arc,omitempty"`
	Notes           string          `json:"notes,omitempty"` // Agent 的自由备忘
	CreativeFreedom []string        `json:"creative_freedom,omitempty"`
	Scenes          []ScenePlan     `json:"scenes,omitempty"`
	Contract        ChapterContract `json:"contract,omitempty"`
}

// ScenePlan 是叙事场景，不等同于一次模型调用。一个长场景可以包含多个
// WritingUnit，由本地 Writer 分多次完成而不制造不必要的转场。
type ScenePlan struct {
	ID          string        `json:"id"`
	Purpose     string        `json:"purpose"`
	Location    string        `json:"location"`
	POV         string        `json:"pov"`
	Characters  []string      `json:"characters"`
	EntryState  string        `json:"entry_state"`
	Conflict    string        `json:"conflict"`
	Turn        string        `json:"turn"`
	ExitState   string        `json:"exit_state"`
	TargetChars int           `json:"target_chars"`
	Units       []WritingUnit `json:"units"`
}

// WritingUnit 是单次正文生成的执行卡。TargetChars 的上限随 Writer 上下文
// 窗口动态收紧；EndAnchor 只规定本次写到哪里，中间 unit 不应被写成章末钩子或阶段总结。
type WritingUnit struct {
	ID             string   `json:"id"`
	TargetChars    int      `json:"target_chars"`
	RequiredBeats  []string `json:"required_beats"`
	ForbiddenMoves []string `json:"forbidden_moves,omitempty"`
	EndAnchor      string   `json:"end_anchor"`
	Transition     string   `json:"transition,omitempty"`
}

// WritingUnitAssignment 把扁平执行顺序与所属场景绑定，供工具和上下文直接使用。
type WritingUnitAssignment struct {
	Ordinal          int         `json:"ordinal"`
	SceneID          string      `json:"scene_id"`
	ScenePurpose     string      `json:"scene_purpose"`
	Location         string      `json:"location"`
	POV              string      `json:"pov"`
	Characters       []string    `json:"characters"`
	EntryState       string      `json:"entry_state"`
	Conflict         string      `json:"conflict"`
	Turn             string      `json:"turn"`
	ExitState        string      `json:"exit_state"`
	SceneTargetChars int         `json:"scene_target_chars"`
	Unit             WritingUnit `json:"unit"`
	FinalUnit        bool        `json:"final_unit"`
}

// WritingProgress 是 unit 工件推导出的可恢复事实，不单独维护可漂移的计数文件。
type WritingProgress struct {
	Chapter          int                    `json:"chapter"`
	TotalUnits       int                    `json:"total_units"`
	CompletedUnits   int                    `json:"completed_units"`
	CompletedUnitIDs []string               `json:"completed_unit_ids,omitempty"`
	Next             *WritingUnitAssignment `json:"next,omitempty"`
	PreviousTail     string                 `json:"previous_tail,omitempty"`
	Complete         bool                   `json:"complete"`
}

// WritingUnits 按场景及 unit 声明顺序返回扁平执行队列。
func (p ChapterPlan) WritingUnits() []WritingUnitAssignment {
	var out []WritingUnitAssignment
	for _, scene := range p.Scenes {
		for _, unit := range scene.Units {
			out = append(out, WritingUnitAssignment{
				Ordinal:          len(out) + 1,
				SceneID:          scene.ID,
				ScenePurpose:     scene.Purpose,
				Location:         scene.Location,
				POV:              scene.POV,
				Characters:       append([]string(nil), scene.Characters...),
				EntryState:       scene.EntryState,
				Conflict:         scene.Conflict,
				Turn:             scene.Turn,
				ExitState:        scene.ExitState,
				SceneTargetChars: scene.TargetChars,
				Unit:             unit,
			})
		}
	}
	if len(out) > 0 {
		out[len(out)-1].FinalUnit = true
	}
	return out
}

// ChapterContract 是 Writer 和 Editor 共享的章节验收契约。
// 它定义本章必须完成的推进项、禁止越界项以及审阅关注点。
type ChapterContract struct {
	RequiredBeats    []string `json:"required_beats,omitempty"`    // 本章必须落地的推进项
	ForbiddenMoves   []string `json:"forbidden_moves,omitempty"`   // 本章明确不能发生的推进
	ContinuityChecks []string `json:"continuity_checks,omitempty"` // 本章需特别核对的连续性点
	EvaluationFocus  []string `json:"evaluation_focus,omitempty"`  // 后续人工检查重点
	EmotionTarget    string   `json:"emotion_target,omitempty"`    // 可选：本章希望读者主要感受到的情绪
	PayoffPoints     []string `json:"payoff_points,omitempty"`     // 可选：关键章希望回应的情节点/兑现点
	HookGoal         string   `json:"hook_goal,omitempty"`         // 可选：章末钩子希望驱动的追读欲望
}

// ChapterSummary 章节摘要，供后续章节的上下文窗口使用。
type ChapterSummary struct {
	Chapter    int      `json:"chapter"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Characters []string `json:"characters"`
	KeyEvents  []string `json:"key_events"`
}

// ArcSummary 弧级摘要。
type ArcSummary struct {
	Volume    int      `json:"volume"`
	Arc       int      `json:"arc"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// VolumeSummary 卷级摘要，卷结束时生成。
type VolumeSummary struct {
	Volume    int      `json:"volume"`
	Title     string   `json:"title"`
	Summary   string   `json:"summary"`
	KeyEvents []string `json:"key_events"`
}

// CharacterSnapshot 角色状态快照，弧边界时记录。
type CharacterSnapshot struct {
	Volume     int    `json:"volume"`
	Arc        int    `json:"arc"`
	Name       string `json:"name"`
	Status     string `json:"status"`
	Power      string `json:"power,omitempty"`
	Motivation string `json:"motivation"`
	Relations  string `json:"relations,omitempty"`
}

// OutlineFeedback Writer 对大纲的反馈，提交章节时可选。
type OutlineFeedback struct {
	Deviation  string `json:"deviation"`  // 偏离描述
	Suggestion string `json:"suggestion"` // 调整建议
}

// WritingStyleRules 从已写章节中提炼的写作规则，弧边界时由 Editor 生成。
// 取代原文片段（style_anchors / voice_samples），用规则替代搬运原文。
type WritingStyleRules struct {
	Volume    int              `json:"volume"`
	Arc       int              `json:"arc"`
	Prose     []string         `json:"prose"`      // 3-5 条叙述风格规则，每条 ≤50 字
	Dialogue  []CharacterVoice `json:"dialogue"`   // 角色对话风格规则
	Taboos    []string         `json:"taboos"`     // 禁忌清单
	UpdatedAt string           `json:"updated_at"` // ISO8601 时间戳
}

// CharacterVoice 单个角色的对话风格规则。
type CharacterVoice struct {
	Name  string   `json:"name"`
	Rules []string `json:"rules"` // 2-3 条语言特征规则，每条 ≤30 字
}

// RelatedChapter 推荐回读的相关章节。
type RelatedChapter struct {
	Chapter int    `json:"chapter"`
	Reason  string `json:"reason"`
}

// RecallItem 是按当前任务选择性召回的长期信息。
// 它不替代正式工件，只负责把当前轮真正相关的少量历史信息回注给模型。
type RecallItem struct {
	Kind    string `json:"kind"`
	Key     string `json:"key,omitempty"`
	Chapter int    `json:"chapter,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// CommitResult 是 commit_chapter 工具的结构化返回值。
// 只包含事实字段；"下一步做什么"由 Reminder 通道基于当前 Progress 自行生成。
type CommitResult struct {
	Chapter        int              `json:"chapter"`
	Committed      bool             `json:"committed"`
	WordCount      int              `json:"word_count"`
	NextChapter    int              `json:"next_chapter"`
	ReviewRequired bool             `json:"review_required"`
	ReviewReason   string           `json:"review_reason,omitempty"`
	HookType       string           `json:"hook_type,omitempty"`
	DominantStrand string           `json:"dominant_strand,omitempty"`
	Feedback       *OutlineFeedback `json:"feedback,omitempty"`
	// 长篇分层信号
	ArcEnd         bool `json:"arc_end,omitempty"`
	VolumeEnd      bool `json:"volume_end,omitempty"`
	Volume         int  `json:"volume,omitempty"`
	Arc            int  `json:"arc,omitempty"`
	NeedsExpansion bool `json:"needs_expansion,omitempty"`  // 下一弧是骨架，需要展开章节
	NeedsNewVolume bool `json:"needs_new_volume,omitempty"` // 需要 Architect 创建下一卷
	NextVolume     int  `json:"next_volume,omitempty"`      // 下一弧/卷序号
	NextArc        int  `json:"next_arc,omitempty"`         // 下一弧序号
	// 完成态事实：本次 commit 后是否整本书已完成
	BookComplete bool `json:"book_complete,omitempty"`
	// 当前 Progress.Flow 快照（writing / reviewing / rewriting / polishing）
	Flow string `json:"flow,omitempty"`
}
