// Package flow 实现垂类路由：Host 根据事实决定下一个调哪个子代理做什么。
//
// 设计原则：
//   - Route 是纯函数：输入 State，输出 *Instruction。无 IO、无 Store 调用，可单测。
//   - State 由 LoadState（非纯）从 Store 构造，一次性把路由需要的事实读齐。
//   - 返回 nil 是合法的：表示当前没有可由确定性事实推出的 Worker 指令；
//     Engine 再按终态、启动补裁或等待用户干预处理。
//
// Router 覆盖的是"查表型"决策（每章下一步、弧末后处理、队列驱动），
// 不覆盖"语义理解型"决策（选规划师、处理用户 Steer、输出总结）。
package flow

import (
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/domain"
	storepkg "github.com/voocel/ainovel-cli/internal/store"
)

// plannerForTier 从已落盘的规划级别推导规划师身份:short 归短篇规划师,
// mid/long 归长篇规划师(与启动 Arbiter 的选型口径一致)。
func plannerForTier(tier domain.PlanningTier) string {
	if tier == domain.PlanningTierShort {
		return "architect_short"
	}
	return "architect_long"
}

// Instruction 指示 Engine 下一步直接运行的 Worker 与任务。
type Instruction struct {
	Agent   string // architect_long / architect_short / chapter_planner / writer / editor
	Task    string // 给子代理的任务描述
	Reason  string // 路由理由（用于事件、日志与失败裁定）
	Chapter int    // writer 任务涉及的章节号（续写/重写/打磨）；0 表示不涉及（editor/architect 任务）
}

// State 是 Route 的输入：所有事实必须在此显式声明，禁止 Route 内部读 Store。
type State struct {
	Progress *domain.Progress

	// 上一个已完成章节（Progress.CompletedChapters 末尾）；为 0 表示尚未开始写作。
	LastCompleted int

	// 上一章的弧边界信息；IsArcEnd=false 时其他字段无意义。
	// 当 LastCompleted=0 或非 Layered 模式时应为 nil。
	ArcBoundary *storepkg.ArcBoundary

	// 基础设定缺项（规划阶段的补齐信号）。
	FoundationMissing []string

	// 已落盘的规划级别（save_foundation 落 scale 时写入 RunMeta）。
	// 空 = 首次规划尚未产出任何设定，规划师身份不可判定。
	PlanningTier domain.PlanningTier

	// 正常续写的下一章是否已有 Chapter Planner 落盘的执行计划。
	HasNextChapterPlan bool

	// 下一章基于计划和 unit 工件推导出的写作进度。计划不存在时为 nil；
	// 旧版无 units 的计划 TotalUnits=0，等待重新规划或人工迁移。
	NextChapterWriting *domain.WritingProgress
}

