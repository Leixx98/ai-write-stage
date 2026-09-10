package models

// localModelOverrides 是官方 API 已上线、但 OpenRouter 基线尚未收录的模型。
// NewModelRegistry 在 generatedModels 之后合并：同 provider+id 覆盖价格/窗口，新 id 追加。
var localModelOverrides = []ModelEntry{
	{
		Provider: "deepseek", ID: "deepseek-flash", Name: "DeepSeek Flash",
		ContextWindow: 1048576, MaxTokens: 384000,
		InputCostPer1M: 0.15, OutputCostPer1M: 0.6,
		CacheReadCostPer1M: 0.003, CacheWriteCostPer1M: 0,
	},
	{
		Provider: "deepseek", ID: "deepseek-v4-flash", Name: "DeepSeek V4 Flash",
		ContextWindow: 1048576, MaxTokens: 384000,
		InputCostPer1M: 0.15, OutputCostPer1M: 0.6,
		CacheReadCostPer1M: 0.003, CacheWriteCostPer1M: 0,
	},
}
