# Web 工作台与 ComfyUI 接口契约

本文是 Web 工作台、Host、Store、Prompter 和 ComfyUI 适配器之间的契约。操作步骤见 [comfyui-canvas-user-guide.md](comfyui-canvas-user-guide.md) 与 [novel-comfy-bridge-user-guide.md](novel-comfy-bridge-user-guide.md)。历史设计稿见 [history/](history/)。

文中区分两类陈述：

- **已实现**：当前代码遵守的边界。
- **目标边界**：文档曾承诺、代码尚未落地；实现前不得当成现状。

新增 HTTP API 必须使用统一响应 envelope；现有 `/api/state`、`/api/replay`、`/api/start` 等兼容接口可以保留，但在迁移时应逐步包装到同一 envelope。

## 1. 系统架构

```text
                    +-----------------------------+
                    | Web Workbench                |
                    | settings / writing / images |
                    +-------------+---------------+
                                  | REST + SSE
                    +-------------v---------------+
                    | internal/entry/web           |
                    | handlers + DTO + event broker|
                    +-------------+---------------+
                                  | service interfaces
             +--------------------v--------------------+
             | Host / Application Services              |
             | lifecycle, commands, snapshot, events   |
             +---------+------------------+-------------+
                       |                  |
          +------------v---------+  +-----v----------------+
          | Writing Engine       |  | Image Service         |
          | architect/planner/   |  | Prompter -> workflow  |
          | writer/editor        |  | -> ComfyUI client     |
          +------------+---------+  +-----------+------------+
                       |                         |
                 +-----v-------------------------v-----+
                 | Store                               |
                 | config, outline, units, image jobs, |
                 | runtime queue and metadata           |
                 +----------------+--------------------+
                                  |
                         +--------v--------+
                         | ComfyUI Server  |
                         | /prompt /history|
                         | /view /interrupt|
                         +-----------------+
```

### 数据流

1. 浏览器通过 `/api/settings/*` 读取或保存模型、写作提示词和 ComfyUI 配置；浏览器不直接访问模型服务或 ComfyUI。
2. 用户从顶部设置或写作面板提交命令。Web handler 调用 Host 的命令服务，Host 负责生命周期、互斥和权限边界。
3. Host 继续使用现有 Engine/Store 完成导入、仿写、重新规划、写作要求、开始/继续/暂停/中止等动作。
4. `write_chapter_unit` 成功落盘后，Image Service 以 `unit_id` 创建图片任务：调用 `Prompter` 角色生成结构化 prompt，绑定工作流参数，提交 ComfyUI，并持久化每一次状态转移。
5. Image Service 通过 Host event sink 广播 `comfyui.job.*` 事件。Web SSE 和 TUI 都只消费 Host 广播，不自行轮询 ComfyUI。
6. 图片写入 unit 所属目录，Web 仅通过受控媒体接口读取图片和 metadata。严格模式下图片任务未完成时，Host 的 unit/章节推进门保持阻塞。

### 模块边界

| 模块 | 责任 | 不负责 |
| --- | --- | --- |
| `internal/entry/web` | HTTP DTO、路由、SSE、静态页面、统一 envelope | 业务状态机、ComfyUI 协议细节 |
| `internal/host` | 生命周期、命令、事件广播、推进门、Prompter 调度 | JSON workflow 节点编辑和 HTTP 细节 |
| `internal/comfyui` | URL 校验、API workflow 校验/绑定、提交、轮询、取消、下载 | 小说章节规划和 Web 页面 |
| `internal/imagejob`（建议新增） | unit 图片任务状态机、重试、恢复、严格模式 | 浏览器渲染 |
| `internal/store` | 配置/工作流/任务 metadata 的持久化和并发读写 | 远程请求和 UI |
| `internal/agents` | `prompter` role 的模型构建和提示词模板 | ComfyUI 连接管理 |

## 2. 统一 HTTP 响应

所有新增 `/api/v2/*` 接口以及后续迁移接口使用：

```json
{
  "code": 0,
  "data": {},
  "msg": ""
}
```

