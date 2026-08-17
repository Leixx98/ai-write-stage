# ComfyUI Canvas 工作台架构 v2

本文依据 `docs/infinite_canvas_canvas_analysis_v2.md` 定义 ainovel Web 工作台的画布层。画布是 workflow 的编辑和测试投影，不改变 ComfyUI API workflow 的原始语义；浏览器仍然只调用 ainovel 后端，后端负责绑定参数、媒体上传、任务执行和持久化。

## 1. 分层模型

```text
API workflow JSON                 # ComfyUI /prompt 输入
  node_id -> {class_type, inputs}
          |
          +-- WorkflowConfig       # fields/bindings/defaults/output selectors
          |
          +-- CanvasDocument       # 节点位置、边、viewport、mini test cards

Canvas UI -> fields + test_cards -> POST run -> ImageJob -> ComfyUI instance
```

三类 JSON 必须有独立 `format`：

| 格式 | 用途 | 是否可直接 POST `/prompt` |
| --- | --- | --- |
| `comfyui_api_v1` | 原始 API workflow | 是（应用 bindings 后） |
| `ainovel_workflow_config_v1` | 控件、字段、默认值、输出选择器 | 否 |
| `ainovel_comfy_canvas_v1` | 画布节点、连线、视口和测试卡 | 否 |

CanvasDocument 的删除、移动、缩放和字段勾选都不应改写 API workflow；运行时只读取 CanvasDocument 的 `field_values` 和 mini test card values。

## 2. CanvasDocument 数据模型

### 2.1 根对象

```json
{
  "format": "ainovel_comfy_canvas_v1",
  "version": 1,
  "id": "canvas-wf-sdxl",
  "workflow_id": "wf-sdxl",
  "title": "SDXL unit image",
  "nodes": [],
  "edges": [],
  "viewport": {"x": 0, "y": 0, "scale": 1},
  "fields": [],
  "mini_test_cards": [],
  "updated_at": "2026-08-14T00:00:00Z"
}
```

`workflow_id` 必须指向同一项目中的 API workflow。Canvas 可独立保存草稿，但不能引用不存在的 workflow。

### 2.2 节点

```json
{
  "id": "node-6",
  "kind": "workflow",
  "source_node_id": "6",
  "class_type": "CLIPTextEncode",
  "label": "Positive prompt",
  "x": 320,
  "y": 140,
  "width": 180,
  "height": 96,
  "collapsed": false,
  "exposed_field_ids": ["6::text"],
  "locked": false,
  "metadata": {}
}
```

`kind` 为 `workflow`、`prompt`、`media`、`comfy`、`output` 或 `note`。只有 `workflow` 节点必须有 `source_node_id`；mini card 使用其他 kind。尺寸有默认值和最大值，前端不能让动态文本撑大整个画布容器。

### 2.3 边

```json
{
  "id": "edge-6-3-0",
  "source": "node-6",
  "source_handle": "output-0",
  "target": "node-3",
  "target_handle": "positive",
  "kind": "workflow",
  "label": "positive"
}
```

边只表示 API workflow 中的连接或 mini card 的数据流。保存时校验两端节点存在；workflow 边的真实连接仍以 API JSON 的 `[node_id, output_index]` 为准，画布边不能偷偷修改 API JSON。

### 2.4 视口

```json
{"x": 0, "y": 0, "scale": 1, "min_scale": 0.2, "max_scale": 3}
```

`scale` 必须在 `0.2..3`；服务端保存有限精度（建议 4 位小数）。前端 SVG/DOM 容器高度固定并 `overflow:hidden`，拖拽、缩放和节点弹窗不能改变页面整体高度。

### 2.5 字段暴露

```json
{
  "id": "6::text",
  "node_id": "6",
  "input": "text",
  "name": "Positive prompt",
  "control": "textarea",
  "value_type": "string",
  "default": "",
  "min": null,
  "max": null,
  "step": null,
  "options": [],
  "required": false,
  "random_enabled": false,
  "exposed": true,
  "source": "inferred"
}
```