// Route 根据事实返回下一步确定性指令；返回 nil 由 Engine 按调用上下文处理。
//
// 决策优先级（互斥，自上而下匹配第一个）：
//
//  1. Phase=Complete        → nil（Host 确定性输出总结）
//
//  2. 规划期设定缺项且规划师可判定 → 同一规划师补齐；否则 nil（Engine 启动补裁）
//
//  3. PendingRewrites 非空  → writer 按队列重写/打磨
//
//  4. Flow=Steering         → nil（用户干预处理中）
//
//  5. 下一弧是骨架           → architect_long(expand_arc)
//
//  7. 卷末需决策下一卷       → architect_long(append_volume / complete_book)
//
//  8. 非分层大纲已耗尽       → architect(决定完结或续接大纲)
//
//  9. 下一章计划缺失         → chapter_planner(生成场景与 unit 计划)
//
// 10. 有待写 unit           → writer(只写一个唯一 unit)
// 11. units 全部完成        → nil（Engine 程序级自动提交）
func Route(s State) *Instruction {
	p := s.Progress
	if p == nil {
		return nil
	}

	// 1. 终态：Host 根据 store 事实生成确定性总结
	if p.Phase == domain.PhaseComplete {
		return nil
	}

	// 2. 规划期补齐：查表型决策——缺什么在 store，规划师身份从已落盘的 scale 推导
	//    （short → architect_short，其余 → architect_long）。tier 为空说明首次规划
	//    尚未落盘任何设定（选型是语义判断），由 Engine 的 planStartFallback 补裁。
	if p.Phase != domain.PhaseWriting {
		if len(s.FoundationMissing) > 0 && s.PlanningTier != "" {
			task := fmt.Sprintf("补齐基础设定缺项：%s（用 save_foundation 落盘对应 type）", strings.Join(s.FoundationMissing, "、"))
			if len(s.FoundationMissing) == 1 && s.FoundationMissing[0] == "foundation_audit" {
				task = "基础设定已齐全：重新调用 novel_context 读取全部已落盘工件与 foundation_status.fingerprint，审查跨文件语义一致性后调用 audit_foundation；有问题先修正并重新审查"
			}
			return &Instruction{
				Agent:  plannerForTier(s.PlanningTier),
				Task:   task,
				Reason: "基础设定缺项未齐，照缺项续派同一规划师",
			}
		}
		return nil
	}

	// 3. 重写/打磨队列优先（事实已在工具层落盘，Router 只照单派发）
	if len(p.PendingRewrites) > 0 {
		ch := p.PendingRewrites[0]
		verb := "重写"
		if p.Flow == domain.FlowPolishing {
			verb = "打磨"
		}
		return &Instruction{
			Agent:   "writer",
			Task:    fmt.Sprintf("%s第 %d 章", verb, ch),
			Reason:  fmt.Sprintf("PendingRewrites 队列剩余 %d 章", len(p.PendingRewrites)),
			Chapter: ch,
		}
	}

	// 4. 用户干预处理中：Arbiter 正在裁定，Engine 不抢占。旧项目残留的
	// Reviewing 会按普通 writing 继续，避免等待已删除的自动评审流程。
	if p.Flow == domain.FlowSteering {
		return nil
	}

	// 5-6. 分层模式只保留继续规划所必需的结构动作，不再做弧末评审或摘要。
	if p.Layered && s.ArcBoundary != nil && s.ArcBoundary.IsArcEnd {
		b := s.ArcBoundary
		switch {
		case b.NeedsExpansion && b.NextArc > 0:
			return &Instruction{
				Agent:  "architect_long",
				Task:   fmt.Sprintf("展开第 %d 卷第 %d 弧（save_foundation type=expand_arc）", b.NextVolume, b.NextArc),
				Reason: "下一弧骨架待展开",
			}
		case b.NeedsNewVolume:
			return &Instruction{
				Agent:  "architect_long",
				Task:   "创建下一卷：按完结判定清单评估后调用 save_foundation——故事继续 → type=append_volume；故事接近终点 → type=append_volume 且卷 JSON 顶层带 \"final\": true（收官卷，整卷收线，写完自动完结）；全部完结条件当下已满足 → type=complete_book。三选一均须附 reason 参数写明判定理由",
				Reason: "卷末需决定追加新卷、收官卷或结束全书",
			}
		}
	}

	// 8. 非分层大纲耗尽时不能继续派发越界章节。让 Architect 基于当前故事事实
	// 决定完结，或用 revise_outline 从 next 章续接计划。
	next := p.NextChapter()
	if next <= 0 {
		return nil
	}
	if !p.Layered && p.TotalChapters > 0 && next > p.TotalChapters {
		return &Instruction{
			Agent: plannerForTier(s.PlanningTier),
			Task: fmt.Sprintf(
				"非分层大纲已写完（已完成 %d 章，共 %d 章）：若故事已收束，调用 save_foundation(type=complete_book)；若仍需继续，用 revise_outline 从第 %d 章续接后续计划",
				len(p.CompletedChapters), p.TotalChapters, next,
			),
			Reason: "非分层大纲已耗尽，需决定完结或续接",
		}
	}

	// 9. 章节计划是云端 Planner 与本地 Writer 的显式交接工件。
	if !s.HasNextChapterPlan {
		return &Instruction{
			Agent:   "chapter_planner",
			Task:    fmt.Sprintf("为第 %d 章生成可执行章节计划：读取 novel_context(chapter=%d)，用 plan_chapter 保存 scenes 和符合当前 Writer 动态预算的 units；严格遵守工具 schema，不要写正文", next, next),
			Reason:  "下一章尚无场景与写作片段计划",
			Chapter: next,
		}
	}

	// 10-11. units 完成后由 Engine 自动提交；纯路由在该过渡态不派 LLM。
	if writing := s.NextChapterWriting; writing != nil && writing.TotalUnits > 0 {
		if writing.Complete {
			return nil
		}
		if writing.Next != nil {
			return &Instruction{
				Agent: "writer",
				Task: fmt.Sprintf("写第 %d 章 unit %s（%d/%d）", next,
					writing.Next.Unit.ID, writing.Next.Ordinal, writing.TotalUnits),
				Reason:  fmt.Sprintf("续写当前 writing unit %s", writing.Next.Unit.ID),
				Chapter: next,
			}
		}
	}

	// 旧版无 units 计划不再由 Writer 整章补写；等待重新规划或人工迁移。
	return nil
}