`code = 0` 表示成功；非零 code 表示业务或输入错误。HTTP 状态仍表达传输层结果：400 输入错误、404 不存在、409 状态冲突、422 校验失败、502 远程服务失败、504 超时。

建议错误码：

| code | 含义 |
| ---: | --- |
| 1001 | invalid_request |
| 1002 | not_found |
| 1003 | conflict / operation_not_allowed |
| 2001 | config_invalid |
| 3001 | comfyui_unreachable |
| 3002 | comfyui_workflow_invalid |
| 3003 | comfyui_job_failed |
| 3004 | comfyui_job_timeout |
| 3005 | comfyui_job_cancelled |
| 4001 | image_required_for_progress |

错误 data 应包含机器可读字段，示例：

```json
{
  "code": 3003,
  "data": {"job_id": "img_01", "node_id": "3", "node_type": "KSampler"},
  "msg": "ComfyUI node 3 (KSampler) failed: ..."
}
```

## 3. 顶部设置与命令入口

顶部导航固定提供四个设置项目：

| 项目 | 子页面/动作 | 后端资源 |
| --- | --- | --- |
| API 配置 | provider、model、API key 引用、角色模型（含 `prompter`） | `/api/v2/settings/models` |
| 设置 | 导入书本、仿写、重新规划、写作要求、运行策略 | `/api/v2/settings/workflow`、Host command API |
| 写作提示词 | architect/planner/writer/editor/prompter 模板及版本 | `/api/v2/settings/prompts` |
| ComfyUI | 连接、工作流、bindings、默认参数、严格模式、任务测试 | `/api/v2/comfyui/*` |

命令按钮替代命令行交互，但仍调用同一 Host service：

```text
POST /api/v2/commands/start       {"prompt":"..."}
POST /api/v2/commands/continue    {"text":"..."}
POST /api/v2/commands/steer       {"text":"..."}
POST /api/v2/commands/pause
POST /api/v2/commands/abort
POST /api/v2/commands/import      {"source":"...", ...}
POST /api/v2/commands/imitate     {"reference":"...", ...}
POST /api/v2/commands/replan      {"from_chapter":3,"instruction":"..."}
PUT  /api/v2/settings/writing-rules {"preferences":"..."}
```

按钮请求必须是幂等或有明确冲突响应；不能在 handler 中直接修改文件或启动 goroutine 绕过 Host。

## 4. 模型与 Prompter 配置

现有 `bootstrap.Config.Roles` 扩展 `prompter` 为可配置角色（默认回落到 architect 或顶层模型，具体回落规则由 ModelSet 统一处理）：

```json
{
  "roles": {
    "architect": {"provider":"openrouter", "model":"..."},
    "chapter_planner": {"provider":"openrouter", "model":"..."},
    "writer": {"provider":"openrouter", "model":"..."},
    "editor": {"provider":"openrouter", "model":"..."},
    "prompter": {"provider":"openrouter", "model":"..."}
  },
  "prompter": {
    "template_file": "assets/prompts/prompter.md",
    "output_schema": "image_prompt_v1",
    "default_negative_prompt": "low quality, blurry, malformed"
  }
}
```

Prompter 输入只包含当前 unit 及必要上下文：chapter title、unit plan、unit text、characters/setting、上一 unit 尾部。输出必须可解析：

```json
{"prompt":"...", "negative_prompt":"...", "tags":["fantasy"], "seed":0}
```

Prompt 模板和模型配置由 Store/Config service 管理；Web 只能编辑模板文本和非密钥引用，API key 不回传明文。

## 5. ComfyUI 配置与工作流契约

### 5.1 配置

```json
{
  "enabled": true,
  "base_url": "http://127.0.0.1:8188",
  "client_id": "ainovel-web",
  "timeout_ms": 600000,
  "poll_interval_ms": 1000,
  "strict": true,
  "max_response_bytes": 52428800,
  "workflow_id": "wf-default"
}
```

