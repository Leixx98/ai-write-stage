# Infinite Canvas ComfyUI 画布复刻分析（v2）

本文基于 `D:\Infinite-Canvas-main` 的 `static/comfyui-settings.html`、
`static/js/comfyui-settings.js`、`static/css/comfyui-settings.css` 和 `main.py`，
并对照 ainovel-cli 当前 `internal/entry/web/static/index.html`、`app.js`、
`v2.go`、`internal/comfyui` 和 `internal/store`，给出 ComfyUI 前台画布的迁移建议。

## 1. 参考仓库页面结构

参考实现把 ComfyUI 页面拆成一个两栏工作台，而不是将所有配置堆在一个表单里：

- 左栏（`comfyui-settings.html` 的 `.sidebar`）：工作流列表、导入按钮、ComfyUI 后端地址和运行参数的实时预览。
- 中央栏（`.content`）：当前工作流标题、保存/删除操作、`工作流` 与 `测试画布` 两个模式、图形画布、缩放控制和节点编辑弹窗。
- 图模式下节点列表默认折叠，只有需要完整检查节点时才展开，避免长工作流把页面撑长。
- 测试画布模式隐藏 SVG 工作流图，显示可拖拽、可缩放的测试节点（Prompt、输入媒体、ComfyUI、Output），控件在对应节点内直接编辑。
- 结果使用图片 lightbox（`#imageLightbox`）放大，不在页面中打开新窗口。

CSS 中 `.graph-svg-wrap` 高度固定为 `760px`，`.mini-canvas.large` 也固定为 `760px`，并通过 `overflow:hidden` 保证拖动和新增控件不会改变页面尺寸。这一点可直接解决 ainovel 当前流式/动态区撑大的问题。

## 2. 工作流导入、持久化和 API

### 2.1 API JSON 导入

`comfyui-settings.js:onUpload` 读取文件文本并解析 JSON，要求用户填写工作流名称，然后 POST：

```http
POST /api/workflows
Content-Type: application/json

{"name":"<name>","workflow":<ComfyUI API JSON>}
```

`main.py:upload_workflow` 的关键行为：

1. 只接受 `.json`，名称限制为安全字符并归一化到 `workflows/custom/<name>.json`。
2. 首个节点必须是对象并包含 `class_type`，因此 ComfyUI UI 格式（含 `nodes` 数组）会被拒绝，提示用户导出 API 格式。
3. 工作流 JSON 和配置 JSON 分开保存，配置路径为同名 `.config.json`。

### 2.2 列表和详情

```http
GET /api/workflows
GET /api/workflows/{name}
```

列表返回 `name/title/builtin/field_count`；详情返回 `workflow` 原始 API JSON、`config` 和 `builtin`。前端选择列表项后同时初始化：

- `currentWorkflow`：原始节点字典；
- `currentConfig`：`{title, fields, mini_cards}`；
- `previewValues`：当前测试值；
- `miniCards` / `miniTestNodes`：测试画布布局和节点。

### 2.3 配置保存

```http
PUT /api/workflows/{name}/config
Content-Type: application/json

{"title":"...","fields":[...],"mini_cards":{...}}
```

`mini_cards` 保存测试画布中固定节点的位置（`prompt/image/custom/output` 的 `x/y`）。当前参考实现没有把 SVG 图的自动布局坐标写回配置，SVG 图每次根据拓扑重新计算；测试画布位置才是用户可持久化状态。

### 2.4 运行工作流

```http
POST /api/workflows/{name}/run
{
  "fields": {"<field-id>": value},
  "config": {"title":"...","fields":[...]},
  "client_id": "workflow-test"
}
```

后端遍历 `config.fields`，用 `field.node` 和 `field.input` 组成 ComfyUI 节点覆盖值，再调用统一 `generate` 逻辑；数值、布尔、下拉值会先转换为 ComfyUI 可接受的类型。返回 `images` 等输出数组，前端显示第一张图片。

## 3. SVG 工作流图实现

### 3.1 节点和边提取

`computeLayers()` 遍历 API JSON 的每个 `inputs` 值：形如 `[upstreamNodeId, outputIndex]` 的数组被识别为边。函数生成 `incoming/outgoing` 集合并从无入边节点开始 DFS，计算每个节点的拓扑层级；遗漏节点和环会回退到第 0 层。

`renderGraph()` 使用固定卡片尺寸：`NODE_W=130`、`NODE_H=50`、水平间距 `36`、垂直间距 `14`。同一层的节点按 ID 排序纵向排列，层级按列排列。每条边渲染为 SVG 三次贝塞尔路径：

```text
M (from.x + NODE_W, from.y + NODE_H/2)
C (mid.x, from.y), (mid.x, to.y), (to.x, to.y + NODE_H/2)
```

节点 `g.gnode` 带有 `data-node-id`、类别 class（`cat-prompt/loader/sampler/image/output/...`）和 `has-exposed` class。类别只用于浅色背景/边框，不改变数据。

### 3.2 缩放、平移和适配

图形根组 `#graphViewport` 使用 `translate(x,y) scale(k)`。状态是：

