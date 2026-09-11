package guard

import (
	"context"
	"log/slog"
	"sync/atomic"

	"github.com/Leixx98/ai-write-stage/internal/store"
	"github.com/voocel/agentcore"
)

// subagentMaxConsecutiveBlocks 连续阻拦 N 次后升级为终止，避免弱模型死循环。
const subagentMaxConsecutiveBlocks = 3

// BlockHook synchronously reports every StopGuard block or escalation.
// Host forwards the facts to runtime events and external notifications so clients
// can distinguish recovery from repeated work. The callback never affects guard decisions.
// Reason values:
//   - "blocked"    已注入催促消息，模型将继续推进
//   - "escalated"  连续空转超限，本轮 run 终止交回上层
//   - "hard_stop"  provider 拒答（safety/content_filter），立即终止
type BlockHook func(agent, reason string, consecutive int32)

// hardStopReasons 是无法用催促消息恢复的 provider 端拒答原因。注入
// "必须 commit" 对它们无效，反而每次产生一次完整 LLM 调用的 token 消耗，
// 并最终升级 escalate 后让 Engine 重跑整个 Worker 任务，叠加多倍浪费
// （实测 ch02 撞 safety 时一次写章产生 3 次重派 17 次 LLM 调用、命中率
// 从 50% 跌到 2.8%）。
//
// 注意 StopReasonError / StopReasonAborted 不需要列入：agentcore 在
// loop.go 收到这两种 stop reason 时直接终止 run，根本不会调用 StopGuard。
// 这里只列那些会真正走到 StopGuard 的 provider 拒答语义。
var hardStopReasons = map[agentcore.StopReason]struct{}{
	"safety":         {},
	"content_filter": {},
}

// newCheckpointDeltaGuard 构造一个 StopGuard：
// 在 baseline 之后若未出现指定 step 的 checkpoint，则拒绝 end_turn。
// baseline 由调用方在 factory 时刻捕获，保证 per-run 语义正确。
//
// blockMsg 接收 baseline 之后已观测到的 checkpoint step 集合，按实际进度组装
// 催促消息——静态消息在"必需工具本身持续报错"的场景下是误导（催模型去调一个
// 正在失败的工具，见 #75）。
//
// 计数语义是"有进展即重置"：两次拦截之间出现过
// 任何新 checkpoint（重新 draft / check 等）视为模型在推进，consecutive 归零；
// 只有毫无产物的连续空转才累计并升级终止。
func newCheckpointDeltaGuard(st *store.Store, agentName string, requiredSteps []string, blockMsg func(seen map[string]struct{}) string, onBlock BlockHook) agentcore.StopGuard {
	var baseline int64
	if cp := st.Checkpoints.LatestGlobal(); cp != nil {
		baseline = cp.Seq
	}
	need := make(map[string]struct{}, len(requiredSteps))
	for _, s := range requiredSteps {
		need[s] = struct{}{}
	}
	var consecutive atomic.Int32
	var lastBlockSeq atomic.Int64 // 上次拦截时观测到的最新 checkpoint Seq；-1 表示尚未拦截过
	lastBlockSeq.Store(-1)
	return func(_ context.Context, info agentcore.StopInfo) agentcore.StopDecision {
		// 不可恢复错误：直接升级，不浪费一次催促。
		if _, hard := hardStopReasons[info.Message.StopReason]; hard {
			slog.Error("subagent stop_guard 检测到不可恢复停机，立即升级",
				"module", "agent.guard", "agent", agentName,
				"turn", info.TurnIndex, "stop_reason", info.Message.StopReason)
			if onBlock != nil {
				onBlock(agentName, "hard_stop", consecutive.Load())
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		// 倒序扫描 baseline 之后的 checkpoint，收集已出现的 step（放行判定 + 进度消息共用）。
		// 新 checkpoint 在尾部，遇到 <= baseline 即可 break。
		all := st.Checkpoints.All()
		latestSeq := baseline
		seen := make(map[string]struct{})
		for i := len(all) - 1; i >= 0; i-- {
			cp := all[i]
			if cp.Seq <= baseline {
				break
			}
			if cp.Seq > latestSeq {
				latestSeq = cp.Seq
			}
			seen[cp.Step] = struct{}{}
		}
		for s := range need {
			if _, ok := seen[s]; ok {
				consecutive.Store(0)
				return agentcore.StopDecision{Allow: true}
			}
		}
		// 上次拦截以来有新工件落盘 = 模型在推进（如被催后重新 draft 再试探收尾），
		// 重置计数；升级只应惩罚毫无进展的空转，而不是把整个 run 的拦截攒在一起报废。
		if prev := lastBlockSeq.Load(); prev >= 0 && latestSeq > prev {
			consecutive.Store(0)
		}
		lastBlockSeq.Store(latestSeq)
		n := consecutive.Add(1)
		if n > subagentMaxConsecutiveBlocks {
			slog.Error("subagent stop_guard 连续阻拦超限，升级为终止",
				"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
			if onBlock != nil {
				onBlock(agentName, "escalated", n)
			}
			return agentcore.StopDecision{Allow: false, Escalate: true}
		}
		slog.Warn("subagent stop_guard 拦截 end_turn",
			"module", "agent.guard", "agent", agentName, "turn", info.TurnIndex, "consecutive", n)
		if onBlock != nil {
			onBlock(agentName, "blocked", n)
		}
		return agentcore.StopDecision{Allow: false, InjectMessage: blockMsg(seen)}
	}
}

// staticBlockMsg 把固定文案适配成 blockMsg 签名（架构/编辑器的产物是单工具落盘，
// 不存在多步进度，静态催促即够）。
func staticBlockMsg(msg string) func(map[string]struct{}) string {
	return func(map[string]struct{}) string { return msg }
}

// NewWriterStopGuard requires exactly one persisted unit from every Writer run.
// Chapter submission is owned by the deterministic Engine after all units exist.
func NewWriterStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "writer", []string{"draft_unit"},
		staticBlockMsg("禁止结束：本轮必须只完成 working_memory.execution.current_unit，并调用 write_chapter_unit 落盘。不要写后续 unit；成功后程序会结束本轮并用新会话派发下一片段。"), onBlock)
}

// NewArchitectStopGuard 要求 architect 本轮至少落盘一次规划产物。
func NewArchitectStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "architect",
		[]string{
			"premise", "outline", "layered_outline", "characters", "world_rules",
			"foundation_audit", "expand_arc", "append_volume", "update_compass", "complete_book", "revise_outline",
		},
		staticBlockMsg("你必须调用 save_foundation、revise_outline 或 audit_foundation 将产出落盘后才能结束。只输出 Markdown/JSON 文字等于丢失。"),
		onBlock,
	)
}

// NewChapterPlannerStopGuard 要求章节规划师只在 plan 工件落盘后结束。
func NewChapterPlannerStopGuard(st *store.Store, onBlock BlockHook) agentcore.StopGuard {
	return newCheckpointDeltaGuard(st, "chapter_planner", []string{"plan"},
		staticBlockMsg("你必须调用 plan_chapter 保存带 scenes/units 的章节执行计划后才能结束；不要输出正文。"),
		onBlock,
	)
}
