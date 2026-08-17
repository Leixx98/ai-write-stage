# ComfyUI 工作流工作台架构

本文把 `docs/infinite_canvas_main_analysis.md` 中可复用的设计转化为 ainovel 的后端/前端契约。目标是提供类似 Infinite Canvas 的灵活 workflow 配置体验，但保持 ainovel 的边界：浏览器只访问 ainovel Web API，ComfyUI 请求、文件写入、任务恢复和严格模式都由 Go 后端负责。

## 1. 设计原则

1. ComfyUI API workflow、工作台画布文档和工作流 UI 配置是三种不同 JSON，必须使用不同字段、文件名和版本号；导入时不能根据字段猜测并静默转换。
2. 工作流模板不可变地保存原始 API JSON；每次任务在深拷贝上应用 bindings 和运行参数，避免前一次 unit 污染模板。
3. 多实例选择、媒体上传、轮询、取消、下载和错误归一化都在后端完成；前端只提交结构化参数并订阅事件。
4. 每个 unit 最多绑定一张主图。工作流可返回多个媒体，但其余输出只保存在 job metadata 和测试页面中。
5. 严格模式是 Host 的推进策略，不是前端开关：主图没有成功落盘时，下一个 unit 不得开始。

## 2. 数据模型与文件布局

### 2.1 ComfyUI 实例

```json
{
  "id": "local-8188",
  "name": "Local ComfyUI",
  "base_url": "http://127.0.0.1:8188",
  "enabled": true,
  "priority": 100,
  "max_concurrency": 1,
  "selection_weight": 1,
  "health": "unknown",
  "last_checked_at": null,
  "auth_ref": "",
  "tags": ["local", "sdxl"]
}
```

`auth_ref` 仅引用后端安全存储中的凭据，不在 GET 响应中返回明文。`base_url` 必须是无 userinfo、无 query/fragment 的 `http` 或 `https` URL；默认实例为 `local-8188` 和 `http://127.0.0.1:8188`。

实例配置：

```json
{
  "default_instance_id": "local-8188",
  "strategy": "least_queue",
  "fallback_instance_ids": [],
  "health_ttl_ms": 10000,
  "queue_probe": true,
  "sticky_unit": true
}
```

### 2.2 选择策略

选择顺序固定为：

```text
job.instance_id（显式覆盖）
  -> workflow.instance_id（工作流绑定）
  -> project.default_instance_id
  -> strategy（explicit / least_queue / round_robin / weighted）
  -> fallback_instance_ids
```

`least_queue` 使用实例 `/queue` 的可观测队列长度和本地 running job 数；无法探测队列时降级到健康实例的 `priority`、`selection_weight` 和稳定 ID 排序。选择结果写入 job，任务开始后不得漂移到另一实例；只有输入媒体尚未上传且任务在 `selecting` 阶段失败时才允许重新选择。选择操作需要短时 lease，防止两个 goroutine 同时把同一实例判断为空闲。

后端模块：`internal/comfyui/instances.go`（注册、校验、健康检查、选择器）；Store 持久化 `meta/comfyui/instances.json`。前端页面提供实例列表、默认实例和策略，但不允许直接编辑运行时健康状态。

### 2.3 JSON 类型和持久化

| 类型 | 内容 | canonical 文件 |
| --- | --- | --- |
| API workflow | ComfyUI `/prompt` 接受的 node map | `meta/comfyui/workflows/<id>.api.json` |
| Workflow config | 字段 schema、bindings、默认值、输出选择器、实例覆盖 | `meta/comfyui/workflows/<id>.config.json` |
| Canvas document | 未来工作台节点/连线/资源布局，只用于 UI | `meta/comfyui/canvases/<id>.canvas.json` |
| Job | 任务状态、prompt_id、实例、参数、所有输出 metadata | `meta/comfyui/jobs/<id>.json` |
| Input media | 受控输入文件和 hash | `assets/input/<sha256>.<ext>` |

