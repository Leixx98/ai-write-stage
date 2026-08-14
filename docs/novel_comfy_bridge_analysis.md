# Unit 正文到 ComfyUI 图片任务：桥接分析

本文只分析现有代码和推荐接入方式，不修改业务实现。目标链路是：一个已落盘的 writing unit 正文交给 `Prompter`，得到可校验的 JSON；把 JSON 映射到已导入的 ComfyUI API workflow；最后复用现有图片任务执行器提交、轮询并保存图片。

## 结论

ComfyUI 的最后一公里已经存在于 `internal/entry/web/v2.go`：`testJob` 负责组装输入、绑定字段和创建 `store.ImageJob`，`runJob` 负责调用 ComfyUI `/prompt`、`/history`、`/view` 并持久化状态。缺少的是“unit 完成后自动调用 Prompter，并把结构化结果转换成 `testJob` 等价请求”的服务层。`internal/agents/build.go` 当前只构造 architect、chapter_planner、writer 三类 worker，没有构造或注册 `prompter` worker；`Host`/`engine` 也没有图片任务回调。

建议新增一个位于 Host 与 ComfyUI 之间的 Image/Prompter service（文档中的 `internal/imagejob` 方向），由 unit 完成事件触发。HTTP `testJob` 继续作为手工测试入口，自动链路应调用同一套“解析、校验、绑定、提交”核心函数，而不是通过 HTTP 回环。

## 现有代码路径

### `testJob`：输入合并、画布投影和绑定

`internal/entry/web/v2.go:1266` 的 `testJob` 执行顺序如下：

1. 读取并归一化 ComfyUI config 和 workflow，加载对应 canvas；必要时按实例队列选择 `BaseURL`。
2. 用 `comfyui.CanvasValues(canvas, req.FieldValues, req.MiniTestValues)` 建立字段值。随后补入 `parameters`，再把 `prompt`/`negative_prompt` 映射为便利别名，最后填充 canvas 默认值。显式 `field_values` 优先于其他来源。
3. `mergeCanvasRuntime`（`v2.go:1194`）只把暴露的 canvas 字段投影成 `Binding{Key, NodeID, Path:"inputs."+Input, Type}`，同时更新 workflow/config 的 bindings/defaults。它还解析唯一 input 名称和 prompt 别名；同名 input 多个拥有者时会拒绝有歧义的别名。
4. `comfyui.ApplyBindings`（`v2.go:1364`）返回一个深拷贝后的 API workflow，不修改持久化模板；缺失 required binding、类型转换失败或路径无效都会返回错误。
5. 保存 `store.ImageJob`（包含 `UnitID`、章节/序号、prompt、negative prompt、parameters、inputs、workflow/instance）后启动 `go c.runJob(...)`，HTTP 返回 `202`。

注意：`testJob` 中 `Mode` 当前未使用；`parameters` 的最后一个循环（约 `v2.go:1369`）是空操作，真正生效的参数必须已经落入 binding key 或默认值。

### `runJob`：提交、轮询、下载和终态

`internal/entry/web/v2.go:1387` 的 `runJob` 创建 `comfyui.HTTPClient`，以 `cfg.Timeout()` 包裹 context，然后依次：

`pending -> submitting -> Submit -> queued -> Wait -> running -> Download -> completed`。

每一步都会尽力 `SaveJob`。成功图片写入测试路径 `meta/images/tests/<job>.png`，有章节和 ordinal 时写入 `drafts/%02d.units/%03d.png`，并生成浏览器可用的 `/api/v2/comfyui/jobs/{id}/image` URL。超时会调用 `/interrupt`（best effort）并标记 `timeout`；本地取消标记 `cancelled`；其他错误标记 `failed`，最终写入 `FinishedAt`。

重试入口 `v2.go:1645` 会从旧 job 重建 prompt/parameters/inputs，重新执行 `mergeCanvasRuntime` 和 `ApplyBindings`，再启动同一个 `runJob` 状态机。因此自动链路应保存足够的原始输入，确保重试不需要再次调用 Prompter（除非明确选择重新生成提示词）。

## ComfyUI 校验和字段映射

`internal/comfyui/workflow.go:9` 的 `ValidateWorkflow` 检查 workflow ID、API JSON 节点（每个节点必须有 `class_type` 和 object 类型 `inputs`）、binding 所引用的节点和路径、支持的类型（`string`/`integer`/`number`/`boolean`）以及 output 节点。

`ApplyBindings`（`workflow.go:80`）先验证，再通过 JSON marshal/unmarshal 深拷贝 workflow。每个 binding 按以下优先级取值：`values[b.Key]`，否则 `Defaults[b.Key]`；required 且仍缺失则失败。`convertValue` 对整数、数字、布尔值执行严格转换；`setPath` 支持对象 key 和非负数组下标，拒绝越界或穿过标量。

Prompter 结果不应直接当作 ComfyUI workflow。推荐先规范化为内部值：

```json
{
  "prompt": "正向中文提示词",
  "negative_prompt": "low quality, blurry",
  "tags": ["fantasy"],
  "seed": 123
}
```

再构造 `values`：`positive_prompt <- prompt`、`negative_prompt <- negative_prompt`、`seed <- seed`，把固定的 width/height/steps/cfg 等运行参数作为显式参数或 workflow defaults。若 canvas 使用自定义字段 ID，应优先写入该 ID；只有唯一 input 拥有者时才依赖 `mergeCanvasRuntime` 的 input 别名。绑定前应再次运行 `ValidateWorkflow`，绑定后可检查 output spec 能否从历史结果中取到图片。

## Prompter 接入建议

### 输入边界

