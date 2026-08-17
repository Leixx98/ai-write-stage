package imagejob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/voocel/ainovel-cli/internal/comfyui"
)

const DefaultPrompterTemplate = `你是小说插图提示词生成器。根据当前 writing unit 的计划与正文，为 ComfyUI 工作流填写图片参数。

保持人物外观、服饰、时代背景、地点和世界观连续。正向字段应包含主体、场景、动作、构图、镜头、光线、色彩和画面风格；负向字段应排除低质量、模糊、错误肢体、文字和水印等问题。

只输出一个 JSON object，字段名必须与下面的 JSON 完全一致。不要输出 Markdown、解释或 JSON 之外的任何文本。`

type PromptRequest struct {
	UnitID       string          `json:"unit_id"`
	Chapter      int             `json:"chapter"`
	Ordinal      int             `json:"ordinal"`
	ChapterTitle string          `json:"chapter_title"`
	UnitPlan     string          `json:"unit_plan"`
	UnitText     string          `json:"unit_text"`
	PreviousTail string          `json:"previous_tail,omitempty"`
	Schema       json.RawMessage `json:"schema"`
	SchemaHash   string          `json:"schema_hash"`
	SystemPrompt string          `json:"-"`
}

type Prompter interface {
	Generate(context.Context, PromptRequest) (string, error)
}

type PrompterFunc func(context.Context, PromptRequest) (string, error)

func (f PrompterFunc) Generate(ctx context.Context, request PromptRequest) (string, error) {
	return f(ctx, request)
}

// UserPrompt serializes bounded novel context. Field JSON lives in the system prompt.
func UserPrompt(request PromptRequest) (string, error) {
	payload := struct {
		UnitID       string `json:"unit_id"`
		Chapter      int    `json:"chapter"`
		Ordinal      int    `json:"ordinal"`
		ChapterTitle string `json:"chapter_title"`
		UnitPlan     string `json:"unit_plan"`
		UnitText     string `json:"unit_text"`
		PreviousTail string `json:"previous_tail,omitempty"`
	}{request.UnitID, request.Chapter, request.Ordinal, request.ChapterTitle, request.UnitPlan, request.UnitText, request.PreviousTail}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal Prompter request: %w", err)
	}
	return "请根据以下 writing unit 填写图片字段。只返回 JSON object：\n" + strings.TrimSpace(string(data)), nil
}

func FieldJSONObject(fields []comfyui.CanvasField) map[string]any {
	out := make(map[string]any, len(fields))
	for _, field := range fields {
		switch normalizedValueType(field.ValueType) {
		case "integer", "number":
			out[field.ID] = 0
		case "boolean":
			out[field.ID] = false
		default:
			out[field.ID] = ""
		}
	}
	return out
}

func ComposeSystemPrompt(template string, fields []comfyui.CanvasField) string {
	if strings.TrimSpace(template) == "" {
		template = DefaultPrompterTemplate
	}
	sample, _ := json.MarshalIndent(FieldJSONObject(fields), "", "  ")
	var notes strings.Builder
	for _, field := range fields {
		notes.WriteString(field.ID)
		notes.WriteString(": ")
		notes.WriteString(strings.TrimSpace(field.Note))
		notes.WriteByte('\n')
	}
	return strings.TrimSpace(template) + "\n\n## 输出 JSON\n" + string(sample) + "\n\n## 字段说明\n" + notes.String()
}

func PromptFingerprint(systemPrompt string) string {
	sum := sha256.Sum256([]byte(systemPrompt))
	return hex.EncodeToString(sum[:])
}