API workflow 的根对象必须是 `node_id -> {class_type, inputs}`。Canvas document 的根对象应带 `format: "ainovel_canvas_v1"`、`nodes`、`connections`、`resources`；它绝不能直接提交到 `/prompt`。Workflow config 根对象带 `format: "ainovel_workflow_config_v1"`、`fields`、`bindings`、`defaults`、`outputs`。三者响应中都应明确 `format` 和 `version`。

## 3. Workflow 导入、导出和编辑

### 3.1 导入 API workflow

```text
POST /api/v2/comfyui/workflows/import
```

请求：

```json
{
  "format": "comfyui_api_v1",
  "id": "wf-sdxl",
  "name": "SDXL unit image",
  "api_json": {"3": {"class_type": "KSampler", "inputs": {}}},
  "config": {
    "format": "ainovel_workflow_config_v1",
    "fields": [], "bindings": [], "defaults": {}, "outputs": []
  },
  "instance_id": "local-8188"
}
```

兼容当前 `workflowRequest`：如果 body 直接包含 `workflow`、`bindings`、`defaults`、`output`，后端把 `workflow` 视为 `api_json`，生成缺省 config；新前端应使用 `format`/`api_json`。如果检测到 `nodes`、`links` 或 `connections`，返回 `3002`，data 中给出 `detected_format: "canvas_or_ui"` 和转换提示，不静默导入。

导入校验：根对象非空；每个 node 是 object，包含非空 `class_type` 和 object `inputs`；node id 只能是安全字符串；不允许路径、脚本、任意文件引用。服务器返回 `data.workflow`、`data.config`、`data.inferred_fields` 和 `data.warnings`。内置 workflow 只读，用户 workflow ID 必须经过 `safeComfyID` 校验。

### 3.2 导出

```text
GET /api/v2/comfyui/workflows/{id}/export?format=api
GET /api/v2/comfyui/workflows/{id}/export?format=config
GET /api/v2/comfyui/workflows/{id}/export?format=canvas
```

`format=api` 返回原始 API JSON，`format=config` 返回字段配置；`format=canvas` 只在存在 canvas 文档时可用，否则返回 `1002`。统一 envelope 的 JSON 响应为：

```json
{"code":0,"data":{"format":"comfyui_api_v1","id":"wf-sdxl","filename":"wf-sdxl.api.json","content":{}},"msg":""}
```

若后续提供文件下载，响应使用 `Content-Disposition`，并设置 `X-API-Code: 0`；不能把本地绝对路径放进 data。

### 3.3 动态节点参数 schema

```text
GET /api/v2/comfyui/workflows/{id}/schema
PUT /api/v2/comfyui/workflows/{id}/config
```

字段实体：

```json
{
  "id": "6::text",
  "node_id": "6",
  "input": "text",
  "label": "Positive prompt",
  "control": "textarea",
  "value_type": "string",
  "default": "",
  "min": null,
  "max": null,
  "step": null,
  "options": [],
  "required": false,
  "randomizable": false,
  "source": "inferred",
  "editable": true
}
```

`control` 支持 `text`、`textarea`、`number`、`slider`、`boolean`、`dropdown`、`image`、`video`、`audio`、`file`。服务端按 node input 的运行时类型、`class_type` 和已知名称推断；前端只负责渲染控制器，最终类型转换和范围检查由后端完成。`PUT config` 可手工覆盖 label/control/value_type/default/min/max/step/options/required/randomizable；未知 node/input、重复 field id、min > max 和 options 类型不匹配必须返回 `3002`。

前端编辑器应显示“推断/手工覆盖”来源；动态重新渲染时保留当前 popover 的 pinned/interacting 状态，不重置滚动位置。

## 4. Bindings 自动推断与手工覆盖

### 4.1 推断规则

后端 `InferBindings(api_json)` 返回候选和警告，不直接改变模板：

