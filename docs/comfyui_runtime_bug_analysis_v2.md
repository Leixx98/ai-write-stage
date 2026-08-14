# ComfyUI 运行问题分析（v2）

## 结论摘要

本轮现象不是 ComfyUI 推理失败，而是 Web 画布投影、旧版 Workflow Config 和输出分类之间没有形成一致的数据链：

1. 字段取消勾选不生效：节点编辑器在 Inspector 和 Modal 中渲染了两份相同的复选框；保存时固定优先读取 Modal，而用户实际点击的通常是 Inspector，导致旧的 checked 状态覆盖了取消操作。
2. prompt 没有生效：用户编辑的字段保存在 `<workflow>.canvas.json`，但运行接口加载 Workflow 后只读取 `<workflow>.config.json` 中的 bindings。当前运行样本的 config 和主 workflow 都是空 bindings，因此 `2::text` 只进入了请求 parameters，没有进入 ApplyBindings 的绑定循环。
3. 图片无法显示：ComfyUI 图片已经下载并保存到本地，但 history 输出被分类为 `kind=file`。`/outputs/0` 对 `kind != image` 返回 JSON envelope 而不是图片字节，浏览器拿到 JSON 自然无法作为 `<img>` 展示。样本中的 `mime=image/png` 与 `kind=file` 也说明分类过程对 ComfyUI output ref 的字段形态不够稳健。

## 1. 取消暴露字段

### 代码链路

`internal/entry/web/static/app.js` 的 `renderNodeEditor` 同时写入：

- `#node-field-editor`（右侧 Inspector）
- `#modal-field-editor`（节点 Modal）

两处都会生成 `.node-field-row` 和 `[data-field-expose]`。`openNodePopup` 打开 Modal 后，用户点击右侧 Inspector 的复选框时，Modal 中的副本并不会同步。

`saveNodeFields` 当前逻辑：

```js
const source = modalHost?.querySelector('.node-field-row') ? modalHost : inspectorHost;
const added = readFieldEditor(source, nodeEditorID);
```

只要 Modal 存在字段行，就永远从 Modal 读取。因此 Inspector 取消勾选不会进入 `readFieldEditor`，原字段被保留。反向操作也可能产生同样的覆盖问题。

### 持久化证据

`test/output/novel/meta/comfyui/workflows/wf-1786682749190521200.canvas.json` 中仍有：

```json
"fields": [{"id":"2::text", "node_id":"2", "input":"text", "exposed":true}]
```

节点 `node-2` 也仍保留 `exposed_field_ids: ["2::text"]`。这与“取消后仍显示/仍可配置”一致。

### 修复边界

应只保留一个字段编辑 DOM，或保存时按当前可见/激活的编辑器读取；如果必须保留两份，则打开时同步，输入时双向同步，保存后再用统一模型重新渲染，不能通过“Modal 存在即优先”选择数据源。

## 2. 字段值和 prompt 没有绑定到节点

### 请求侧

`testJob` 会把动态控件值放入：

```js
parameters: values,
field_values: values
```

当前样本 `img_1786684349301399200.json` 的确记录了：

```json
"parameters": {"2::text":"1girl,standing"}
```

因此浏览器读取控件并提交请求并非主要问题。

### 后端运行链路

`internal/entry/web/v2.go:testJob`：

1. `LoadWorkflow(wfID)` 读取 `<workflow>.json`。
2. `LoadOrCreateWorkflowCanvas` 读取画布字段。
3. `CanvasValues` 合并 `field_values`/prompt/defaults。
4. `comfyui.ApplyBindings(wf, values)` 将逻辑字段写入 API workflow。

关键缺口是第 4 步之前没有将 `canvas.Fields` 转换为 `wf.Bindings`。`ApplyBindings` 只使用：

```go
bindings := w.Bindings
if w.Config != nil && len(bindings) == 0 {
    bindings = w.Config.Bindings
}
```

而当前持久化样本显示：

- `wf-...config.json`: `fields: [], bindings: [], defaults: {}`
- `wf-...json.config`: `fields: [], bindings: [], defaults: {}`
- `wf-...canvas.json`: `fields` 有 `2::text`

所以 `values["2::text"]` 会被接收并记录到 job，但绑定循环没有任何 binding，ComfyUI 节点 `2.inputs.text` 保持原默认 prompt。`NormalizeWorkflowConfig` 只在 `Workflow.Config` 非空时同步旧字段，不会读取 CanvasDocument。

