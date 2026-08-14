# Infinite Canvas 参考分析与 ainovel-cli ComfyUI 设计建议

## 1. 结论摘要

对 `D:\infinite-canvas-0.15.1` 做了全仓库检索和重点源码阅读。该版本**没有原生 ComfyUI 适配**：仓库中没有 `ComfyUI/comfyui` 字样，也没有直接实现 ComfyUI 常见的 `/prompt`、`/history/{prompt_id}`、`/view`、`/interrupt` 路由。因此不能把它当作 ComfyUI 客户端实现来移植。

它可以参考的部分是：

- 可配置的多渠道模型配置，以及每个模型单独的调用脚本。
- 插件节点和自定义 Panel 的前端扩展边界。
- `poll(request, extract, { intervalMs, timeoutMs })` 形式的通用异步任务轮询。
- 浏览器端 `AbortSignal` 贯穿请求、轮询和生成操作。
- IndexedDB/localForage 保存图片、日志和插件私有配置，画布节点只保存可序列化 metadata。
- 配置页、抽屉、弹窗和 CodeMirror 编辑器组成的参数化工作台。

对 ainovel-cli 的建议是：将 ComfyUI 做成**服务端的独立 Provider/任务模块**，Web 端做一个独立的“ComfyUI 设置/工作流”页面。工作流 JSON 和参数映射由服务端校验、保存和执行；浏览器只提交配置和任务控制请求，避免把 ComfyUI 地址、小说内容和控制能力暴露给任意前端脚本。

## 2. 参考仓库的实际实现边界

### 2.1 模型调用和插件脚本

`web/src/services/api/model-plugin.ts` 提供用户自定义模型调用脚本。脚本通过 `new Function` 执行，注入以下变量和辅助函数：

- 输入：`prompt`、`images`、`messages`、`params`。
- 渠道信息：`model`、`baseUrl`、`apiKey`、`systemPrompt`、`reasoningEffort`。
- 网络和控制：`http`、`request`、`poll`、`sleep`、`signal`、`onDelta`。

`poll` 的行为是固定间隔轮询，默认间隔 2500ms、超时 300000ms；每一轮先检查 `AbortSignal`，超时后抛出错误。`http/request` 使用 Axios 并传递 signal，所以取消可以传播到请求和等待阶段。

脚本返回结果后按能力归一化。图像返回 `string[]` 或包含 `url/dataUrl/b64_json` 的对象，最终统一为图片 URL/data URL。这种“脚本负责适配供应商，主流程只处理统一结果”的思路适合借鉴，但 ComfyUI 的 workflow JSON 需要强校验和节点级绑定，不能只依赖任意脚本。

### 2.2 前端配置体验

`web/src/components/layout/app-config-modal.tsx` 将配置拆成 Tabs：渠道、偏好、提示词来源、WebDAV、本地存储。渠道编辑使用 Drawer，单个模型可以配置能力（image/video/text/audio）和独立脚本。

`model-script-editor.tsx` 使用 CodeMirror：

- 左侧展示返回约束和可插入变量。
- 右侧编辑代码。
- 底部提供 OpenAI/Gemini 模板、恢复默认、保存/取消。
- 编辑状态先保存在本地 draft，点击保存才写回配置。

这是一种很适合 ComfyUI workflow 页面借鉴的交互：工作流 JSON 使用代码编辑器，同时提供“节点参数映射”表单和模板变量插入，避免用户只面对一大段 JSON。

### 2.3 画布节点和自定义面板

`web/src/types/canvas-plugin.ts` 定义插件节点协议。节点可声明：

- `Panel`：自定义设置面板。
- `Content`：自定义节点内容渲染。
- `toolbar`：节点悬浮操作。
- `useBuiltinPanel`：复用内置 image/video/text/audio 生成面板。
- `defaultMetadata`：节点初始参数。

`CanvasNodeContext` 提供节点 metadata 更新、上下游节点读取、事件总线、插件私有存储、AI 生成能力和打开/关闭 Panel 的操作。插件节点类型采用 `<pluginId>:<name>`，内置节点和插件节点共用渲染链。