```js
let graphView = { k: 1, x: 0, y: 0 };
```

- 鼠标滚轮以指针位置为中心缩放，范围 `0.2..3`；
- 空白区拖动平移，节点点击不会触发平移；
- `graphZoom(+/-)` 以容器中心缩放；
- `graphFit()` 根据内容宽高与容器尺寸计算 `k`，并将图居中；
- SVG 容器固定高度、隐藏溢出，避免节点或边把页面撑高。

参考实现没有使用第三方画布库，SVG + DOM 足以迁移到 ainovel 的原生 JS。

## 4. 节点字段配置弹窗

### 4.1 节点点击和弹窗

每个 `g.gnode` 的 `onclick` 调用 `openNodePopup(nodeId, this)`。弹窗包括：图标、友好节点名、`class_type` 和 ID，以及该节点所有非链接输入。

弹窗定位按节点 `getBoundingClientRect()` 计算：优先放右侧，右侧空间不足放左侧；顶部/底部按最大高度（容器高度和窗口高度 70% 的较小值）裁剪。`.popup-backdrop` 覆盖图区域，点击或 Escape 关闭。`.popup-body` 独立滚动，滚轮不会继续缩放画布。

### 4.2 字段暴露（可配置项）

节点输入行由 `renderInputRow(nodeId, inputKey, rawValue)` 生成：

- 未勾选：只显示输入名和原始默认值；
- 勾选：创建/保留一个 `fields[]` 项，显示自定义名称输入框、控件类型下拉框和类型附加参数；
- 再次点击复选框：移除字段并删除对应测试值；
- 输入链接（`[nodeId, outputIndex]`）被过滤，不允许直接暴露为用户参数。

字段对象（参考 `WorkflowField`）如下：

```json
{
  "id": "f_xxx",
  "node": "12",
  "input": "steps",
  "name": "采样步数",
  "type": "number",
  "default": 20,
  "min": 1,
  "max": 50,
  "step": 1,
  "options": [],
  "random_enabled": false
}
```

支持类型：`text`、`textarea`、`number`、`slider`、`dropdown`、`image`、`video`、`audio`、`boolean`。`guessType()` 根据原始值和字段名推断初始类型（prompt/text -> textarea，image/file -> image，seed/strength 等数值 -> slider/number）。

数值字段可配置 min/max/step/default；number 可以启用随机骰子。dropdown 允许增删选项，前端会把看起来像数字的字符串转换为数字后发送给 ComfyUI。

### 4.3 左栏实时控件

`renderPreview()` 将 `currentConfig.fields` 按类型渲染到左侧预览面板：文本、文本域、数字、滑块、下拉、开关和媒体上传。所有控件只写入 `previewValues[field.id]`，不会修改原始工作流。媒体先生成浏览器 Blob URL 立即预览，再 POST `/api/upload` 获取 ComfyUI 文件名作为实际运行值。

## 5. 测试画布实现

### 5.1 节点模型

测试画布初始化为四个固定节点：

```js
[
  {id:'prompt_1', type:'prompt', x:36, y:96, text:''},
  {id:'image_1', type:'image', x:36, y:286, url:'', value:''},
  {id:'comfy_1', type:'comfy', x:330, y:150},
  {id:'output_1', type:'output', x:670, y:190}
]
```

用户可以用工具栏新增 prompt/image/video/audio 节点、删除 prompt/image 节点；ComfyUI 和 Output 节点固定存在。每个节点是 `.mini-card`，端口是 `.mini-port.in/.out`，连线是绝对定位的 `.mini-line`（通过两卡片中心点计算长度和旋转角度）。

ComfyUI 节点内部显示当前工作流的媒体字段数量、prompt 字段数量和 setting 控件，并直接放置“运行测试”按钮。Output 节点显示最后一次结果。

### 5.2 拖动和缩放

状态：`miniView={k:1,x:0,y:0}`、`miniCards` 保存固定节点位置、`miniTestNodes` 保存动态节点位置和内容。

- 画布滚轮改变 `miniView.k`（`0.45..1.8`），以指针位置为中心；
- 空白区拖动改变 `miniView.x/y`；
- 卡片标题区拖动改变节点 `x/y`，坐标除以当前缩放比例；
- 输入框、下拉、按钮、媒体区域会阻止拖动事件；
- 鼠标抬起后重新渲染以更新连线和控件。

参考样式 `.mini-canvas` 使用固定高度、点阵背景、`overflow:hidden`；桌面大画布 760px，窄屏时布局切换为单栏。

### 5.3 从画布组装运行参数

`fieldsFromMiniCanvas()` 将测试节点内容映射回字段：

- 所有 prompt 节点文本按顺序以空行拼接，写入每个 prompt 字段；
- 第 N 个 image/video/audio 节点的 `value` 写入同类型字段的第 N 项；
- 未在画布中提供值时回退到 `previewValues`；
- setting 字段一直使用 ComfyUI 节点卡中的输入值。

然后 `onRun()` 调用 `/api/workflows/{name}/run`。运行期间按钮禁用、状态栏显示 running；成功后将第一张 `images[0]` 放入 Output 节点和右侧结果区，点击图片打开 lightbox。