| 逻辑 key | 候选规则 | 典型类型 |
| --- | --- | --- |
| `positive_prompt` | `CLIPTextEncode` 的 `inputs.text`，按 node 连接到采样器 positive | string/textarea |
| `negative_prompt` | 连接到 negative 的 `CLIPTextEncode.inputs.text` | string/textarea |
| `seed`、`steps`、`cfg` | `KSampler`/同类采样节点对应 input | integer/number |
| `width`、`height` | EmptyLatent/latent 初始化节点 | integer |
| `sampler`、`scheduler` | 采样节点 input，非空 choices | dropdown |
| `input_image` | LoadImage/图像输入节点 | image |
| output | SaveImage/Preview/视频/音频输出 | output selector |

多个候选时返回 `ambiguous: true`、`candidates[]`，要求用户在页面确认；不得按节点 ID 偶然排序后静默选取。Prompter 结果默认只填充 `positive_prompt` 和 `negative_prompt` bindings。

### 4.2 Binding 结构

```json
{
  "key": "positive_prompt",
  "node_id": "6",
  "path": "inputs.text",
  "control": "textarea",
  "value_type": "string",
  "required": true,
  "default": "",
  "min": null,
  "max": null,
  "step": null,
  "options": [],
  "random": false,
  "source": "manual"
}
```

应用优先级：本次 job values > binding default > workflow config default > API node 原值。`random:true` 只允许数值字段，seed 由后端生成并写入 job；浏览器不得生成安全关键参数。Binding path 只允许 object key 和合法非负数组索引，应用到深拷贝；缺失 required、类型/范围/enum 不匹配均在提交前失败。

## 5. 输入媒体上传和引用

### 5.1 媒体引用

```json
{
  "source": "local",
  "storage_key": "assets/input/ab12...ef.png",
  "instance_id": "local-8188",
  "upload_name": "ainovel_ab12.png",
  "mime": "image/png",
  "size": 24576,
  "sha256": "ab12...ef"
}
```

本地输入由 Store 根据项目根目录解析，禁止 `..` 和绝对路径。上传前检查 MIME、扩展名和最大字节数；浏览器上传使用 ainovel multipart API，不能直接调用 ComfyUI。

### 5.2 API

```text
POST /api/v2/comfyui/media/upload       # multipart: file, instance_id, purpose
POST /api/v2/comfyui/media/reference    # 引用已有 unit/asset，不复制浏览器路径
GET  /api/v2/comfyui/media/{id}
```

后端先按 SHA-256 去重，再调用选定实例 `/upload/image`（必要时覆盖 `subfolder`/`type`）。实例发生变化时，如果目标实例没有该 upload_name，服务端从本地受控文件重新上传。job 记录每个媒体的 source、instance、upload_name、sha256 和上传状态。

## 6. Job 状态、轮询、取消和重试

### 6.1 Job 模型

```json
{
  "job_id": "img_01",
  "unit_id": "1-1-1",
  "chapter": 1,
  "ordinal": 1,
  "instance_id": "local-8188",
  "workflow_id": "wf-sdxl",
  "workflow_version": 3,
  "workflow_hash": "sha256:...",
  "status": "running",
  "attempt": 1,
  "prompt_id": "comfy-prompt-id",
  "progress": 0.42,
  "parameters": {},
  "inputs": [],
  "outputs": [],
  "error": null,
  "created_at": "...", "started_at": "...", "finished_at": null
}
```

状态：`pending -> selecting -> uploading -> prompting -> submitting -> queued -> running -> downloading -> succeeded`；终态 `failed`、`timeout`、`cancelled`。现有 `completed` 读入时映射为 `succeeded`，对旧 API 响应可继续输出 `completed` 别名一段迁移期。

### 6.2 执行策略

```text
创建 job（持久化 pending）
  -> 选择并 lease 实例
  -> 上传缺失媒体
  -> Prompter 生成 prompt（或使用测试 prompt）
  -> 应用 bindings 到 workflow 深拷贝
  -> POST /prompt
  -> 轮询 /history/{prompt_id}
  -> 分类 outputs，按 selector 下载主图/其他预览
  -> 持久化结果并广播事件
```

