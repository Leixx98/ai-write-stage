package play

import (
	"fmt"
	"strings"

	"github.com/voocel/agentcore/schema"
	"github.com/voocel/ainovel-cli/internal/llmcontract"
	"github.com/voocel/ainovel-cli/internal/store"
)

type ArchitectOutput struct {
	SegmentID            string `json:"segment_id"`
	Goal                 string `json:"goal"`
	CompleteAfterSegment bool   `json:"complete_after_segment"`
	Notes                string `json:"notes"`
}

func (o *ArchitectOutput) Validate() error {
	if strings.TrimSpace(o.SegmentID) == "" {
		return fmt.Errorf("segment_id is required")
	}
	if strings.TrimSpace(o.Goal) == "" {
		return fmt.Errorf("goal is required")
	}
	return nil
}

type PlannerOutput struct {
	SegmentID string               `json:"segment_id"`
	Cards     []store.PlayBeatCard `json:"cards"`
}

func (o *PlannerOutput) Validate() error {
	if strings.TrimSpace(o.SegmentID) == "" {
		return fmt.Errorf("segment_id is required")
	}
	if len(o.Cards) == 0 {
		return fmt.Errorf("cards cannot be empty")
	}
	if len(o.Cards) > 16 {
		return fmt.Errorf("cards exceed 16")
	}
	for i, card := range o.Cards {
		if err := validateCard(card, i == len(o.Cards)-1); err != nil {
			return fmt.Errorf("cards[%d]: %w", i, err)
		}
	}
	return nil
}

func validateCard(card store.PlayBeatCard, last bool) error {
	switch card.Kind {
	case store.BeatNarration, store.BeatDialogue, store.BeatInner, store.BeatChoice:
	default:
		return fmt.Errorf("invalid kind %q", card.Kind)
	}
	if card.CG != store.PlayCGNew && card.CG != store.PlayCGKeep {
		return fmt.Errorf("invalid cg %q", card.CG)
	}
	if card.Kind == store.BeatChoice {
		if !last {
			return fmt.Errorf("choice must be the last card")
		}
		if n := len(card.Choices); n < 2 || n > 3 {
			return fmt.Errorf("choice needs 2 or 3 options")
		}
		for _, choice := range card.Choices {
			if strings.TrimSpace(choice.ID) == "" || strings.TrimSpace(choice.Label) == "" {
				return fmt.Errorf("choice id and label are required")
			}
		}
		return nil
	}
	if len(card.Choices) > 0 {
		return fmt.Errorf("non-choice card cannot include choices")
	}
	return nil
}

type WriterOutput struct {
	Speaker string `json:"speaker"`
	Text    string `json:"text"`
}

func (o *WriterOutput) Validate() error {
	if strings.TrimSpace(o.Text) == "" {
		return fmt.Errorf("text is required")
	}
	return nil
}

var architectContract = llmcontract.Contract{
	Name:        "play_architect",
	Description: "Galgame 剧场下一段方向",
	Schema: schema.Object(
		schema.Property("segment_id", schema.String("本段短标识")).Required(),
		schema.Property("goal", schema.String("本段要推进到的目标")).Required(),
		schema.Property("complete_after_segment", schema.Bool("本段结束后是否收束全剧")).Required(),
		schema.Property("notes", schema.String("给分镜规划的备注，可空字符串")).Required(),
	),
}

var plannerContract = llmcontract.Contract{
	Name:        "play_planner",
	Description: "Galgame 剧场分镜卡",
	Schema: schema.Object(
		schema.Property("segment_id", schema.String("与规划师相同的段标识")).Required(),
		schema.Property("cards", schema.Array("按播放顺序的分镜卡", schema.Object(
			schema.Property("kind", schema.Enum("拍类型", "narration", "dialogue", "inner", "choice")).Required(),
			schema.Property("speaker", schema.String("说话人，旁白可空")).Required(),
			schema.Property("location", schema.String("地点")).Required(),
			schema.Property("time_of_day", schema.String("时间")).Required(),
			schema.Property("cg", schema.Enum("是否换图", "new", "keep")).Required(),
			schema.Property("cg_intent", schema.String("换图时的画面意图，keep 时为空")).Required(),
			schema.Property("required_beats", schema.Array("本拍必须落地的要点", schema.String("要点"))).Required(),
			schema.Property("choices", schema.Array("仅 choice 拍填写，其它拍省略", schema.Object(
				schema.Property("id", schema.String("选项 id")).Required(),
				schema.Property("label", schema.String("玩家可见文案")).Required(),
				schema.Property("consequence", schema.String("选后规划用的后果")).Required(),
			))),
		))).Required(),
	),
}

var writerContract = llmcontract.Contract{
	Name:        "play_writer",
	Description: "Galgame 剧场一屏正文",
	Schema: schema.Object(
		schema.Property("speaker", schema.String("说话人，旁白可空")).Required(),
		schema.Property("text", schema.String("这一屏要显示的全部文字")).Required(),
	),
}

func validatePlannerAgainstArchitect(plan PlannerOutput, arch ArchitectOutput) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(plan.SegmentID) != strings.TrimSpace(arch.SegmentID) {
		return fmt.Errorf("planner segment_id %q != architect %q", plan.SegmentID, arch.SegmentID)
	}
	last := plan.Cards[len(plan.Cards)-1]
	if arch.CompleteAfterSegment {
		if last.Kind == store.BeatChoice {
			return fmt.Errorf("complete segment cannot end with a choice")
		}
		return nil
	}
	if last.Kind != store.BeatChoice {
		return fmt.Errorf("open segment must end with a choice")
	}
	return nil
}