这说明参考项目的“灵活工作流”主要来自**节点/插件扩展系统**，而不是 ComfyUI workflow 导入器。ainovel 不需要复制无限画布节点系统，但可以将 ComfyUI 工作流抽象成一个可编辑的配置实体，并为其提供独立的 Panel/API。

### 2.4 图片和日志持久化

`web/src/services/image-storage.ts` 使用 localForage/IndexedDB 保存图片 Blob 和生成日志：

- 图片 key 形如 `image:<nanoid>`。
- 节点 metadata 只保存 `storageKey`、宽高、大小、MIME、状态等。
- 通过 `resolveImageUrl` 按需创建 object URL。
- `cleanupUnusedImages` 扫描画布、资产和日志引用，清理无引用图片。

对 ainovel 的对应建议是：图片文件应落在 unit 所属目录，metadata 单独保存任务信息；Web 的图片接口只返回受控的媒体 URL，不让浏览器直接访问任意本地路径。若以后需要远程 WebDAV/多设备同步，可再引入类似 `storageKey` 的逻辑标识。

### 2.5 事件和本地 Agent

参考仓库的 `canvas-agent` 是一个独立的本地 Node/TypeScript Agent，HTTP 路由用于 Codex 对话、历史和中断；画布页面通过 Agent 读取/修改画布。它不是图片生成后端，也没有 ComfyUI 路由。

ainovel 当前已经有 Go `Host`、`Events()`、`Stream()`、`Snapshot()` 和 Web SSE。ComfyUI 任务状态应进入现有 Host 事件广播层，而不是由浏览器自行轮询 ComfyUI。

## 3. ComfyUI API 适配建议

### 3.1 ComfyUI API JSON 的基本形态

ComfyUI 的 API workflow 通常是“节点 ID -> 节点定义”的对象：

```json
{
  "3": {
    "class_type": "KSampler",
    "inputs": {
      "seed": 123,
      "steps": 28,
      "cfg": 7,
      "sampler_name": "euler",
      "scheduler": "normal",
      "positive": ["6", 0],
      "negative": ["7", 0],
      "model": ["4", 0],
      "latent_image": ["5", 0]
    }
  },
  "6": {
    "class_type": "CLIPTextEncode",
    "inputs": { "text": "a cinematic fantasy scene", "clip": ["4", 1] }
  },
  "7": {
    "class_type": "CLIPTextEncode",
    "inputs": { "text": "low quality, blurry", "clip": ["4", 1] }
  },
  "9": {
    "class_type": "SaveImage",
    "inputs": { "filename_prefix": "ainovel", "images": ["3", 0] }
  }
}
```

提交时通常是：

```json
{
  "prompt": { "3": { "class_type": "...", "inputs": {} } },
  "client_id": "ainovel-web-..."
}
```

返回 `prompt_id` 后，通过 `/history/{prompt_id}` 读取执行结果，从 `outputs` 中选择第一个图片输出，再调用 `/view?filename=...&subfolder=...&type=...` 下载图片。取消可以调用 `/interrupt`，但取消后的历史状态仍需由客户端判定并持久化为 `cancelled`。

### 3.2 ainovel 内部工作流实体

建议不要只保存原始 JSON，而是保存一个带版本和参数映射的实体：