请求 context 必须贯穿选择、上传、提交、轮询和下载。总 timeout、单请求 timeout、poll interval 分离配置；轮询使用可取消 timer，不得无限等待。取消时先取消本地 context，再 best-effort POST `/interrupt` 到该 job 的实例；无论 `/interrupt` 是否成功，本地状态都落为 `cancelled`。重试创建新 attempt，保留上一 attempt 的 error/output metadata，不复用旧 prompt_id。

事件类型：`comfyui.job.created`、`comfyui.job.status`、`comfyui.job.progress`、`comfyui.job.output`、`comfyui.job.succeeded`、`comfyui.job.failed`、`comfyui.job.cancelled`。每条 SSE data 为 `{event_id,type,time,data:{job_id,unit_id,status,...}}`；HTTP API 使用统一 `{code,data,msg}`。

### 6.3 API

```text
GET    /api/v2/comfyui/instances
PUT    /api/v2/comfyui/instances
POST   /api/v2/comfyui/instances/{id}/test
GET    /api/v2/comfyui/workflows
POST   /api/v2/comfyui/workflows/import
GET    /api/v2/comfyui/workflows/{id}
PUT    /api/v2/comfyui/workflows/{id}
GET    /api/v2/comfyui/workflows/{id}/schema
PUT    /api/v2/comfyui/workflows/{id}/config
GET    /api/v2/comfyui/workflows/{id}/export
POST   /api/v2/comfyui/workflows/{id}/run
POST   /api/v2/comfyui/jobs
GET    /api/v2/comfyui/jobs/{id}
POST   /api/v2/comfyui/jobs/{id}/cancel
POST   /api/v2/comfyui/jobs/{id}/retry
GET    /api/v2/comfyui/jobs/{id}/outputs
GET    /api/v2/units/{chapter}/{ordinal}/image
GET    /api/v2/units/{chapter}/{ordinal}/image-job
POST   /api/v2/units/{chapter}/{ordinal}/image/retry
```

创建 job 请求：

```json
{
  "unit_id":"1-1-1", "chapter":1, "ordinal":1,
  "workflow_id":"wf-sdxl", "instance_id":null,
  "prompt":"", "negative_prompt":"",
  "parameters":{"width":1024,"height":1024,"seed":0},
  "inputs":[], "mode":"unit|test"
}
```

成功创建返回 `202 {"code":0,"data":<Job>,"msg":""}`；任务查询始终返回最新持久化状态。每 unit 的 `/image` 媒体成功响应可返回真实 `image/*` 并设置 `X-API-Code: 0`；找不到图片时返回 envelope。

## 7. 输出分类和预览

服务端按 output selector 优先，否则按扩展名、MIME 和 node `class_type` 分类：`image`、`video`、`audio`、`text`、`file`。每个 output：

```json
{
  "kind":"image", "node_id":"9", "output_key":"images",
  "class_type":"SaveImage", "mime":"image/png",
  "previewable":true, "url":"/api/v2/comfyui/jobs/img_01/outputs/0",
  "storage_key":"drafts/01.units/001.png", "size":12345
}
```

`storage_key` 仅供后端，不能暴露绝对路径。文本输出直接保留截断预览和完整 metadata；图片/视频/音频通过受控媒体接口预览。若存在正式 `SaveImage` 输出，`PreviewImage` 等调试输出不作为主图；若只有 Preview 输出，允许其作为主图但 job warning 必须标记 `preview_fallback`。unit 主图只选择一个 `kind=image` output，其余 output 可在 ComfyUI 测试页查看。

## 8. ainovel 模块接口

