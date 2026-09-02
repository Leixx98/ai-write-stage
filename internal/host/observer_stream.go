package host

import (
	"time"

	"github.com/voocel/agentcore"
)

// handleSubagentDelta 分流 subagent 的文本与工具调用参数：
// - DeltaText 直接作为 markdown 流出
// - DeltaToolCall 只对已知的长内容工具（如 draft_chapter.content）抽取字段流出；其他工具的参数 JSON 全部丢弃
func (o *observer) handleSubagentDelta(p *agentcore.ProgressPayload) {
	if p.DeltaKind != agentcore.DeltaToolCall {
		o.emitStreamDelta(p.Delta, false)
		return
	}
	if p.Tool == "" {
		return // 工具名未就绪，下一个 delta 再试
	}

	// 流式识别到工具名时提前发 TOOL 进行中事件，让 spinner 覆盖整段 LLM 生成期间
	// （否则 draft_chapter 这类工具的"进行中"只在真实 Execute 的几十毫秒里显示）。
	// 真正的 ProgressToolStart 到来时识别到 toolStarts 已有记录，只会补齐 summary。
	o.ensureSubagentToolStarted(p.Agent, p.Tool)
	o.updateToolCallSummaryFromDelta(p.Agent, p.Tool, p.Delta)

	cur, ok := o.streamExtractors[p.Agent]
	// 同工具调用 args 已闭合（顶层 } 命中）后，仍可能收到 trailing delta：
	// 某些 provider（deepseek-v4-flash 实测）会把单次 args 拆成多个 chunk，
	// 最末一个 chunk 在 `}` 之后还跟着空白或重复字符。此时若按"工具名匹配 +
	// Done 即重建"处理，新 extractor 又会 emit 一次 header 并把尾段 token
	// 当作新 args 解析。这些 delta 是冗余尾巴，丢弃即可。
	if ok && cur.tool == p.Tool && cur.ext.Done() {
		return
	}
	// 工具名变了或还没建过：新建。
	if !ok || cur.tool != p.Tool {
		ext := newToolExtractor(p.Tool)
		if ext == nil {
			delete(o.streamExtractors, p.Agent)
			return
		}
		cur = &agentExtractor{tool: p.Tool, ext: ext}
		o.streamExtractors[p.Agent] = cur
	}
	if emitted := cur.ext.Feed(p.Delta); emitted != "" {
		if !cur.emittedAny {
			cur.emittedAny = true
			o.streamClear()
			o.streamExtractors[p.Agent] = cur
			if header := cur.ext.Header(); header != "" {
				o.emitS(StreamEvent{Kind: StreamEventTool, Tool: header})
			}
		}
		o.emitStreamDelta(emitted, false)
	} else if cur.ext.Done() && !cur.emittedAny {
		cur.emittedAny = true
		if header := cur.ext.Header(); header != "" {
			o.streamClear()
			o.streamExtractors[p.Agent] = cur
			o.emitS(StreamEvent{Kind: StreamEventTool, Tool: header})
		}
	}
}

func (o *observer) emitStreamDelta(delta string, thinking bool) {
	if delta == "" {
		return
	}
	kind := StreamEventText
	if thinking {
		kind = StreamEventThinking
	}
	o.emitS(StreamEvent{Kind: kind, Text: delta})
}

// ensureSubagentToolStarted 在流式识别到 tool_call 首次出现时，提前为该 agent
// 登记一次进行中的 TOOL 调用，使事件流的 spinner 覆盖"LLM 流式生成 tool_call
// 参数"这一段时间（通常占调用总耗时的 99%）。args 此时尚不完整，暂以纯工具名
// 为 summary；等真正的 ProgressToolStart 到来时会补齐带参数的 summary。
func (o *observer) ensureSubagentToolStarted(agent, tool string) {
	if agent == "" || tool == "" {
		return
	}
	if _, ok := o.toolStarts[agent]; ok {
		return // 已有进行中调用，幂等
	}
	o.resetStreamArgLabel(agent, tool)
	id := nextEventID()
	o.toolStarts[agent] = &activeCall{
		id:      id,
		start:   time.Now(),
		summary: tool, // 先用纯工具名，ProgressToolStart 到来时可能更新为 tool(第N章)
		depth:   1,
	}
	o.emitAndLog(Event{
		ID:       id,
		Time:     time.Now(),
		Category: "TOOL",
		Agent:    agent,
		Summary:  tool,
		Level:    "info",
		Depth:    1,
	})
	o.updateAgent(agent, func(a *agentState) {
		a.state = "working"
		a.tool = tool
	})
	o.emitFallbackStreamHeader(tool)
}

func (o *observer) resetStreamArgLabel(agent, tool string) {
	key := streamArgKey(agent, tool)
	delete(o.streamArgPrefixes, key)
	delete(o.streamArgLabels, key)
}

// emitFallbackStreamHeader 给未配置 extractor 的工具补一行标题到流面板。
// 两条路径都要调用以保证一致：
//  1. ensureSubagentToolStarted —— subagent 流式 tool args（DeltaToolCall）
//  2. handleToolUpdate ProgressToolStart —— subagent 非流式 tool args
//
// 缺任何一条，流式与非流式模型的工具标题就会表现不一致。
func (o *observer) emitFallbackStreamHeader(tool string) {
	if _, has := toolDisplays[tool]; has {
		return // 有 extractor，header 由 extractor 自行输出
	}
	o.streamClear()
	o.emitS(StreamEvent{Kind: StreamEventTool, Tool: streamHeaderFallback(tool)})
}

// streamHeaderFallback 为未配置 extractor 的工具生成流式 header 文本，
// 让用户即使对轻量读取类工具也能看到"在调用什么"。
func streamHeaderFallback(tool string) string {
	return tool
}

// streamClear 通知消费者开启新一轮 streamRound，并清理上一轮抽取状态。
func (o *observer) streamClear() {
	o.emitS(StreamEvent{Kind: StreamEventClear})
	// 上一轮的 subagent 结束前 ProgressToolEnd 已 delete，这里防御性清空。
	if len(o.streamExtractors) > 0 {
		o.streamExtractors = make(map[string]*agentExtractor)
	}
}