```json
{
  "id": "wf-default",
  "name": "小说 unit 主图",
  "version": 1,
  "enabled": true,
  "workflow": { "3": { "class_type": "KSampler", "inputs": {} } },
  "bindings": [
    { "key": "positive_prompt", "node_id": "6", "path": "inputs.text", "type": "string", "required": true },
    { "key": "negative_prompt", "node_id": "7", "path": "inputs.text", "type": "string", "required": false },
    { "key": "seed", "node_id": "3", "path": "inputs.seed", "type": "integer", "required": false },
    { "key": "width", "node_id": "5", "path": "inputs.width", "type": "integer", "required": true },
    { "key": "height", "node_id": "5", "path": "inputs.height", "type": "integer", "required": true },
    { "key": "steps", "node_id": "3", "path": "inputs.steps", "type": "integer", "required": false },
    { "key": "cfg", "node_id": "3", "path": "inputs.cfg", "type": "number", "required": false }
  ],
  "defaults": {
    "negative_prompt": "low quality, blurry, malformed",
    "width": 1024,
    "height": 1024,
    "steps": 28,
    "cfg": 7
  },
  "output": {
    "node_id": "9",
    "input_path": "inputs.images",
    "index": 0,
    "mime": "image/png"
  },
  "created_at": "2026-08-14T00:00:00Z",
  "updated_at": "2026-08-14T00:00:00Z"
}
```

其中：

- `workflow` 保留用户导入的原始 API JSON，便于导出和排查。
- `bindings` 是可编辑的节点输入映射，前端可以通过节点 ID、输入路径和类型配置。
- `defaults` 是每次 unit 任务的默认参数；unit 级覆盖应另存，不修改模板。
- `output` 明确从哪个节点提取图片，避免“遍历所有 outputs 猜第一张”的不稳定行为。

前端导入时应接受“仅 API 格式 JSON”。若用户导入的是带 `nodes/links/extra` 的 ComfyUI UI workflow，应提示转换或要求在 ComfyUI 中导出 API 格式；不要静默猜测节点结构。

### 3.3 任务状态实体

每个 unit 一张图片，建议持久化：

```json
{
  "unit_id": "1-1-1",
  "workflow_id": "wf-default",
  "status": "queued",
  "prompt_id": "comfy-prompt-id",
  "client_id": "ainovel-...",
  "prompt": "...",
  "negative_prompt": "...",
  "parameters": { "width": 1024, "height": 1024, "steps": 28, "cfg": 7 },
  "output": { "filename": "001.png", "path": "drafts/01.units/001.png", "mime": "image/png" },
  "error": null,
  "started_at": "...",
  "finished_at": null,
  "attempt": 1
}
```

状态建议：`pending -> submitting -> queued -> running -> completed`，异常分支为 `failed/cancelled/timeout`。严格模式下只有 `completed` 才允许 unit 流程继续。

## 4. ainovel 的前后端边界

### 4.1 后端职责

Go 后端应负责：

1. 读取和校验 ComfyUI base URL（只允许 `http`/`https`，必须有 host，禁止 `file:`、空地址和不合法端口）。
2. 读取 workflow 文件并解析为 JSON object；校验节点含 `class_type` 和 `inputs`。
3. 根据 binding 的 JSON path 做类型化覆盖，拒绝越界路径、数组索引非法和类型不匹配。
4. 生成 `client_id`，提交 `/prompt`，保存 `prompt_id`。
5. 在 context 下轮询 `/history/{prompt_id}`，设置总超时和轮询间隔。
6. 解析执行错误（node、node_type、exception_message），通过 Host 事件广播。
7. 用户取消时取消本地 context，并尽力 POST `/interrupt`；即使 `/interrupt` 失败，也必须把本地任务标记为 cancelled。
8. 通过 `/view` 下载第一张图片，写入 unit 目录和 image metadata。
9. 恢复运行时根据 metadata 判断是否复用已完成图片、重试失败任务或继续 pending 任务。

### 4.2 Web 前端职责

Web 端应提供：

- ComfyUI 设置页：地址、鉴权（预留）、连接测试、超时、轮询间隔、严格模式开关。
- 工作流列表：导入 API JSON、导出、复制、删除、启用/停用。
- 工作流详情：原始 JSON CodeMirror 编辑器、节点输入映射表、默认参数表、输出节点选择。
- 测试生成：输入正/负提示词和参数，提交单次任务，显示实时状态和错误。
- 小说运行页：当前 unit 的图像任务状态、prompt_id、耗时、失败原因、取消/重试按钮。
- 图片展示接口：预留 `/media/...` 或 `/api/units/{chapter}/{unit}/image`，不要把本地绝对路径交给浏览器。