```go
// internal/comfyui
type InstanceRegistry interface {
    List(ctx context.Context) ([]Instance, error)
    Save(ctx context.Context, instance Instance) error
    Remove(ctx context.Context, id string) error
    Select(ctx context.Context, hint SelectionHint) (Instance, error)
    Test(ctx context.Context, id string) (Health, error)
}

type WorkflowService interface {
    Import(ctx context.Context, req ImportRequest) (Workflow, Config, []Warning, error)
    Export(ctx context.Context, id string, format ExportFormat) (ExportedWorkflow, error)
    Schema(ctx context.Context, id string) (FieldSchema, error)
    UpdateConfig(ctx context.Context, id string, cfg WorkflowConfig) error
    Apply(ctx context.Context, id string, values map[string]any) (map[string]any, error)
}

// internal/imagejob
type Service interface {
    Create(ctx context.Context, req CreateRequest) (Job, error)
    Get(ctx context.Context, id string) (Job, error)
    Cancel(ctx context.Context, id string) error
    Retry(ctx context.Context, id string) (Job, error)
    ResumePending(ctx context.Context) error
}
```

`internal/entry/web` 只负责 DTO、envelope、路由、SSE 和媒体响应；`internal/host` 负责 unit 生命周期、Prompter 调度和严格推进门；`internal/store` 负责上述 JSON/文件原子持久化；`internal/agents` 增加 `prompter` role。任何后端 job 都必须能在 Host 关闭/重启后从 Store 恢复或明确失败。

## 9. 严格模式与每 unit 一图

`comfyui.strict=true` 时：

- unit 正文可以先落盘，但 image job 必须进入 `succeeded` 且主图已落盘，Host 才能开始下一个 unit。
- `failed`、`timeout`、`cancelled`、无可分类图片或下载失败均暂停当前流程，并广播可操作错误。
- 恢复运行扫描 `meta/comfyui/jobs`：已 succeeded 且文件 hash 正确的不重复生成；pending/running 根据 prompt_id 查询或取消后重试；其他终态等待用户 Retry。

每个 unit 只创建一个主 job；Retry 增加 attempt 而不是创建第二张并行主图。非严格模式可以继续写作，但必须在 unit metadata 中记录失败，Web 仍显示重试按钮。

## 10. 迁移兼容策略

1. 现有 `meta/comfyui/config.json` 单实例配置映射为 `local-8188`；缺失文件使用 `DefaultConfig`。`base_url`、`timeout_ms`、`poll_interval_ms` 等 snake_case 字段保持兼容，写入 canonical 格式。
2. 现有 `meta/comfyui/workflows/<id>.json` 读入时视为 API workflow + 内嵌 config，首次编辑/导出时拆分为 `.api.json` 和 `.config.json`；不删除旧文件直到显式迁移。
3. 现有 `store.ImageJob.Status == "completed"` 读取映射为 `succeeded`；旧 `/api/v2/comfyui/jobs/test` 保留为 `mode:test` 的别名，响应在迁移期可同时提供 `status_alias:"completed"`。
4. 现有直接 workflow body（`workflow`/`bindings`/`defaults`/`output`）继续接受；新增 API 要求 `format` 字段，Canvas/UI JSON 一律返回明确的 `3002`。
5. 现有 `/api/units/.../image` 和 `X-API-Code: 0` 媒体响应保留；新增 output 预览接口全部通过 envelope 或受控媒体 URL，不暴露本地路径。

## 11. 统一错误 envelope 和安全边界

所有 HTTP JSON：

```json
{"code":0,"data":{},"msg":""}
```

建议错误：`2001 config_invalid`、`3001 instance_unreachable`、`3002 workflow_invalid`、`3003 job_failed`、`3004 job_timeout`、`3005 job_cancelled`、`3006 output_invalid`、`3007 media_upload_failed`。data 至少含 `phase`、`retryable`、`job_id`/`instance_id`（适用时），不含 API key 或绝对路径。

媒体上传、workflow ID、输出 filename/subfolder 和本地 storage key 都要做路径清理和项目根目录约束；响应体大小、MIME、输入媒体尺寸和输出文件尺寸必须有限制。连接失败、超时、ComfyUI execution error 记录结构化日志，截断 exception message，禁止记录 prompt 中的密钥和完整大文件内容。