`fields` 是对 WorkflowConfig.fields/bindings 的画布投影；字段 id 必须唯一并能定位 API node/input。`control` 支持 `text`、`textarea`、`number`、`slider`、`dropdown`、`boolean`、`image`、`video`、`audio`、`file`。字段值的最终类型、范围和枚举由后端再次校验。

### 2.6 Mini test cards

Mini test cards 是固定的测试拓扑，不等同于完整 API graph：

```json
[
  {"id":"prompt_1","kind":"prompt","x":36,"y":96,"text":""},
  {"id":"image_1","kind":"media","media_type":"image","x":36,"y":286,"media_ref":null},
  {"id":"comfy_1","kind":"comfy","x":330,"y":150,"workflow_id":"wf-sdxl"},
  {"id":"output_1","kind":"output","x":670,"y":190,"job_id":null,"output_index":0}
]
```

`prompt` 卡按顺序映射到 `positive_prompt`/textarea fields；media 卡按顺序映射到 image/video/audio bindings；comfy 卡只选择 workflow/instance；output 卡展示最近一次 job 输出。无法映射时必须显示 warning，不得静默覆盖 field values。

## 3. 前端工作台布局和交互

页面采用三栏固定布局：

```text
左栏：workflow 列表、实例/运行参数摘要、字段预览
中栏：API 拓扑 SVG（Workflow 模式）或 Mini Test Canvas（Test 模式）
右栏：节点字段弹窗/编辑器、Output 预览、job 状态
```

工作流节点按拓扑层级自动布局，用户拖动后只更新 `CanvasDocument.nodes[].x/y`；缩放更新 viewport。节点字段弹窗显示 class_type、node id、未连接 inputs，以及暴露字段的控件。输入控件事件必须阻止画布拖动和滚轮缩放；Escape/遮罩关闭弹窗。

Test 模式只显示 prompt/media/comfy/output mini cards，隐藏完整 SVG 拓扑。点击 Run 后禁用当前 card 的 Run，状态栏显示 `pending/selecting/uploading/submitting/queued/running/downloading/succeeded`；失败保留上一次 Output 并提供 Cancel/Retry。

## 4. HTTP API 契约

所有 JSON 响应统一：

```json
{"code": 0, "data": {}, "msg": ""}
```

SSE 不使用 HTTP envelope，而使用：

```json
{"event_id":"evt-1","type":"comfyui.job.progress","time":"...","data":{"job_id":"img-1","status":"running","progress":0.42}}
```

### 4.1 Workflow

```text
GET    /api/v2/comfyui/workflows
POST   /api/v2/comfyui/workflows/import
GET    /api/v2/comfyui/workflows/{id}
PUT    /api/v2/comfyui/workflows/{id}
DELETE /api/v2/comfyui/workflows/{id}
GET    /api/v2/comfyui/workflows/{id}/schema
PUT    /api/v2/comfyui/workflows/{id}/config
GET    /api/v2/comfyui/workflows/{id}/export?format=api|config
```

详情成功 data：

```json
{
  "workflow": {"format":"comfyui_api_v1","id":"wf-sdxl","api_json":{}},
  "config": {"format":"ainovel_workflow_config_v1","fields":[],"bindings":[]},
  "canvas": {"format":"ainovel_comfy_canvas_v1","nodes":[],"edges":[],"viewport":{}}
}
```

导入只接受 API JSON，建议请求：

```json
{"format":"comfyui_api_v1","id":"wf-sdxl","name":"SDXL","api_json":{},"config":{},"canvas":{}}
```