后端保存前校验 scheme 为 `http` 或 `https`、host 非空、端口在 1..65535；禁止 `file:`、userinfo、控制字符和路径穿越。默认监听仍为 `127.0.0.1`。

### 5.2 工作流实体

只接受 ComfyUI API 格式（node id -> `{class_type, inputs}`），不接受 UI workflow 的 `nodes/links` 格式；导入时明确返回转换提示。

```json
{
  "id":"wf-default", "name":"unit image", "version":1, "enabled":true,
  "workflow":{"3":{"class_type":"KSampler","inputs":{}}},
  "bindings":[
    {"key":"positive_prompt","node_id":"6","path":"inputs.text","type":"string","required":true},
    {"key":"negative_prompt","node_id":"7","path":"inputs.text","type":"string","required":false},
    {"key":"seed","node_id":"3","path":"inputs.seed","type":"integer","required":false},
    {"key":"width","node_id":"5","path":"inputs.width","type":"integer","required":true},
    {"key":"height","node_id":"5","path":"inputs.height","type":"integer","required":true},
    {"key":"steps","node_id":"3","path":"inputs.steps","type":"integer","required":false},
    {"key":"cfg","node_id":"3","path":"inputs.cfg","type":"number","required":false}
  ],
  "defaults":{"width":1024,"height":1024,"steps":28,"cfg":7},
  "output":{"node_id":"9","path":"images","index":0,"mime":"image/png"}
}
```

`path` 只允许点分隔 object key 和非负数组索引；绑定应用于深拷贝，不得修改模板原文。服务端执行节点存在性、`class_type`/`inputs`、绑定类型、必填项、输出节点和默认值范围校验。

### 5.3 三类 JSON 隔离（已实现）

API workflow、工作流 UI 配置和画布文档是三种不同 JSON，必须使用不同 `format`、文件名和版本号。导入时不能根据字段猜测并静默转换。UI workflow 或 Infinite Canvas JSON（含 `nodes`、`links`、`connections`、`resources`）返回 `code: 3002`。

| 格式 | 用途 | 可否直接 POST ComfyUI `/prompt` |
| --- | --- | --- |
| `comfyui_api_v1` | 原始 API workflow（`node_id -> {class_type, inputs}`） | 是（应用 bindings 后的深拷贝） |
| `ainovel_workflow_config_v1` | fields、bindings、defaults、outputs、instance_id | 否 |
| `ainovel_comfy_canvas_v1` | 节点位置、边、viewport、暴露字段、测试卡 | 否 |

工作流模板不可变地保存原始 API JSON；每次任务在深拷贝上应用 bindings 和运行参数。

### 5.4 文件布局（已实现）

工作流定义按项目共享，写在项目根 `.ainovel/`；任务、实例和输入媒体按工作区落盘。

```text
<project>/.ainovel/comfyui/workflows/<id>.json          # 聚合文档（兼容）
<project>/.ainovel/comfyui/workflows/<id>.api.json      # API workflow
<project>/.ainovel/comfyui/workflows/<id>.config.json   # 字段与 bindings
<project>/.ainovel/comfyui/workflows/<id>.canvas.json   # 画布投影
<workspace>/meta/comfyui/instances.json                 # 多实例与选择策略
<workspace>/meta/comfyui/jobs/<id>.json                 # ImageJob
<workspace>/assets/input/<sha256>.<ext>                 # 受控输入媒体
```

读取时可从聚合文档或双文件恢复；旧路径 `meta/comfyui/workflows/<id>.json` 仅作回退。`format=canvas` 导出的是 ainovel 画布投影，不能提交给 ComfyUI。

### 5.5 多实例选择（已实现）

实例与策略保存在 `meta/comfyui/instances.json`。选择顺序固定为：

```text
job.instance_id
  -> workflow.instance_id
  -> project.default_instance_id
  -> strategy（least_queue / explicit；其余按 priority + 稳定 ID）
```

`least_queue` 使用实例 `/queue` 的可观测队列长度。选择结果写入 job，任务开始后不得换实例。地址必须是无 userinfo、query、fragment 的 `http`/`https` URL。

