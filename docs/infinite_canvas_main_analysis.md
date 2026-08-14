# Infinite-Canvas-main ComfyUI 分析

## 1. 项目边界

`D:\Infinite-Canvas-main` 是 Python FastAPI (`main.py`) + 原生 HTML/JavaScript (`static/js/smart-canvas.js`) 的 ComfyUI 工作台。ComfyUI 调用由后端完成，浏览器只访问本项目 API；图片输入、输出和预览也经过本项目代理或本地 assets 目录。

需要区分两种 workflow：

- ComfyUI API workflow：`workflows/*.json`，顶层为 node id -> `{class_type, inputs}`。
- Infinite Canvas 画布 workflow：`nodes/connections/resources` JSON 或 ZIP，用于导入画布节点，不是 ComfyUI API JSON。

## 2. 关键后端模块和接口

### ComfyUI 执行

`GenerateRequest` 主要字段为 `prompt`、`width/height`、`workflow_json`、`params`、`type`、`client_id`。`generate()` 执行：

```text
选择 ComfyUI 实例
  -> 同步缺失的 input 图片
  -> 读取 workflow JSON
  -> 写入 prompt/尺寸/seed 和 params 节点覆盖
  -> POST /prompt，得到 prompt_id
  -> 每秒查询 /history/{prompt_id}
  -> 从 outputs 提取媒体
  -> GET /view 下载到本地 assets/output
  -> 写入 history.json 并广播结果
```

任务包装接口：

```text
POST /api/canvas-comfy-tasks              -> {task_id, status: queued}
GET  /api/canvas-comfy-tasks/{task_id}    -> queued/running/succeeded/failed
POST /api/generate                        -> 同步执行入口
GET  /api/view                            -> ComfyUI /view 的代理和本地回退
POST /api/comfyui/upload-base64           -> 上传图片到各 ComfyUI 的 /upload/image
```

### 多实例和地址校验

`COMFYUI_INSTANCES` 由逗号分隔的 `host:port` 组成。保存接口会去协议前缀/尾斜杠、检查 host 和端口、去重，并更新 `.env` 与进程变量。`reserve_best_backend()` 查询每个实例 `/queue`，按远程队列长度和本地任务数选择负载最低实例；若目标实例缺少引用图，会从其他实例 `/view?type=input` 下载并 `/upload/image` 同步。

### Workflow 文件 API

```text
GET    /api/workflows
GET    /api/workflows/{name}
POST   /api/workflows                 # 上传 API JSON
PUT    /api/workflows/{name}/config  # 保存字段映射
DELETE /api/workflows/{name}
POST   /api/workflows/{name}/run      # 测试运行
GET/PUT /api/comfyui/instances
```

上传只接受非空 API JSON，并检查节点含 `class_type`；名称限制为安全字符和 `custom/` 子目录。路径通过正则 + `commonpath` 防止目录穿越，内置 workflow 不允许删除。

## 3. Workflow 字段映射

原始 workflow 与 UI 配置分开保存：

```json
{
  "title": "示例工作流",
  "fields": [
    {
      "id": "6::text",
      "node": "6",
      "input": "text",
      "name": "正向提示词",
      "type": "textarea",
      "default": "",
      "min": 0,
      "max": 100,
      "step": 1,
      "options": [],
      "random_enabled": false
    }
  ],
  "mini_cards": {}
}
```

字段类型支持 `text`、`textarea`、`number`、`slider`、`boolean`、`dropdown`、`image`、`video`、`audio`。前端根据字段生成文本框、开关、下拉、数字控件和骰子随机按钮；后端 `run_workflow` 再把值转换成 `{node: {input: value}}`，避免只信任前端类型。

`smart-canvas.js` 的自定义 workflow 数据流：

```text
GET /api/workflows/{name}
  -> 缓存 workflow + config
  -> prompt 字段写入当前提示词
  -> image/video/audio 字段先上传并写入 ComfyUI 文件名
  -> setting 字段读取当前值或 default
  -> 类型转换为 params
  -> POST /api/canvas-comfy-tasks
```