当前实现接受旧的直接 `{workflow, bindings, defaults, output}` body 作为兼容输入。含 `nodes`/`links` 的 ComfyUI UI JSON 或 canvas JSON 返回 `3002`，data 至少包含 `detected_format` 和转换提示。导出 `format=api` 返回原始 API JSON；`format=config` 返回字段配置；没有 canvas 文档时不伪造 `format=canvas`。

### 4.2 Canvas

```text
GET /api/v2/comfyui/workflows/{id}/canvas
PUT /api/v2/comfyui/workflows/{id}/canvas
```

GET 不存在时返回默认自动布局的 CanvasDocument（`code=0`，不立即落盘）；PUT 要求完整文档或带 `replace:false` 的局部更新。局部更新只允许替换 `nodes/edges/viewport/fields/mini_test_cards`，不允许改变 `workflow_id`。服务端校验：节点/边 ID 唯一、引用存在、坐标有限、scale 范围、field 引用存在、mini card kind 合法。

PUT 成功 data 返回规范化后的完整 CanvasDocument 和 `revision`。并发编辑可带 `If-Match: <revision>`；版本不一致返回 `1003`，data 包含 `current_revision`。

### 4.3 Run、状态、取消、重试和输出

```text
POST /api/v2/comfyui/workflows/{id}/run
GET  /api/v2/comfyui/jobs/{job_id}
POST /api/v2/comfyui/jobs/{job_id}/cancel
POST /api/v2/comfyui/jobs/{job_id}/retry
GET  /api/v2/comfyui/jobs/{job_id}/outputs
GET  /api/v2/comfyui/jobs/{job_id}/outputs/{index}
```

Run 请求：

```json
{
  "mode":"test|unit",
  "unit_id":"1-1-1",
  "instance_id":"local-8188",
  "field_values":{"6::text":"...","3::steps":28},
  "mini_test_values":{"prompt_1":"...","image_1":{"media_id":"..."}},
  "client_id":"ainovel-web"
}
```

后端将 field/mini values 映射到 bindings 的深拷贝，应用优先级为本次请求 > field default > workflow default > API node 原值。成功创建返回 `202` 和 job，状态从 `pending` 开始；任务状态持久化，轮询 ComfyUI `/history/{prompt_id}`，取消使用 context + best-effort `/interrupt`，Retry 增加 attempt 并生成新 prompt_id。Job 终态包括 `succeeded`、`failed`、`timeout`、`cancelled`；旧 `completed` 读写兼容为 `succeeded`。

Outputs data 是数组，每项包含：

```json
{"index":0,"kind":"image","mime":"image/png","node_id":"9","output_key":"images","previewable":true,"url":"/api/v2/comfyui/jobs/img-1/outputs/0"}
```

image/video/audio 使用受控媒体响应；text 返回截断预览和 metadata；未知格式归类为 file。unit 模式只选择一个主 image 输出，其他输出保存在 job metadata 和测试页面。

## 5. 字段控件和错误语义

### 5.1 控件约束

| control | value_type | 约束 |
| --- | --- | --- |
| `text`/`textarea` | string | 最大 rune 数由 schema 指定 |
| `number`/`slider` | integer/number | min/max/step 后端强校验 |
| `boolean` | boolean | 不接受字符串 `"true"` |
| `dropdown` | string/number | 必须在 options 中，除非 `allow_custom` |
| `image`/`video`/`audio`/`file` | media_ref | 先上传/引用，再绑定 ComfyUI upload name |

随机 seed 只允许 integer/number 且由后端生成；浏览器显示随机按钮但不直接改 workflow。输入媒体大小、MIME、路径和 sha256 均由后端校验。

### 5.2 错误