```text
GET  /api/v2/comfyui/instances
PUT  /api/v2/comfyui/instances
POST /api/v2/comfyui/instances/{id}/test
```

### 5.6 输入媒体（已实现）

```text
POST /api/v2/comfyui/media/upload
GET  /api/v2/comfyui/media/{id}
```

文件保存到 `assets/input/<sha256>.<ext>`。任务参数只携带受控的 `media_ref` / `storage_key`，不允许浏览器提交任意本地路径。

### 5.7 ComfyUI REST

```text
POST /prompt                         -> {prompt_id}
GET  /history/{prompt_id}            -> execution status/outputs/errors
GET  /view?filename=&subfolder=&type= -> image bytes
POST /interrupt                      -> best-effort cancellation
```

`internal/comfyui.Client` 的最小接口：

```go
type Client interface {
    ValidateURL(baseURL string) error
    TestConnection(ctx context.Context) error
    Submit(ctx context.Context, workflow map[string]any, clientID string) (promptID string, err error)
    Wait(ctx context.Context, promptID string, pollInterval time.Duration) (History, error)
    Download(ctx context.Context, ref OutputRef, maxBytes int64) (DownloadedImage, error)
    Interrupt(ctx context.Context) error
}
```

所有请求接收 context；轮询用可取消 timer；总超时、单请求超时和轮询间隔分别配置。错误必须保留 node id、node type 和截断后的 exception message。取消/超时都要落盘，`/interrupt` 失败不能阻塞本地取消。

## 6. 图片任务和 unit 接口

`internal/imagejob` 已提供 Schema、Parser 和 Bridge 配置。下面的 `Service` 接口是**目标边界，尚未落地**；当前提交、轮询、取消仍在 `internal/entry/web/v2.go` 的 `v2Controller` 内执行。

目标接口：

```go
type Service interface {
    CreateForUnit(ctx context.Context, req CreateRequest) (Job, error)
    Get(ctx context.Context, jobID string) (Job, error)
    Cancel(ctx context.Context, jobID string) error
    Retry(ctx context.Context, jobID string) (Job, error)
    ResumePending(ctx context.Context) error
}
```

任务状态：`pending -> prompting -> submitting -> queued -> running -> completed`；终态另有 `failed`、`timeout`、`cancelled`。严格模式目前作用于 Prompter JSON 校验和图片任务提交，**尚未**成为 Engine / Host 的 unit 推进门。非严格模式允许写作继续，失败仍须落盘并在 Web 页面显示。

持久化示例（`meta/images/jobs/<job_id>.json`）：

```json
{
  "job_id":"img_01", "unit_id":"1-1-1", "chapter":1, "ordinal":1,
  "workflow_id":"wf-default", "status":"completed", "attempt":1,
  "prompt_id":"comfy-id", "prompt":"...", "negative_prompt":"...",
  "parameters":{"width":1024,"height":1024,"steps":28,"cfg":7},
  "output":{"filename":"001.png","mime":"image/png","url":"/api/v2/units/1/1/image"},
  "error":null, "started_at":"...", "finished_at":"..."
}
```

文件落点固定为 `drafts/<chapter>.units/<ordinal>.png`；同目录可有 `image.json`，但浏览器不能获得绝对路径。断点恢复依据 job metadata：completed 不重复生成，pending/failed/timeout 可重试，running/submitting 视为需重新查询或取消后重试。

### 图片 HTTP API

```text
GET  /api/v2/units/{chapter}/{ordinal}/image
GET  /api/v2/units/{chapter}/{ordinal}/image-job
POST /api/v2/units/{chapter}/{ordinal}/image/retry
```

成功响应使用 envelope；图片 bytes 接口可返回真实 `image/*` 内容，并通过 `X-API-Code: 0` 表示 envelope 之外的媒体响应。不存在或失败时返回 JSON envelope。

## 7. Web API

### 配置/工作流

