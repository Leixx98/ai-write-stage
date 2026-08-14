package imagejob

import "strings"

const (
	PresetDanbooru = "danbooru"
	PresetSDXL     = "sdxl"
	PresetZImage   = "zimage"
	PresetNatural  = "natural"
)

const danbooruPrompterTemplate = `你是小说插图提示词生成器。当前工作流使用 Illustrious、NoobAI、Pony 或同类 SDXL 动漫模型，正向提示词必须写成 Danbooru tag。

规则：
- 使用英文、小写、逗号分隔的 tag；不要写自然语言句子，不要用中文 tag。
- 顺序：人数与主体（如 1girl, solo）→ 外观与服饰 → 姿态与动作 → 场景与背景 → 构图与镜头 → 光线与氛围 → 画质 tag。
- 画质 tag 使用：masterpiece, best quality, amazing quality, newest, absurdres, very aesthetic, highly detailed。
- 负向字段使用：worst quality, low quality, normal quality, bad anatomy, extra digits, extra limbs, watermark, text, jpeg artifacts, blurry。
- 不要使用 (word:1.2) 权重语法，除非字段说明明确要求。保持人物外观、服饰、时代、地点与世界观连续。

只输出一个 JSON object，字段名必须与下面的 JSON 完全一致。不要输出 Markdown、解释或 JSON 之外的任何文本。`

const sdxlPrompterTemplate = `你是小说插图提示词生成器。当前工作流使用 SDXL 通用或写实模型。

规则：
- 正向字段用英文自然语言描述主体、场景、动作、构图、镜头、光线、材质和色彩，可夹少量质量关键词。
- 质量关键词可用：cinematic lighting, highly detailed, sharp focus, 8k；仅当画面需要写实时才加 photorealistic。
- 负向字段排除：low quality, blurry, deformed, extra limbs, watermark, text, jpeg artifacts。
- 不要写成 Danbooru tag 列表（不要 1girl, masterpiece, score_9 这种堆叠）。保持人物外观、服饰、时代和地点连续。

只输出一个 JSON object，字段名必须与下面的 JSON 完全一致。不要输出 Markdown、解释或 JSON 之外的任何文本。`

const zimagePrompterTemplate = `你是小说插图提示词生成器。当前工作流使用 Z-Image / Z-Image Turbo。该模型按自然语言句子理解提示词，不要写成 Danbooru 或 SDXL tag。

规则：
- 正向字段写成完整英文描述句，按「主体与动作 → 外貌服饰 → 构图镜头 → 光线氛围 → 风格媒介」组织。也可用通顺的中英混合，但主体细节优先用英文。
- 使用摄影或导演语言：medium shot, 85mm, soft sidelight, analog film, skin texture。不要堆 masterpiece、best quality、1girl、score_9 这类 tag。
- Z-Image Turbo 对负向提示词几乎无效。必须把约束写进正向描述，例如 no watermark, no extra text, correct anatomy, no extra limbs。
- 最重要的主体放在句子开头。避免互相矛盾的风格指令。保持人物外观、服饰、时代和地点连续。

只输出一个 JSON object，字段名必须与下面的 JSON 完全一致。不要输出 Markdown、解释或 JSON 之外的任何文本。`

type PrompterPreset struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Template    string `json:"template"`
}

func PrompterPresets() []PrompterPreset {
	return []PrompterPreset{
		{ID: PresetDanbooru, Label: "Illustrious / SDXL 动漫（Danbooru）", Description: "逗号分隔的英文 Danbooru tag，适合 Illustrious、NoobAI、Pony 等动漫模型。", Template: danbooruPrompterTemplate},
		{ID: PresetSDXL, Label: "SDXL 通用 / 写实", Description: "英文自然语言加少量质量词，适合 SDXL 底模和写实微调。", Template: sdxlPrompterTemplate},
		{ID: PresetZImage, Label: "Z-Image / Z-Image Turbo", Description: "完整描述句，约束写进正向提示词；不要用 tag 堆叠。", Template: zimagePrompterTemplate},
		{ID: PresetNatural, Label: "通用自然语言", Description: "不绑定特定模型的电影感描述，适合尚未确定模型或混合工作流。", Template: DefaultPrompterTemplate},
	}
}

func NormalizePrompterPreset(presetID string) string {
	id := strings.ToLower(strings.TrimSpace(presetID))
	for _, preset := range PrompterPresets() {
		if preset.ID == id {
			return id
		}
	}
	return PresetNatural
}

func ResolvePrompterTemplate(presetID, custom string) string {
	if template := strings.TrimSpace(custom); template != "" {
		return template
	}
	id := NormalizePrompterPreset(presetID)
	for _, preset := range PrompterPresets() {
		if preset.ID == id {
			return preset.Template
		}
	}
	return DefaultPrompterTemplate
}