| HTTP/code | 场景 | data |
| --- | --- | --- |
| 400/1001 | JSON、字段值或 viewport 非法 | `field`, `path`, `expected` |
| 404/1002 | workflow/canvas/job/output 不存在 | `resource`, `id` |
| 409/1003 | job 状态冲突或 Canvas revision 冲突 | `current_status`, `current_revision` |
| 400/2001 | 实例/ComfyUI 配置非法 | `phase`, `base_url`（脱敏） |
| 400/3002 | API/UI/canvas workflow 格式或 config 不合法 | `detected_format`, `errors[]` |
| 502/3001 | ComfyUI 不可达 | `phase`, `instance_id`, `retryable` |
| 500/3003 | ComfyUI 执行失败 | `job_id`, `node_id`, `node_type`, `exception` |
| 504/3004 | 总超时 | `job_id`, `retryable` |
| 409/3005 | 已取消或重复取消 | `job_id`, `status` |
| 400/3006 | 无可用主图输出 | `outputs[]`, `selector` |

错误 `data` 始终为 object，`msg` 给出用户可读信息；不返回 API key、绝对路径或完整 prompt secret。

## 6. 持久化边界

```text
meta/comfyui/workflows/<id>.json       # 现有聚合兼容文件
meta/comfyui/workflows/<id>.api.json   # API workflow
meta/comfyui/workflows/<id>.config.json
meta/comfyui/workflows/<id>.canvas.json
meta/comfyui/jobs/<job_id>.json
meta/comfyui/instances.json
meta/comfyui/media/<sha256>.json
assets/input/<sha256>.<ext>
drafts/<chapter>.units/<ordinal>.png   # unit 主图
```

Canvas PUT 应原子写入 `.canvas.json`；workflow/config 保存失败时不能更新 canvas revision。删除用户 workflow 时同时删除 config/canvas，内置 workflow 不可删除。旧聚合 `.json` 继续可读，首次编辑时拆分写入新文件；迁移完成前不删除旧文件。

## 7. 现有接口与页面迁移

### 保留并复用

- `GET/PUT /api/v2/comfyui/config` 和 `POST /api/v2/comfyui/test-connection`：保留为高级实例兼容设置，统一 envelope。
- `GET/PUT /api/v2/comfyui/instances`、`POST /instances/{id}/test`：作为画布左栏实例选择的后端来源。
- `GET/POST /api/v2/comfyui/workflows*`、`jobs/*`、`media/upload`、`units/*/image`：保留路由和旧字段映射。
- 现有 SSE 事件广播和 `X-API-Code: 0` 媒体响应。

### 隐藏或移除前端入口

- 旧 ComfyUI 单页中直接编辑 `workflow-json`、长 `defaults`、`output selector` 和 bindings 表格的主入口应隐藏；保留“高级 JSON/配置”折叠面板用于排查和导出。
- 独立的 Job test 大日志面板改为 Canvas Output 卡；完整 job metadata 通过详情抽屉查看。
- 顶部 `Base URL/timeout/poll/client/max bytes` 表单改为实例设置摘要；多实例编辑进入单独实例面板。
- 旧 `/api/comfyui/*`（无 v2）若存在，仅作为只读/迁移兼容，不再由新页面调用；不能删除已有项目数据。

### 不兼容输入

- ComfyUI UI workflow（`nodes` 数组、`links`）不得当作 API workflow 保存。
- CanvasDocument 不得传给 `/prompt` 或旧 `workflow` 字段。
- 旧 `completed` 状态在读取时映射，但新页面统一显示 `succeeded`。

## 8. 实施顺序

1. 后端增加 CanvasDocument Store、GET/PUT canvas 和 revision 校验，不改变现有 workflow/job API。
2. 前端把 ComfyUI 页拆为 workflow 列表、固定高度 SVG graph、Test canvas 和 Output 卡，先复用现有字段 schema API。
3. 将 field expose/override 写入 WorkflowConfig，Canvas 只保存投影和位置；完善媒体 card 到 `/media/upload` 的引用。
4. 将 run/cancel/retry/job SSE 接入 Output 卡，严格模式下 unit job 完成前保持 Host 推进门。
5. 最后隐藏旧长 JSON 表单，保留高级抽屉和迁移兼容。