```text
GET    /api/v2/comfyui/config
PUT    /api/v2/comfyui/config
POST   /api/v2/comfyui/test-connection
GET    /api/v2/comfyui/workflows
POST   /api/v2/comfyui/workflows/import
GET    /api/v2/comfyui/workflows/{id}
PUT    /api/v2/comfyui/workflows/{id}
DELETE /api/v2/comfyui/workflows/{id}
POST   /api/v2/comfyui/workflows/{id}/validate
POST   /api/v2/comfyui/jobs/test
```

工作流导入请求：`{"name":"...","workflow":{...},"bindings":[...],"defaults":{...},"output":{...}}`。保存前后都运行同一 validator，返回 `data.errors[]`，不要静默猜测节点。

### 运行状态

```text
GET /api/v2/state
GET /api/v2/replay?after=<seq>
GET /api/v2/units/{chapter}
```

`state.data` 包含现有 `UISnapshot` 的 JSON 版本以及当前 unit/image job 摘要；不得把 Go 私有字段或本地绝对路径暴露给浏览器。

### SSE

```text
GET /api/v2/events
GET /api/v2/stream
```

SSE 每条消息仍为 JSON `data:`。事件 payload 至少包括：

```json
{
  "event_id":"evt_42", "type":"comfyui.job.progress",
  "time":"2026-08-14T00:00:00Z",
  "data":{"job_id":"img_01","unit_id":"1-1-1","status":"running","progress":0.42,"message":"..."}
}
```

已实现的 SSE 覆盖写作运行时：`run.state`、`run.event`、`stream.delta`、`stream.clear`。`comfyui.job.*` 是**目标边界**；当前页面通过 HTTP 轮询 `GET /api/v2/comfyui/jobs/{id}` 获取任务状态，不能依赖 Host SSE 恢复配图任务。

## 8. 分阶段实施和依赖边界

### Phase 0：契约与兼容层

- 固化本文件 DTO、错误码、SSE 类型。
- 给现有 Web handler 增加 envelope writer，保留旧路径兼容。
- 将 `UISnapshot`、`Event`、replay 字段增加 JSON tags/转换 DTO。

依赖：无。禁止引入 ComfyUI 或前端框架。

### Phase 1：ComfyUI 配置和 workflow 页面后端

- 新增 `internal/comfyui` 的 URL validator、workflow parser/binder、client。
- 新增 Store 配置/工作流读写；实现 config、import、validate、test-connection API。
- Web 增加独立 ComfyUI 设置页，先支持 JSON 编辑器和 bindings 表格。

依赖：Phase 0；不接入写作流程。

### Phase 2：图片任务服务和事件

- 新增 `internal/imagejob`、`ImageJobStore`、状态机、超时/取消/重试/恢复。
- 接入 Host event sink 和严格推进门，但提供手动 test job。
- 增加 unit image/job API 与图片预留卡片。

依赖：Phase 1；Prompter 可先使用固定 prompt fixture。

### Phase 3：Prompter 与 unit 生命周期

- 增加 `prompter` role、模板和结构化输出校验。
- `write_chapter_unit` 后创建 image job；严格模式等待 completed。
- Resume 检查 unit 正文与 image metadata，避免重复写作和重复图片。

依赖：Phase 2、现有 Host advance gate。

### Phase 4：工作台命令化和体验完善

- 顶部 API/设置/提示词/ComfyUI 导航及权限/脏状态提示。
- 导入书本、仿写、重新规划、写作要求等命令转为表单/按钮。
- unit 图片画廊、重试、错误诊断、workflow 导入导出。

依赖：Phase 0-3 的稳定 DTO；不改变 Engine 语义。

### Phase 5：交叉验证和文档

- Backend/Frontend 依据本契约互审请求字段和错误处理。
- QA 覆盖 URL/workflow/path 安全、取消/超时、恢复、严格模式阻塞和 SSE 重连。
- 更新 README、部署和本地 ComfyUI workflow 导出说明。

依赖：所有核心代码完成。根据项目 AGENTS 约束，构建、测试和部署验证由用户执行。

## 9. 明确不纳入第一版

