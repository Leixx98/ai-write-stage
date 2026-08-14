# Web 工作台与 ComfyUI 使用说明

本文说明当前代码中已经可以使用的 Web 工作台和 ComfyUI 配置能力，并明确仍在开发中的自动化边界。日常操作优先参阅 [ComfyUI 画布工作台使用指南](comfyui-canvas-user-guide.md)；本文件保留底层接口和兼容说明。接口细节以 [`docs/api_contracts.md`](api_contracts.md) 为准。

## 启动 Web 工作台

Web 工作台与 TUI 使用同一份配置和 `Host` 运行时。启动前需要先完成一次模型配置：

```text
ainovel-cli --web --listen 127.0.0.1:8080
```

浏览器打开 `http://127.0.0.1:8080/`。`--listen` 可以指定其他监听地址；建议在共享网络环境中显式使用回环地址，并通过反向代理或访问控制保护工作台。

约束：`--web` 不能与 `--headless`、`--prompt` 或 `--prompt-file` 同时使用。Web 模式下的创作需求从页面输入框提交。服务端日志写入当前输出目录的 `logs/web.log`，启动或恢复失败时也应优先查看该文件。

## 页面分区

顶部导航目前包含 Workbench、API、Settings、Writing prompts 和 ComfyUI 五个分区：

- Workbench：运行状态、章节大纲、事件、流式输出和当前 unit 图片预览。
- API：模型和 provider 配置的展示入口。
- Settings：导入书本、仿写、重新规划和写作要求的设置文档入口。
- Writing prompts：各角色提示词设置入口，包含 Prompter 的预留位置。
- ComfyUI：地址、超时、轮询、严格模式、工作流、bindings 和测试任务。

新增 API 的响应统一为：

```json
{"code": 0, "data": {}, "msg": ""}
```

旧版 `/api/state`、`/api/events`、`/api/stream` 等兼容接口仍然存在，主要用于已有工作台；新页面优先使用 `/api/v2/*`。媒体响应（例如 PNG）不是 JSON envelope，而是返回真实 `image/*` 内容并附带 `X-API-Code: 0`。

## ComfyUI 配置

在 ComfyUI 页面填写并保存：

| 字段 | 说明 |
| --- | --- |
| Base URL | ComfyUI 服务地址，例如 `http://127.0.0.1:8188`。仅允许 `http`/`https`，禁止用户信息、非法端口和路径穿越。 |
| Timeout (ms) | 单个任务的总超时时间，默认 10 分钟。 |
| Poll interval (ms) | 查询 `/history/{prompt_id}` 的间隔，默认 1 秒。 |
| Client ID | 提交到 ComfyUI 的客户端标识。 |
| Max response bytes | 下载图片时的响应大小上限。 |
| Enabled | 是否启用 ComfyUI 配置。 |
| Strict mode | 图片任务失败时是否阻止后续 unit/章节推进的策略开关。 |

对应接口：

```text
GET  /api/v2/comfyui/config
PUT  /api/v2/comfyui/config
POST /api/v2/comfyui/test-connection
```

连接测试和任务执行均由 Go 后端发起，浏览器不会直接请求 ComfyUI。地址校验失败时会返回 `code: 2001`；服务不可达时通常返回 `code: 3001`。严格模式下遇到失败、超时或取消，应检查 ComfyUI 服务、模型文件、workflow 节点和显存后再重试。

## API workflow JSON

当前导入器只接受 ComfyUI 的 **API workflow JSON**，即节点 ID 到 `{class_type, inputs}` 的对象；不能直接粘贴 ComfyUI 编辑器导出的 UI workflow（其中通常包含 `nodes`、`links`、`extra`）。UI workflow 请先在 ComfyUI 中导出 API 格式。

工作流由 API JSON、参数 bindings、默认值和输出节点组成。最小结构示例：

```json
{
  "name": "unit image",
  "workflow": {
    "3": {"class_type": "KSampler", "inputs": {"seed": 0, "steps": 28, "cfg": 7}},
    "6": {"class_type": "CLIPTextEncode", "inputs": {"text": ""}}
  },
  "bindings": [
    {"key": "positive_prompt", "node_id": "6", "path": "inputs.text", "type": "string", "required": true},
    {"key": "steps", "node_id": "3", "path": "inputs.steps", "type": "integer", "required": false}
  ],
  "defaults": {"steps": 28},
  "output": {"node_id": "9", "path": "images", "index": 0, "mime": "image/png"}
}
```