只向 Prompter 提供当前 unit 和必要上下文：chapter title、unit plan、unit text、人物/设定摘要、上一 unit 尾部。不要把整个会话或 ComfyUI JSON 塞进提示词。`docs/api_contracts.md:134-137` 已约定上述输入和 `prompt/negative_prompt/tags/seed` 输出形状；`v2.go:237` 的默认 `prompter` 文案也要求只输出图片提示词，但自动桥接应升级为严格 JSON 输出约束。

### Worker/模型

`internal/agents/build.go:103-115` 的 `BuildWorkers` 目前只建立 architect、chapter planner、writer tools；`subagent.NewRunner`（约 267 行）也只注册这四个 config。`agentToRole` 对未知名称会原样返回，因此 `prompter` 角色名本身可作为模型选择 key，但仍需增加独立 config、system prompt、结构化输出解析和 stop 条件。模型选择应沿用 `models.ForRoleWithFailover`、`CurrentSelection` 和现有 usage/session logger；不要在 Web handler 中直接创建 LLM。

Prompter 可以是一次性 service 调用，也可以是 runner 中的无工具 worker。无论实现形式，都必须：

- 解析模型输出时只接受一个 JSON object（可先剥离 markdown fence，但不能静默接受自然语言）；
- 校验 `prompt` 非空、长度上限和字符串类型；`negative_prompt` 可选；`tags` 必须为字符串数组；`seed` 只接受整数；
- 对未知字段选择拒绝或明确保留，记录原始响应和 schema 版本；
- 失败时不提交 ComfyUI，job 进入 `failed`/`prompting_failed`，并保留可重试输入。

## 推荐自动链路

```text
writer.write_chapter_unit 成功落盘
        |
        v
Host/engine unit-completed 事件（带 unit_id、chapter、ordinal）
        |
        v
ImageJobService.CreateForUnit
  1. 读取 unit 正文和最小上下文
  2. 调 Prompter，解析 image_prompt_v1 JSON
  3. 加载 config/workflow/canvas，Normalize + ValidateWorkflow
  4. JSON -> values（prompt/negative_prompt/seed + defaults）
  5. mergeCanvasRuntime + ApplyBindings
  6. 保存 prompting/submitting 前的 ImageJob（幂等键 unit_id + workflow_id）
  7. 复用 runJob 核心：Submit -> Wait -> Download -> SaveJob
```

触发点应在 writer 的 `write_chapter_unit` 工具成功之后。当前 `engine.runWorker`（`internal/host/engine.go:470-493`）只运行 worker 并向 observer 报告完成；章节完成后 `autoCommit`（`engine.go:179-194`）直接合并提交，未发送图片事件。因此建议在 writer 成功返回或 unit 提交边界增加 Host 回调/事件 sink，由 service 异步消费；不要让 engine goroutine 同步等待 ComfyUI，否则会阻塞下一次路由和用户控制操作。

Host（`internal/host/host.go`）负责生命周期、事件广播、推进门和模型访问。严格模式下，ImageJobService 的 `completed` 才允许 unit/章节推进；`failed`、`timeout`、`cancelled` 通过 Host 事件和 gate 阻止推进。非严格模式可继续写作，但必须广播失败状态。事件应包含 `job_id`、`unit_id`、status、attempt、error/output URL，供 Web SSE/TUI 消费。

## 幂等、恢复和错误边界

- 以 `unit_id + workflow_id`（必要时加 workflow version）建立幂等键；已 `completed` 的 job 不重复生成，`pending/prompting/submitting/running` 在启动恢复时重新查询或取消后重试。
- Prompter 成功后先持久化结构化 prompt，再提交 ComfyUI；这样重试只重放绑定和远端任务，不重复消耗 LLM。
- `ValidateURL`/`NormalizeConfig` 在配置读取后执行；远端不可达、超时、无输出图片分别映射为可重试错误，不要把错误吞掉。
- output 解析沿用 `firstOutput`/`classifyOutputs`；workflow 的 `OutputSpec` 必须与 ComfyUI history 的 images 结构匹配，否则 job 应失败而不是写入伪成功文件。
- 任务取消先取消本地 context，再 best-effort `/interrupt`；远端中断失败不能阻塞本地终态保存。

## 文件级接入清单（供实现阶段使用）

| 文件 | 可复用能力 | 需要新增/调整 |
| --- | --- | --- |
| `internal/entry/web/v2.go` | `mergeCanvasRuntime`、`testJob` 输入合并、`runJob` 远端状态机 | 抽出无 HTTP 的 submit/bind service；HTTP 仅保留 DTO 和响应 |
| `internal/comfyui/workflow.go` | `ValidateWorkflow`、`ApplyBindings`、类型转换和路径保护 | 可补充 Prompter schema 到 binding 的显式映射校验 |
| `internal/store/comfyui.go` | `ImageJob`、Save/Load/List | 增加幂等键、Prompter 原始/解析结果和状态事件字段（如确有需要） |
| `internal/agents/build.go` | 模型 failover、usage logger、runner 构造模式 | 注册 `prompter` config 或独立 service，并设置结构化输出约束 |
| `internal/host/engine.go` | writer 完成、autoCommit、单 goroutine 控制边界 | 增加异步 unit-completed sink，不在 engine 内阻塞等待图片 |
| `internal/host/host.go` | 生命周期、事件、推进 gate、模型访问 | 注入 ImageJobService，转发 job 事件并在 strict 模式接入 gate |

以上路径能保持浏览器不直接访问模型或 ComfyUI，并让手工 `POST /api/v2/comfyui/jobs/test` 与自动 unit 图片生成共享同一套 workflow 校验和远端执行语义。