浏览器不应直接调用 ComfyUI。这样可以统一处理 CORS、地址校验、超时、取消、错误格式和本地文件写入，也符合当前 ainovel Web 只作为 Host 控制面的定位。

### 4.3 API 统一返回格式

用户要求的统一 envelope 应用于新增后端路由：

```json
{ "code": 0, "data": {}, "msg": "" }
```

建议路由：

```text
GET  /api/comfyui/config
PUT  /api/comfyui/config
POST /api/comfyui/test-connection
GET  /api/comfyui/workflows
POST /api/comfyui/workflows/import
GET  /api/comfyui/workflows/{id}
PUT  /api/comfyui/workflows/{id}
DELETE /api/comfyui/workflows/{id}
POST /api/comfyui/workflows/{id}/validate
POST /api/comfyui/jobs/test
GET  /api/comfyui/jobs/{id}
POST /api/comfyui/jobs/{id}/cancel
POST /api/comfyui/jobs/{id}/retry
GET  /api/units/{chapter}/{ordinal}/image
```

SSE 事件可继续沿用现有 Web 广播机制，新增 `comfyui.job.*` 类别，事件 payload 至少包含 `job_id`、`unit_id`、`status`、`progress/message` 和错误详情。

## 5. 异常、取消和安全兜底

- 地址校验失败应在保存配置和连接测试两个阶段都返回错误，不要等到运行 unit 才失败。
- HTTP 请求要绑定 `context.Context`；轮询等待使用可取消 timer，不能用无限 `time.Sleep`。
- 总超时、单请求超时和轮询间隔分开配置；默认值应有限制范围。
- ComfyUI `/history` 返回执行错误时，错误信息要包含节点 ID、节点类型和异常文本，并截断超长堆栈。
- `/view` 下载必须限制响应大小、检查 MIME，并拒绝空文件/非图片响应。
- 下载文件名、subfolder 和输出路径必须做路径清理，禁止路径穿越；最终文件必须位于项目 workspace/unit 目录内。
- 严格模式下图片任务失败暂停小说流程，并在 Web 中给出“检查 ComfyUI 服务、workflow、模型和显存”的可操作提示。
- 取消语义要区分用户取消、超时和服务端失败；三者都写入 metadata，方便恢复时决定是否自动重试。
- Web 默认绑定 `127.0.0.1`；若允许非回环地址，需要认证和 CSRF/来源校验，不能直接暴露模型密钥和写作控制接口。

## 6. 推荐实施顺序

1. 先建立 `internal/comfyui` 客户端、配置/工作流/任务数据结构和统一 API envelope，不接入写作流程。
2. Web 增加 ComfyUI 设置页和 workflow 导入/校验/测试任务页面，确认真实 ComfyUI 环境可用。
3. 将 Host 事件广播扩展为 ComfyUI job 事件，确保 TUI/Web 都能观察到任务状态。
4. 新增 Prompter role，生成结构化正/负提示词；先在测试任务页面使用。
5. 在 `write_chapter_unit` 成功后创建图片任务，严格模式下等待任务完成再进入下一个 unit。
6. 最后增加 unit 图片卡片、重试、断点恢复和章节级状态检查。

## 7. 不建议直接复制的部分

- 不要复制 Infinite Canvas 的浏览器端 API Key 存储和 `new Function` 任意脚本执行模式；ainovel 的小说内容、ComfyUI 地址和文件写入都应留在 Go 后端。
- 不要为了 ComfyUI 引入完整无限画布/插件节点系统；当前目标是 unit 图片生成，工作流实体和设置页面已经足够。
- 不要把 ComfyUI UI workflow 与 API workflow 混在同一字段；两者结构不同，导入时应显式标记格式。
- 不要让前端自行轮询 ComfyUI；任务状态应由后端统一管理并通过 SSE 广播。