服务端保存前会校验节点、`class_type`、`inputs`、binding 节点引用、路径和类型，并在绑定时深拷贝 workflow，避免修改保存的模板。相关接口：

```text
GET    /api/v2/comfyui/workflows
POST   /api/v2/comfyui/workflows/import
GET    /api/v2/comfyui/workflows/{id}
PUT    /api/v2/comfyui/workflows/{id}
DELETE /api/v2/comfyui/workflows/{id}
POST   /api/v2/comfyui/workflows/{id}/validate
```

## 测试任务、状态与控制

ComfyUI 页面中的 Job test 用于手动验证连接、workflow 和输出节点。提交后可查看任务 JSON 和状态：

```text
pending -> submitting -> queued -> running -> completed
                                      \-> failed / timeout / cancelled
```

测试任务接口：

```text
POST /api/v2/comfyui/jobs/test
GET  /api/v2/comfyui/jobs/{job_id}
POST /api/v2/comfyui/jobs/{job_id}/cancel
POST /api/v2/comfyui/jobs/{job_id}/retry
```

任务元数据保存在 `meta/images/jobs/<job_id>.json`。成功图片写入 unit 对应的 `drafts/<chapter>.units/<ordinal>.png`；测试任务没有 unit 坐标时写入 `meta/images/tests/<job_id>.png`。取消首先取消本地 context，并尽力调用 ComfyUI `/interrupt`；即使远端中断请求失败，本地任务也会进入取消状态。超时会调用 `/interrupt`，然后持久化 `timeout` 状态，避免后台 goroutine 无限等待。

## 图片预览和 unit 接口

```text
GET /api/v2/units/{chapter}/{ordinal}/image
GET /api/v2/units/{chapter}/{ordinal}/image-job
POST /api/v2/units/{chapter}/{ordinal}/image/retry
```

图片不存在时返回 JSON envelope，而不是空白图片。工作台只使用受控的 unit 路径读取图片，不向浏览器泄露输出目录的绝对路径。

## 当前边界

当前版本已经提供独立 ComfyUI 页面、后端配置持久化、API workflow 校验/绑定、连接测试和手动 Job test。以下能力仍是契约和页面的预留项，不能视为已经自动生效：

1. `Prompter` role 尚未接入 `write_chapter_unit` 的自动调度。保存 Prompter 模板不会自动为每个 unit 创建图片任务。
2. 小说写作流程目前不会在每个 unit 完成后自动等待图片任务，也不会自动把图片失败作为章节推进门禁；严格模式开关主要作用于任务策略和后续集成边界。
3. `/api/v2/settings/models`、`/api/v2/settings/workflow` 和 `/api/v2/settings/prompts` 目前是轻量 JSON 文档存储，不等同于完整的模型/提示词运行时热加载。
4. 页面上的 workflow “Validate”按钮当前以保存时的后端校验为准；尚未提供完整的节点可视化编辑器。

因此，接入真实写作闭环前，建议先在 ComfyUI 页面完成连接测试和一个最小 API workflow 的 Job test，再进行后续 Prompter 与 unit 生命周期集成。

## 用户自测（不由项目自动执行）

根据仓库的 `AGENTS.md` 约束，维护者不会在自动修改过程中运行构建、测试或启动命令。完成修改后可由用户在本机执行：

```text
gofmt -w ./internal ./cmd
go test ./...
go run ./cmd/ainovel-cli --web --listen 127.0.0.1:8080
```

启动后依次检查：页面能打开；`GET /api/v2/comfyui/config` 返回 envelope；非法 URL 被拒绝；连接测试能区分不可达与成功；导入非法 workflow 会返回 `code: 3002`；Job test 能进入终态；取消和超时不会长期占用请求；成功图片可在 Workbench 的预览区域显示。若严格模式下出现错误，应按页面提示检查 ComfyUI 环境，而不是继续推进小说流程。