- 不复制 Infinite Canvas 的无限画布、浏览器插件、`new Function` 任意脚本执行或 IndexedDB 图片缓存。
- 不让浏览器直接请求 ComfyUI。
- 不把 UI workflow JSON 与 API workflow JSON 静默混用。
- 不允许图片失败悄悄标记 unit 完成；严格模式必须暂停并提供可恢复错误。

## 10. 提示词与写作要求预设（已实现）

`GET/PUT /api/v2/settings/prompts` 与 `/api/v2/settings/workflow` 使用 `version: 2`，同时保留旧字段镜像：

- `prompts` 始终等于当前激活组合的角色模板；旧前端只提交 `{ "prompts": {...} }` 时，更新当前组合。
- `writing_rules` 始终等于当前激活写作要求预设的 `text`。
- 读取 `version` 缺失或小于 2 的文件时，包装成默认预设，不改用户原文。

```json
{
  "version": 2,
  "active_preset": "默认配置",
  "presets": {
    "默认配置": {
      "name": "默认配置",
      "prompts": {
        "architect": "...",
        "chapter_planner": "...",
        "writer": "...",
        "editor": "...",
        "prompter": "..."
      }
    }
  },
  "prompts": {}
}
```

PUT 使用 `action`：`activate` / `save` / `save_as`。写作要求对应 `activate_writing_rules` / `save_writing_rules` / `save_writing_rules_as`。名称冲突返回 `code: 1003`。提示词组合保存不重启运行中的 Agent。

## 11. 画布文档

画布是 workflow 的编辑和测试投影，不改变 API workflow 语义。

```text
GET /api/v2/comfyui/workflows/{id}/canvas
PUT /api/v2/comfyui/workflows/{id}/canvas
POST /api/v2/comfyui/workflows/{id}/run
```

CanvasDocument 根对象带 `format: "ainovel_comfy_canvas_v1"`、`workflow_id`、`nodes`、`edges`、`viewport`、`fields`。删除、移动、缩放和字段勾选都不应改写 API workflow；运行时只读取画布上的字段值。更早的画布 DTO 讨论见 [history/comfyui_canvas_architecture_v2.md](history/comfyui_canvas_architecture_v2.md)。

## 12. Novel Unit -> Prompter -> ComfyUI 桥接契约

HTTP 与 DTO 以本节为准。早期服务接口草图见 [history/novel_comfy_bridge_architecture.md](history/novel_comfy_bridge_architecture.md)，其中 `imagejob.Service` / `Executor` 仍是目标边界。

### 12.1 Bridge 配置

```text
GET /api/v2/comfyui/bridge
PUT /api/v2/comfyui/bridge
```

```json
{
  "enabled": true,
  "auto_generate": true,
  "workflow_id": "wf-default",
  "strict": true,
  "prompter_timeout_ms": 120000,
  "previous_tail_chars": 1200
}
```

`workflow_id` 必须指向 enabled workflow；`prompter_timeout_ms` 范围 1000..600000，`previous_tail_chars` 范围 0..8000。PUT 保存前必须成功生成当前工作流 Prompt Schema；没有任何 `source=prompter` 字段时返回 `422/code=3101`。

### 12.2 动态 Schema

```text
GET /api/v2/comfyui/workflows/{id}/prompt-schema
```

成功 `data`：

```json
{
  "workflow_id": "wf-default",
  "schema_version": 1,
  "schema_hash": "sha256:...",
  "schema": {"type":"object","additionalProperties":false,"properties":{},"required":[]},
  "fields": [
    {"id":"positive_prompt","node_id":"6","input":"text","value_type":"string","source":"prompter"}
  ]
}
```

Schema property 名严格等于 `CanvasField.ID`。字段 ID 重复、格式非法、类型不支持或没有 Prompter 字段时返回 HTTP 422。

### 12.3 Parser 诊断

```text
POST /api/v2/comfyui/prompter/parse
```

请求：

```json
{
  "workflow_id": "wf-default",
  "raw": "{\"positive_prompt\":\"...\",\"negative_prompt\":\"...\"}",
  "strict": true
}
```