## 6. 异常、校验和边界行为

- 工作流名称和路径由后端正则及 `commonpath` 校验，防止目录穿越。
- 导入 JSON 解析失败、格式不是 API JSON、服务端验证失败都会在状态栏/alert 给出可读错误。
- ComfyUI 地址保存时强制 `host:port`，去除 `http(s)://` 和尾部 `/`，空地址或非数字端口拒绝保存。
- 上传媒体失败会保留本地预览并提示用户；运行时仍使用之前的字段值。
- 内置工作流不能删除；自定义工作流删除会同时删除 API JSON 和 config JSON。
- 弹窗、画布滚轮、输入控件均有事件隔离，防止滚轮冒泡导致画布意外缩放。
- 运行接口返回错误时恢复按钮状态，保留之前结果，不让整个页面卡死。

## 7. 对 ainovel 当前页面的差距

当前 `internal/entry/web/static/index.html` 的 ComfyUI 区域是三列后台表单：实例地址/超时/strict、工作流 JSON 文本框、bindings/defaults/output selector 和单独的 Job test 区。这造成用户必须理解内部契约才能运行，且节点没有图形位置、连线或可视化字段编辑。

建议移除或折叠以下前台元素（后端配置仍保留 API）：

1. 顶部直接展示的 timeout、poll interval、client id、max response bytes、default workflow id、strict 开关；改为服务端默认值，错误时给出环境检查提示，只有高级设置抽屉保留。
2. 原始 `workflow-json`、长 `defaults`、`output selector` 文本框；导入后只在节点弹窗和字段面板中编辑，原始 JSON 可放“高级查看”对话框。
3. bindings 表格和手工 Add parameter；改为节点输入行的勾选 + 自定义名称/控件类型。
4. 单独的 Job test 日志大面板；改为画布 Output 节点的运行状态、取消/重试按钮和结果预览，完整日志可用抽屉查看。

保留并复用当前后端能力：统一 envelope、ComfyUI URL 校验、实例选择、队列、作业取消/重试、输出分类和媒体引用。

## 8. 建议迁移到 ainovel 的数据模型与接口

### 8.1 CanvasDocument

在现有 `Workflow`/`WorkflowConfig` 外增加前端画布文档（可以先存入 config JSON）：

```json
{
  "format": "ainovel_comfy_canvas_v1",
  "workflow_id": "wf-id",
  "viewport": {"x": 0, "y": 0, "scale": 1},
  "graph_layout": {"12": {"x": 40, "y": 80}},
  "test_nodes": [
    {"id":"prompt_1","type":"prompt","x":36,"y":96,"text":""},
    {"id":"image_1","type":"image","x":36,"y":286,"value":""},
    {"id":"comfy_1","type":"comfy","x":330,"y":150},
    {"id":"output_1","type":"output","x":670,"y":190}
  ],
  "fields": []
}
```

`graph_layout` 可选：自动拓扑布局不需要持久化；若以后支持用户拖动真实工作流节点，再保存该字段。`viewport` 与 `test_nodes` 必须保存，否则刷新会丢失工作台状态。

### 8.2 推荐接口契约

保持现有 `/api/v2/comfyui` 前缀和 `{code,data,msg}` envelope，增加或调整：

```text
GET    /api/v2/comfyui/workflows
POST   /api/v2/comfyui/workflows/import        # API JSON，返回 workflow + canvas
GET    /api/v2/comfyui/workflows/{id}         # workflow、config、canvas
PUT    /api/v2/comfyui/workflows/{id}         # 字段配置 + canvas 状态
DELETE /api/v2/comfyui/workflows/{id}
POST   /api/v2/comfyui/workflows/{id}/run     # fields/test_nodes，返回 job
GET    /api/v2/comfyui/jobs/{id}              # 状态、阶段、日志
POST   /api/v2/comfyui/jobs/{id}/cancel
POST   /api/v2/comfyui/jobs/{id}/retry
GET    /api/v2/comfyui/jobs/{id}/outputs
POST   /api/v2/comfyui/media/upload
```

所有响应都包装为 `{ "code": 0, "data": ..., "msg": "" }`；错误数据应至少包含 `phase`、`retryable`、`workflow_id`，URL/超时错误不要返回敏感凭据。

## 9. 推荐实施顺序

1. 前端先把 ComfyUI 顶层页面改成“工作流列表 + 中央画布 + 左侧运行控件/结果”的固定布局，复用现有接口。
2. 实现 API JSON 导入后的拓扑 SVG 图、缩放/平移/适配和节点弹窗；字段勾选直接生成 `bindings`。
3. 加入测试画布（prompt/media/comfy/output 节点）、位置持久化和单元图片测试。
4. 后端补充 canvas 字段读写、上传到选中 ComfyUI 实例、作业状态轮询/取消，并确保所有错误都返回统一 envelope。
5. 最后删除旧表单 DOM 和无用 JS 事件，保留高级配置抽屉；用桌面和窄屏浏览器验收导入、字段编辑、运行、取消、重试、输出预览。

