package play

import (
	"fmt"
	"strings"

	"github.com/Leixx98/ai-write-stage/internal/llmcontract"
	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore/schema"
)

type SpineOutput struct {
	Throughline string              `json:"throughline"`
	Stations    []store.PlayStation `json:"stations"`
	Threads     []store.PlayThread  `json:"threads"`
}

func (o *SpineOutput) Validate() error {
	if o == nil {
		return fmt.Errorf("spine is required")
	}
	return validateSpine(prepareSpine(*o))
}

type ReviseNextOutput struct {
	NextStation store.PlayStation `json:"next_station"`
	Threads     []store.PlayThread `json:"threads"`
}

func (o *ReviseNextOutput) Validate() error {
	if o == nil {
		return fmt.Errorf("revise output is required")
	}
	if err := validateStationDetail(o.NextStation, 0); err != nil {
		return fmt.Errorf("next_station: %w", err)
	}
	return validateThreadUpdates(o.Threads)
}

type ReplanOutput struct {
	Throughline string              `json:"throughline"`
	Stations    []store.PlayStation `json:"stations"`
	Threads     []store.PlayThread  `json:"threads"`
}

func (o *ReplanOutput) Validate() error {
	if o == nil {
		return fmt.Errorf("replan output is required")
	}
	if len(o.Stations) == 0 {
		return fmt.Errorf("stations cannot be empty")
	}
	if len(o.Stations) > maxSpineStations {
		return fmt.Errorf("stations exceed %d", maxSpineStations)
	}
	seen := map[string]bool{}
	for i, station := range o.Stations {
		if err := validateStationDetail(station, i); err != nil {
			return err
		}
		id := strings.TrimSpace(station.ID)
		if seen[id] {
			return fmt.Errorf("stations[%d]: duplicate id %q", i, id)
		}
		seen[id] = true
	}
	return validateThreads(o.Threads)
}

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
	o.Cards = repairPlannerCards(o.Cards, false)
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
			if len(normalizeFacts(choice.SetFacts)) == 0 {
				return fmt.Errorf("choice %q needs set_facts", choice.ID)
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

func playStationSchema() map[string]any {
	return schema.Object(
		schema.Property("id", schema.String("短英文或拼音标识")).Required(),
		schema.Property("title", schema.String("本站短标题")).Required(),
		schema.Property("pressure", schema.String("这一站必须碰到的压力，不是剧情答案")).Required(),
		schema.Property("summary", schema.String("2到4句细纲：场景、冲突、转向，不写死玩家选项")).Required(),
		schema.Property("must_happen", schema.Array("无论怎么选都要碰到的事件", schema.String("事件"))).Required(),
		schema.Property("forks", schema.Array("2到3个本地分叉", schema.Object(
			schema.Property("if_facts", schema.Array("触发该分叉的已有事实 id，可空", schema.String("事实 id"))).Required(),
			schema.Property("tint", schema.String("本站或下一站如何染色")).Required(),
		))).Required(),
		schema.Property("seeds", schema.Array("本站要埋的伏笔 id", schema.String("伏笔 id"))).Required(),
		schema.Property("payoffs", schema.Array("本站可回收的伏笔 id", schema.String("伏笔 id"))).Required(),
	)
}

func playThreadSchema() map[string]any {
	return schema.Object(
		schema.Property("id", schema.String("伏笔短 id")).Required(),
		schema.Property("hint", schema.String("一句话伏笔")).Required(),
		schema.Property("status", schema.Enum("伏笔状态", "open", "planted", "paid", "dropped")).Required(),
		schema.Property("plant_at", schema.String("建议埋点站 id，可空")).Required(),
		schema.Property("payoff_at", schema.String("建议回收站 id，可空")).Required(),
	)
}

var spineContract = llmcontract.Contract{
	Name:        "play_spine",
	Description: "Galgame 剧场本局大纲",
	Schema: schema.Object(
		schema.Property("throughline", schema.String("本局终局要兑现的一句话")).Required(),
		schema.Property("stations", schema.Array("本局 3 到 6 站细纲", playStationSchema())).Required(),
		schema.Property("threads", schema.Array("跨站伏笔台账", playThreadSchema())).Required(),
	),
}

var reviseContract = llmcontract.Contract{
	Name:        "play_revise",
	Description: "Galgame 剧场选择后轻改下一站",
	Schema: schema.Object(
		schema.Property("next_station", playStationSchema()).Required(),
		schema.Property("threads", schema.Array("有变化的伏笔", playThreadSchema())).Required(),
	),
}

var replanContract = llmcontract.Contract{
	Name:        "play_replan",
	Description: "Galgame 剧场按方向改后续细纲",
	Schema: schema.Object(
		schema.Property("throughline", schema.String("按新方向改写的一句话承诺")).Required(),
		schema.Property("stations", schema.Array("后续站细纲", playStationSchema())).Required(),
		schema.Property("threads", schema.Array("完整伏笔台账", playThreadSchema())).Required(),
	),
}

var architectContract = llmcontract.Contract{
	Name:        "play_architect",
	Description: "Galgame 剧场当前站方向",
	Schema: schema.Object(
		schema.Property("segment_id", schema.String("必须等于当前站 id")).Required(),
		schema.Property("goal", schema.String("本站要推进到的目标")).Required(),
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
				schema.Property("consequence", schema.String("给人看的后果，可空")).Required(),
				schema.Property("set_facts", schema.Array("选后写入账本的短 id", schema.String("事实 id"))).Required(),
				schema.Property("ending", schema.Bool("选后是否直接收束全剧")).Required(),
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

func validatePlannerAgainstArchitect(plan PlannerOutput, arch ArchitectOutput, lastStation bool, pacing store.PlayPacing) error {
	if err := plan.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(plan.SegmentID) != strings.TrimSpace(arch.SegmentID) {
		return fmt.Errorf("planner segment_id %q != architect %q", plan.SegmentID, arch.SegmentID)
	}
	pure := store.NormalizePlayPacing(string(pacing)) == store.PlayPacingPure
	for i, card := range plan.Cards {
		if card.Kind == store.BeatChoice && pure {
			return fmt.Errorf("cards[%d]: pure pacing cannot include choice", i)
		}
	}
	if lastStation || pure {
		return nil
	}
	if plan.Cards[len(plan.Cards)-1].Kind != store.BeatChoice {
		return fmt.Errorf("open station must end with a choice")
	}
	return nil
}

func repairPlannerCards(cards []store.PlayBeatCard, fillEmptyCG bool) []store.PlayBeatCard {
	for i := range cards {
		if string(cards[i].CG) == "choice" {
			if cards[i].Kind != store.BeatChoice {
				cards[i].Kind = store.BeatChoice
			}
			cards[i].CG = store.PlayCGKeep
		}
		if cards[i].Kind == store.BeatChoice && cards[i].CG != store.PlayCGNew && cards[i].CG != store.PlayCGKeep {
			cards[i].CG = store.PlayCGKeep
		}
		if fillEmptyCG && cards[i].CG == "" {
			cards[i].CG = store.PlayCGKeep
		}
		if cards[i].Kind == store.BeatChoice {
			for j := range cards[i].Choices {
				cards[i].Choices[j].SetFacts = normalizeFacts(cards[i].Choices[j].SetFacts)
			}
		}
	}
	return cards
}

func validateCardCount(n int, profile playProfile) error {
	if n < profile.MinCards || n > profile.MaxCards {
		return fmt.Errorf("cards=%d not in %d..%d", n, profile.MinCards, profile.MaxCards)
	}
	return nil
}