动态重绘会保存 popover 的 pinned/interacting 状态、面板滚动位置和尺寸选择器滚动位置，避免修改参数后界面跳动。这是 ainovel ComfyUI 设置页可直接复用的交互细节。

## 4. 输出、预览和持久化

后端按扩展名/format 将 outputs 分类为 image/video/audio/text/file，并保留 `node_id`、`output_key`、`class_type`。对 `PreviewImage`、`ImageComparer` 等节点做过滤：存在正式图片时丢弃预览图；只有预览图时才保留。`ShowText`、`Debug`、`Note` 等文本默认不混入最终结果。

结果项类似：

```json
{
  "url": "/assets/output/workflow_...png",
  "kind": "image",
  "node_id": "9",
  "output_key": "images",
  "class_type": "SaveImage"
}
```

持久化位置：

- `workflows/*.json`：原始 ComfyUI API workflow。
- `workflows/*.config.json`：字段映射和 UI 元数据。
- `history.json`：最近生成记录，包括 prompt、outputs、prompt_id、backend、params。
- `assets/input` / `assets/output`：输入和生成文件。
- `data/canvases`：画布节点、运行设置和历史分组。

前端把生成结果写入图片节点和历史分组，并保存 prompt、引用、settings、运行时间；图片通过 `/assets`、`/api/view` 或本地输出代理预览。

## 5. 异常与可复用设计

可复用：

- workflow JSON 与字段 schema 分离，字段映射可配置而不是硬编码节点。
- 后端统一完成 `/prompt`、`/history`、`/view` 和输入上传，前端不直连 ComfyUI。
- 多实例按队列负载选择，并同步输入媒体。
- 输出保留来源节点信息并过滤预览/调试节点。
- 本地文件优先、上游 `/view` 回退，适合 Web 图片展示。
- 前端动态参数控件可覆盖尺寸、seed、采样器等任意 workflow 输入。

需要在 ainovel 改进：

- 当前任务只存在 `CANVAS_TASKS` 内存字典，进程重启后 task id 失效；应把 unit image job metadata 持久化。
- `/api/canvas-comfy-tasks` 没有取消路由，前端轮询没有 AbortController；应增加 context 取消、`/interrupt`、`cancelled/timeout` 状态。
- ComfyUI 轮询固定 1 秒、最长默认 1800 秒；应拆分请求超时、总超时和轮询间隔并允许配置。
- workflow 校验目前主要检查首个节点 `class_type`；应遍历节点、校验 links、字段类型/范围/枚举和 output selector。
- API 返回格式未统一；ainovel 新接口应统一 `{ "code": 0, "data": {}, "msg": "" }`。
- 地址当前仅字符串检查 `host:port`；ainovel 应使用 URL 解析、连接测试、SSRF/路径安全和默认回环监听。

## 6. 对 ainovel 的接口建议

建议保留以下实体：

```text
ComfyWorkflow: id/name/title/api_json/fields/output_selector/version/hash
UnitImageJob: id/unit_id/workflow_id/status/prompt_id/backend/parameters/output/error/timestamps
```

建议路由：

```text
GET/PUT  /api/comfyui/instances
GET      /api/comfyui/workflows
POST     /api/comfyui/workflows/import
GET/PUT  /api/comfyui/workflows/{id}
DELETE   /api/comfyui/workflows/{id}
POST     /api/comfyui/workflows/{id}/validate
POST     /api/comfyui/jobs
GET      /api/comfyui/jobs/{id}
POST     /api/comfyui/jobs/{id}/cancel
POST     /api/comfyui/jobs/{id}/retry
GET      /api/units/{chapter}/{ordinal}/image
```

Prompter 生成的正/负提示词可以映射到字段 `type=textarea` 的指定节点；unit 引用图映射到 `type=image` 字段并先上传到 ComfyUI；每个 unit 最终选择一个配置的 output selector，其他输出仅保留 metadata 供排错。
