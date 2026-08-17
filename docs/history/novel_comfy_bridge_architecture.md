# 小说 Unit 与 ComfyUI 桥接架构

本文定义第一版可实施架构：writing unit 正文落盘后，由 `Prompter` 按当前工作流暴露字段生成 JSON，经严格解析和字段绑定后，复用现有 ComfyUI 图片任务执行链路。实现目标是新增桥接层，不复制 `testJob`/`runJob`。

## 1. 边界与数据流

```text
write_chapter_unit 成功落盘
        |
        v
UnitImageHook.Enqueue(unit_id, chapter, ordinal)
        |  幂等创建 ImageJob(pending)
        v
internal/imagejob.Service
  load unit + workflow + canvas
  BuildPromptSchema(canvas)
        |
        v
Prompter.Generate(unit context + JSON Schema)
        |
        v
ParseAndValidate(raw JSON, schema, strict)
        |  PromptValues: field.id -> typed value
        v
BindAndStart(job, workflow, canvas, values)
        |  mergeCanvasRuntime + ApplyBindings
        v
ComfyExecutor.Start/Resume/Cancel
        |  Submit -> Wait -> Download
        v
drafts/<chapter>.units/<ordinal>.png + ImageJob(completed)
```

模块职责：

| 模块 | 职责 |
| --- | --- |
| `internal/agents` | 构造 `prompter` 模型调用器，使用当前激活的 Prompter 提示词和角色模型 |
| `internal/imagejob` | unit 上下文、动态 Schema、JSON 解析、幂等、状态机、严格推进门 |
| `internal/comfyui` | workflow/canvas 校验、字段绑定、远端提交/轮询/下载/中断 |
| `internal/store` | bridge 配置、ImageJob、Prompter 原始和解析结果、执行快照持久化 |
| `internal/entry/web` | DTO、统一 envelope、配置和诊断页面；不得直接调用模型或 ComfyUI |

## 2. 共享服务接口

HTTP 测试与自动 unit 生成必须调用同一服务，不能在 handler 中保留第二套绑定规则。

```go
type Prompter interface {
    Generate(ctx context.Context, req PromptRequest) (raw string, err error)
}

type Service interface {
    EnqueueForUnit(ctx context.Context, req UnitImageRequest) (store.ImageJob, bool, error)
    StartFromValues(ctx context.Context, req ValuesJobRequest) (store.ImageJob, error)
    Parse(ctx context.Context, workflowID, raw string, strict bool) (ParseResult, error)
    Get(ctx context.Context, jobID string) (store.ImageJob, error)
    Wait(ctx context.Context, jobID string) (store.ImageJob, error)
    Cancel(ctx context.Context, jobID string) error
    Retry(ctx context.Context, jobID string, regeneratePrompt bool) (store.ImageJob, error)
    ResumePending(ctx context.Context) error
}

type Executor interface {
    Start(ctx context.Context, job store.ImageJob, snapshot BoundSnapshot) error
    Resume(ctx context.Context, job store.ImageJob) error
    Cancel(ctx context.Context, job store.ImageJob) error
}
```

`POST /api/v2/comfyui/jobs/test` 改为组装 `ValuesJobRequest` 后调用 `StartFromValues`；现有 `runJob` 的状态机下移到 `Executor`。自动链路只在前面增加 Prompter 和 parser。

## 3. 动态 Prompter JSON Schema

### 3.1 字段身份

`CanvasField.ID` 是稳定逻辑键，同时用于：

- Prompter JSON property；
- `PromptValues` map key；
- `Binding.Key`。

`NodeID + Input` 只负责定位 ComfyUI 节点输入。前端字段编辑器应允许把逻辑键从旧值 `6::text` 改为 `positive_prompt`，但必须保证工作流内唯一，且只允许 `^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`。导入工作流的 node id、input 和 `class_type` 不得改写。

`CanvasField.Source` 第一版支持：

| source | 行为 |
| --- | --- |
| `prompter` | 放入动态 Schema，由模型返回 |
| `default` | 不放入 Schema，使用 canvas/workflow default |
| `runtime` | 不放入 Schema，只允许手工测试或受信任运行时传入 |

旧数据兼容：`source` 为空、`canvas` 或 `inferred` 时，`string + text/textarea` 归一化为 `prompter`，其他类型归一化为 `default`；下次保存写回显式值。

### 3.2 Schema 生成

