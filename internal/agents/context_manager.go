package agents

import (
	"log/slog"

	"github.com/Leixx98/ai-write-stage/internal/bootstrap"
	"github.com/voocel/agentcore"
	corecontext "github.com/voocel/agentcore/context"
)

// contextManagerConfig 聚合 ContextManager 的全部配置参数。
type contextManagerConfig struct {
	Model            agentcore.ChatModel
	ContextWindow    int
	ReserveTokens    int
	Agent            string
	CommitProjected  bool
	Summary          *corecontext.FullSummaryConfig
	ToolMicrocompact *corecontext.ToolResultMicrocompactConfig
	ExtraStrategies  []corecontext.Strategy
}

func roleContextWindow(models *bootstrap.ModelSet, role string) int {
	if models == nil {
		return 0
	}
	provider, model, _ := models.CurrentSelection(role)
	window, _ := models.ResolveContextWindow(provider, model)
	return window
}

// writerContextBudgets scales deterministic compaction to the actual local
// model window. Fixed 20K/7K budgets cannot ever run inside an 8K endpoint.
func writerContextBudgets(window int) (keepRecent, summary int) {
	if window <= 0 {
		return 20000, 7000
	}
	threshold := window - bootstrap.CompactReserveTokens(window)
	if threshold < 2000 {
		threshold = 2000
	}
	keepRecent = min(20000, max(1000, threshold/2))
	summary = min(7000, max(800, threshold/3))
	return keepRecent, summary
}

func newContextManager(cfg contextManagerConfig) *corecontext.ContextEngine {
	var sc corecontext.FullSummaryConfig
	if cfg.Summary != nil {
		sc = *cfg.Summary
	}
	sc.Model = cfg.Model

	var tc corecontext.ToolResultMicrocompactConfig
	if cfg.ToolMicrocompact != nil {
		tc = *cfg.ToolMicrocompact
	}

	strategies := []corecontext.Strategy{
		corecontext.NewToolResultMicrocompact(tc),
	}
	strategies = append(strategies, cfg.ExtraStrategies...)
	strategies = append(strategies, corecontext.NewFullSummary(sc))

	var commitStrategies []string
	if cfg.CommitProjected {
		commitStrategies = make([]string, len(strategies))
		for i, strategy := range strategies {
			commitStrategies[i] = strategy.Name()
		}
	}

	engine := corecontext.NewEngine(corecontext.EngineConfig{
		ContextWindow:    cfg.ContextWindow,
		ReserveTokens:    cfg.ReserveTokens,
		CommitStrategies: commitStrategies,
		Strategies:       strategies,
	})

	callback := contextRewriteCallback(cfg.Agent)
	engine.SetProjectHook(callback)
	engine.SetRecoverHook(callback)
	return engine
}

// contextRewriteCallback 创建上下文重写的日志回调。
// 新架构简化为只写 slog,不再写 runtime queue 和 UIEvent。
func contextRewriteCallback(agent string) func(corecontext.RewriteEvent) {
	return func(ev corecontext.RewriteEvent) {
		attrs := []any{
			"module", "context",
			"agent", agent,
			"reason", ev.Reason,
			"strategy", ev.Strategy,
			"committed", ev.Committed,
			"tokens_before", ev.TokensBefore,
			"tokens_after", ev.TokensAfter,
		}
		if info := ev.Info; info != nil {
			attrs = append(attrs,
				"msgs_before", info.MessagesBefore,
				"msgs_after", info.MessagesAfter,
				"compacted", info.CompactedCount,
				"kept", info.KeptCount,
				"duration_ms", info.Duration.Milliseconds(),
			)
		}
		slog.Warn("上下文重写", attrs...)
	}
}
