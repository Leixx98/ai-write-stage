package play

import "github.com/Leixx98/ai-write-stage/internal/store"

type densityProfile struct {
	MinCards    int
	MaxCards    int
	RecentBeats int
	WriterTurns int
	FillEmptyCG bool
	PromptHint  string
}

func profileFor(density store.PlayDensity) densityProfile {
	if store.NormalizePlayDensity(string(density)) == store.PlayDensityRich {
		return densityProfile{
			MinCards:    5,
			MaxCards:    10,
			RecentBeats: 8,
			WriterTurns: 20,
			FillEmptyCG: false,
			PromptHint:  "本局档位是丰满：本段 5～10 拍，notes 写清冲突与选项后果。选项必须带 set_facts。不要宣布全剧结束。",
		}
	}
	return densityProfile{
		MinCards:    3,
		MaxCards:    5,
		RecentBeats: 4,
		WriterTurns: 6,
		FillEmptyCG: true,
		PromptHint:  "本局档位是精简：本段 3～5 拍，少写气氛。选项必须带短 id 的 set_facts。不要宣布全剧结束。",
	}
}