只读取 `Exposed=true && Source=prompter` 的字段，按 `ID` 排序生成 `image_prompt_fields_v1`：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "ainovel://comfyui/workflows/wf-default/image-prompt/v1",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "negative_prompt": {"type":"string","description":"负向提示词"},
    "positive_prompt": {"type":"string","description":"正向提示词"}
  },
  "required": ["negative_prompt", "positive_prompt"]
}
```

所有 `source=prompter` 字段均为必返字段。固定值字段应改为 `source=default`，不要依靠模型省略。类型映射：`string -> string`、`integer -> integer`、`number -> number`、`boolean -> boolean`；`options` 映射为 `enum`，数值 `min/max` 映射为 `minimum/maximum`。`image`、数组、对象及未知类型不能作为 Prompter 字段，保存配置或生成 Schema 时返回校验错误。

Schema 用规范化 JSON 计算 SHA-256，保存为 `schema_hash`。Prompter system prompt 后追加机器生成约束：只输出一个 JSON object，不输出 Markdown、解释或 Schema 本身；字段说明来自 `Name`，字段名严格使用 `ID`。

### 3.3 Parser

Parser 使用结构化 JSON API（`json.Decoder.UseNumber`），只允许一个顶层 object，并拒绝空输出、尾随 token、重复 key、非有限数字和超出 1 MiB 的响应。可兼容剥离一层完整的 `````json ... ``` `` fence，但不从自然语言中搜索 JSON 片段。

- strict：拒绝未知字段、缺失字段、类型不匹配、enum/range 不合法；失败时不提交 ComfyUI。
- non-strict：忽略未知字段，并允许缺失字段用 canvas default 补齐；没有 default 的缺失字段仍失败。类型校验不降级，不做含糊字符串转换。

解析结果必须按 Schema 字段白名单生成新的 `PromptValues`，禁止直接把模型 map 传给 workflow。随后由 `mergeCanvasRuntime` 和 `ApplyBindings` 做第二次绑定校验。

## 4. Unit 输入与 Prompter 输出

```go
type PromptRequest struct {
    UnitID       string
    Chapter      int
    Ordinal      int
    ChapterTitle string
    UnitPlan     string
    UnitText     string
    PreviousTail string
    Schema       json.RawMessage
    SchemaHash   string
}
```

只发送当前 unit 正文、当前 unit 计划、章节名和有限 previous tail；不发送整个 ComfyUI API JSON。成功输出示例：

```json
{
  "positive_prompt": "cinematic fantasy scene, a swordswoman under rain...",
  "negative_prompt": "low quality, blurry, extra fingers"
}
```

激活的 Web 提示词组合中 `prompter` 必须进入 `assets.Bundle` 并被实际 Prompter 调用；不存在或为空时使用内置默认模板。与其他角色一致，组合在进程启动时形成快照，切换后重启生效。

## 5. ImageJob 与状态机

`store.ImageJob` 增加以下兼容字段：

```go
IdempotencyKey  string         `json:"idempotency_key,omitempty"`
Trigger         string         `json:"trigger,omitempty"` // unit|manual|test
SchemaHash      string         `json:"schema_hash,omitempty"`
WorkflowHash    string         `json:"workflow_hash,omitempty"`
PromptRaw       string         `json:"prompt_raw,omitempty"`
PromptValues    map[string]any `json:"prompt_values,omitempty"`
SnapshotKey     string         `json:"snapshot_key,omitempty"`
Recoverable     bool           `json:"recoverable,omitempty"`
```

状态：

```text
pending -> prompting -> validating -> binding -> submitting
        -> queued -> running -> completed
```

终态为 `failed`、`timeout`、`cancelled`。`PromptRaw` 仅用于诊断，不能直接进入 binding；`PromptValues` 在校验成功后、提交 ComfyUI 前落盘。绑定后的工作流快照保存到 `meta/images/jobs/<job_id>.workflow.json`，重试和恢复不因用户之后编辑工作流而漂移。

## 6. 幂等、重试与恢复

自动任务幂等键为以下规范化内容的 SHA-256：

```text
unit_id + unit_content_hash + workflow_id + workflow_hash + schema_hash + active_prompt_preset
```

- 同键 `completed`：直接返回已有任务和图片；不重复调用模型。
- 同键非终态：返回已有任务；不创建第二个 goroutine。
- 同键 `failed/timeout/cancelled`：默认返回旧任务，由用户显式 retry。
- 正文、工作流、Schema 或提示词组合变化会形成新键，允许生成新图。

普通 retry 默认复用已持久化 `PromptValues` 和绑定快照，不再次消耗模型；`regenerate_prompt=true` 才清除旧解析结果并重新调用 Prompter。

启动恢复：

| 持久状态 | 恢复行为 |
| --- | --- |
| `pending/prompting` 且无 `PromptValues` | 标记可恢复失败，等待显式重试，避免崩溃后重复扣费 |
| `validating/binding` 且已有 `PromptRaw/PromptValues` | 从已保存阶段继续，不重新调用模型 |
| `submitting` 且无 `prompt_id` | 使用 workflow snapshot 重新提交 |
| `queued/running` 且有 `prompt_id` | 查询 history；存在则继续等待/下载，不存在则标记可恢复失败 |
| `completed` | 校验图片存在；缺失则标记可恢复失败 |

## 7. 严格模式、取消与超时

Bridge 配置的 `strict` 同时控制 parser 和写作推进：

- strict：unit 落盘后创建任务，Engine 在开始下一个 unit 前等待当前图片 `completed`；任何 Prompter、parser、binding 或 ComfyUI 错误暂停运行并提示用户检查模型、Schema、工作流和 ComfyUI 环境。
- non-strict：任务异步执行，写作继续；失败仍持久化并广播，不能静默丢失。

取消使用单一 job context，覆盖 prompting、parsing 和 ComfyUI 轮询；已获得 `prompt_id` 后再 best-effort 调用对应实例 `/interrupt`。远端 interrupt 失败不能阻止本地保存 `cancelled`。`prompter_timeout_ms` 与 ComfyUI `timeout_ms` 分开；前者超时记 `failed` 且阶段为 `prompting`，后者记 `timeout`。取消或超时后不得继续执行后续 binding/submit。

## 8. 接入顺序

1. 抽取 `BindAndStart` 与 `Executor`，让现有 test/retry 先改用共享服务，保持行为不变。
2. 实现动态 Schema 与 parser 的纯函数和测试。
3. 注册 `prompter` role，扩展 ImageJob 持久化和自动任务服务。
4. 在 `write_chapter_unit` 成功落盘后调用幂等 hook，并接入 strict wait gate。
5. 增加 Web bridge 配置、Schema 预览、parser 诊断和 unit 图片状态。

第一版不允许浏览器直接调用模型/ComfyUI，不支持任意 JSONPath/脚本 parser，不自动改写导入工作流字段，也不在失败时猜测相近字段名。