成功 `data`：

```json
{
  "valid": true,
  "schema_hash": "sha256:...",
  "values": {
    "positive_prompt": "...",
    "negative_prompt": "..."
  },
  "warnings": []
}
```

失败使用 HTTP 422 和统一 envelope：

```json
{
  "code": 3102,
  "data": {
    "valid": false,
    "stage": "validating",
    "errors": [{"field":"negative_prompt","rule":"required","message":"缺少字段 negative_prompt"}]
  },
  "msg": "图片提示词 JSON 校验失败"
}
```

### 12.4 Unit 图片生成

自动触发不经过 HTTP；手工生成/重新生成使用：

```text
POST /api/v2/units/{chapter}/{ordinal}/image/generate
POST /api/v2/comfyui/jobs/{job_id}/retry
POST /api/v2/comfyui/jobs/{job_id}/cancel
```

generate 请求允许为空；可选字段为：

```json
{"workflow_id":"wf-default","force":false}
```

retry 请求：

```json
{"regenerate_prompt":false}
```

`force=false` 遵守幂等键；`force=true` 创建新 attempt，但仍不能并行覆盖同一个 unit 图片。创建/重试成功返回 HTTP 202：

```json
{
  "code": 0,
  "data": {
    "job_id":"img_01",
    "unit_id":"1-1-1",
    "workflow_id":"wf-default",
    "status":"prompting",
    "stage":"prompting",
    "attempt":1,
    "schema_hash":"sha256:...",
    "prompt_values":{},
    "output":null,
    "error":""
  },
  "msg":""
}
```

读取继续复用：

```text
GET /api/v2/comfyui/jobs/{job_id}
GET /api/v2/units/{chapter}/{ordinal}/image-job
GET /api/v2/units/{chapter}/{ordinal}/image
```

`prompt_raw` 默认不返回列表接口，只在单任务详情中返回且最大 64 KiB；任何 API 都不得返回 API key、本地绝对路径或完整 LLM 请求上下文。

### 12.5 test job 兼容

`POST /api/v2/comfyui/jobs/test` 保留当前请求 DTO，`trigger=test`，不调用 Prompter。目标是与自动链路共用同一 `StartFromValues -> BindAndStart -> Executor`；**当前仍由 `v2Controller.runJob` 执行**。

### 12.6 新增错误码

| code | HTTP | 含义 |
| ---: | ---: | --- |
| 3101 | 422 | prompt_schema_invalid |
| 3102 | 422 | prompter_json_invalid |
| 3103 | 502 | prompter_call_failed |
| 3104 | 504 | prompter_timeout |
| 3105 | 409 | unit_image_job_conflict |

所有响应仍为 `{ "code": 0, "data": {}, "msg": "" }`；图片 bytes 接口延续 `Content-Type: image/*` 与 `X-API-Code: 0` 例外。

### 12.7 事件

SSE 增加 `comfyui.job.prompting`、`comfyui.job.validating`、`comfyui.job.binding` 是**目标边界**。payload 至少包含 `job_id`、`unit_id`、`workflow_id`、`status`、`stage`、`attempt`；失败事件增加 `code`、`recoverable` 和经过截断的 `message`。事件不得携带 unit 正文或 `prompt_raw`。当前实现用 HTTP 轮询替代。

## 13. 目标边界（未落地）

以下内容不得写成现状，也不得按「已完成」实现新功能时省略：

1. `internal/imagejob.Service` / `Executor`：HTTP 测试与自动 unit 生成应走同一服务；web handler 不应直接调用模型或 ComfyUI。
2. ComfyUI job 事件尚未接入 Host SSE；页面不能依赖 `comfyui.job.*`。
3. `strict` 尚未成为 Engine / Host 的 unit 或章节推进门；图片失败不会自动挡住现有写作循环。
4. 远端 ComfyUI 实例的输入媒体同步未做：上传文件保存在本地，不会自动调用每个远端实例的 `/upload/image`。
5. 酒馆 / Galgame 复用同一 ImageJob 管道，但没有独立契约文档。