### 修复边界

运行前必须从 CanvasDocument 构造运行时 config（或把 canvas fields 映射成临时 `wf.Bindings`/`wf.Defaults`），映射规则为：

```text
CanvasField.ID       -> Binding.Key
CanvasField.NodeID   -> Binding.NodeID
"inputs."+Input     -> Binding.Path
ValueType            -> Binding.Type
Default              -> Workflow.Defaults[ID]
```

只映射 `Exposed == true` 的字段；取消暴露后不能生成 binding。该运行时合并不应覆盖原始 API JSON，也不必强制把画布字段回写旧 config 文件，除非另有兼容迁移策略。

## 3. 输出被分类为 file，图片响应变成 JSON

### 观测到的状态

用户返回的 API 数据：

```json
{
  "kind":"file",
  "node_id":"7",
  "output_key":"images",
  "class_type":"<nil>",
  "mime":"image/png",
  "previewable":false,
  "url":"/api/v2/comfyui/jobs/img_1786684349301399200/outputs/0"
}
```

本地文件实际存在：

`test/output/novel/meta/images/tests/img_1786684349301399200.png`（约 988 KB）。因此 ComfyUI `/view` 下载和本地保存已经成功。

### 分类和路由链路

`classifyOutputs` 遍历 ComfyUI `/history/{prompt_id}` 的 output map，构造 `MediaOutput`。当前实现：

- 用 `fmt.Sprint(m["class_type"])`，字段不存在时得到字符串 `<nil>`。
- 只接受 `[]any`，对其他 JSON 数组形态/嵌套结构不做兼容。
- 先以 `filename + mime + class_type` 调用 `outputKind`，再在必要时从扩展名补 MIME。

理论上标准 `filename: *.png` 或 `mime: image/png` 应分类为 image；样本却出现 `mime=image/png`、`kind=file`，说明实际 history payload 在分类时没有按预期提供可识别的 filename/mime（或者 payload 在前序层被包装/转换）。分类函数没有记录原始 output ref，无法从现有 web.log 还原具体字段。

`internal/entry/web/v2.go:jobOutput` 随后按 `MediaOutput.Kind` 分支：

```go
if o.Kind != "image" {
    envelope(w, 200, 0, o, "")
    return
}
```

因此 `kind=file` 时 `/outputs/0` 返回统一 JSON，而不是 PNG。前端即使识别到 `mime=image/png`，`<img src=".../outputs/0">` 仍会收到 JSON，显示失败。

### 修复边界

后端分类应使用统一的 `isImageOutput`：优先检查 MIME（`image/*`），其次检查 filename 扩展名，再检查 ComfyUI output key/class type；同时将 `<nil>` 归一为空字符串。分类结果应保证 `mime=image/png` 时 `Kind=image`、`Previewable=true`。

路由还应对已保存的 `job.Output` 做兜底：当 `job.Output.mime` 为 `image/*` 或文件扩展名是图片，即使旧 job 的 `Outputs[idx].Kind` 是 `file`，也直接发送保存的图片字节。这样可以兼容已经落盘的历史任务，不要求用户重新运行。

前端 `renderOutputs` 可继续保留 `mime.startsWith('image/')` 判断，但不能解决后端返回 JSON 的问题；应先修复响应 Content-Type/字节。

## 建议的验证用例

1. 节点字段：暴露 `2::text` -> 保存 -> 取消勾选 -> 保存；Canvas fields、node exposed IDs、运行动态控件和 bindings 都应变为 0。
2. 绑定：提交 `field_values: {"2::text":"new prompt"}`，断言提交给 ComfyUI `/prompt` 的 JSON 中 `2.inputs.text == "new prompt"`，且原始 workflow 文件未被修改。
3. 输出：用 history output `{ "7": { "images": [{"filename":"x.png","subfolder":"","type":"output"}] } }` 和 `{ "mime":"image/png" }` 两种 fixture，均断言 `kind=image`, `previewable=true`，GET `/outputs/0` 返回 `Content-Type: image/png` 和 PNG 字节，而不是 envelope JSON。
4. 兼容旧任务：加载当前 `img_1786684349301399200`，GET `/outputs/0` 仍能从 `job.Output` 找到本地 PNG 并正常返回。

