# ComfyUI Canvas 问题分析

本文针对以下两个现象给出代码级定位和修复建议：

1. 在节点编辑器勾选字段并应用后，左侧 Workflow 仍显示 `0 exposed fields`。
2. ComfyUI job 可以完成，但生成图片无法稳定显示在网页上。

## 1. 字段计数与字段配置

### 现状链路

- 画布版代码从 `internal/entry/web/static/app.js:246` 开始，覆盖前面的兼容版实现。
- 左侧计数在 `renderWorkflowList()`（约第 264 行）计算：

  ```js
  w.field_count ?? canvasFields(w).length
  ```

- `canvasFields()` 只读取 `workflow.config.fields`、`workflow.schema` 或顶层 `fields`。
- 节点弹窗的保存在 `saveNodeFields()`（约第 275 行）：先修改内存中的
  `selectedWorkflow.config.fields`，再调用 `saveWorkflowDefinition(true)`，最后
  `loadWorkflows()`。
- `/api/v2/comfyui/workflows` 的后端 `listWorkflows()`（`v2.go:504`）直接返回
  `store.ListWorkflows()` 的完整 `Workflow` 对象，没有显式的 `field_count`。

### 高概率根因

当前 `app.js` 同时保留了旧版和画布版函数定义（例如 `loadWorkflows`、
`saveWorkflowDefinition`、`renderDynamicFields`、`testJob`、`renderJob`）。虽然画布版
定义在文件末尾会覆盖函数名，但旧版事件监听器仍在第 217-240 行注册了一批同名动作。
这会造成以下可观察的不一致：

- 左侧列表首次渲染使用的是 `/workflows` 返回的旧对象；此对象的 `config` 可能为
  `null` 或字段配置尚未合并，因而显示 0。
- `saveNodeFields()` 只在异步保存完成后间接触发列表刷新，且
  `loadWorkflows()` 刷新时先 `renderWorkflowList()`、再异步 `selectWorkflow()`；
  列表计数没有使用刚刚保存的 `selectedWorkflow`，短时间内会继续显示旧值。
- `selectWorkflow()` 从 `/canvas` 返回时仅在 `canvas.fields.length > 0` 时覆盖
  `selectedWorkflow.config.fields`。字段为空的旧 canvas 会保留另一份 config，导致
  画布、工作流 JSON、列表计数三份状态可能不同。
- `persistCanvas()` 把字段写入 `.canvas.json`，而 `saveWorkflowDefinition()` 把字段写入
  `.config.json`；两次 PUT 的顺序依赖异步调用，旧客户端事件可能再次保存空字段。

### 复现/确认步骤

在浏览器 DevTools 中：

1. 点击节点，勾选一个 primitive input，点击 `Apply fields`。
2. 检查 `PUT /api/v2/comfyui/workflows/{id}` 的请求体：`config.fields` 应有一项。
3. 检查随后 `PUT /api/v2/comfyui/workflows/{id}/canvas`：`fields` 应有同一项。
4. 检查 `GET /api/v2/comfyui/workflows` 和 `GET /api/v2/comfyui/workflows/{id}`。
   如果 GET 单项有字段而列表对象没有，问题在列表 DTO/计数计算；如果两者都没有，
   问题在保存链路或旧事件监听器覆盖。

### 建议修复

1. **合并 app.js 的重复实现**：保留一套画布版函数和事件绑定，删除前面旧版
   `loadWorkflows`、`saveWorkflowDefinition`、`renderDynamicFields`、`testJob`、
   `renderJob` 等定义及对应重复监听器。这是避免状态竞争的首要修复。
2. 在后端 `listWorkflows()` 生成明确的轻量 DTO，包含：

   ```json
   {"id":"...", "name":"...", "field_count": 1, "config": {"fields": [...]}}
   ```

   `field_count` 应由持久化 `WorkflowConfig.Fields` 计算，而不是依赖前端推断。
3. `renderWorkflowList()` 对当前工作流优先读取 `selectedWorkflow`：

   ```js
   const source = selectedWorkflow?.id === w.id ? selectedWorkflow : w;
   const count = source.config?.fields?.length || 0;
   ```

   保存成功后立即更新 `workflows` 中对应对象并调用 `renderWorkflowList()`，不要等
   `loadWorkflows()` 选择第一个工作流后再间接刷新。
4. 保存字段时一次请求完成配置和 canvas 持久化，或至少让 canvas PUT 成功后再更新
   UI；失败时必须显示错误，不要在 `persistCanvas()` 中静默吞掉异常。
5. 对 canvas 字段和 config 字段做统一转换函数，保证 `name`/`label`、`value_type`/`type`
   等别名不会造成计数或编辑器读取差异。

## 2. Job 输出图片

### 现状链路

- `runJob()`（`internal/entry/web/v2.go:950` 附近）等待 ComfyUI history，调用
  `classifyOutputs()`，然后用 `firstOutput()` 下载第一张图片到：

  `meta/images/tests/{job_id}.png`（测试 job）或 `drafts/{chapter}.units/{ordinal}.png`。

- `classifyOutputs()` 为每个可识别输出生成 URL：

  `/api/v2/comfyui/jobs/{job_id}/outputs/{index}`。

- `jobOutput()`（`v2.go:1249`）按 index 从 `j.Outputs` 取 MIME，再把本地 png 文件
  作为二进制返回。
- 前端 `renderOutputs()`（`app.js:284`）把 `o.url` 或上述 URL 放进 `<img src>`。
- 测试画布 `renderTestCards()`（`app.js:315-320`）只从
  `currentJob.outputs.find(...)` 或 `currentJob.output[0]` 取输出；但后端
  `currentJob.output` 是对象而不是数组，因此没有 `outputs` 时测试画布永远不会显示图。

### 已确认的脆弱点

1. **测试画布错误读取 output**：`currentJob.output?.[0]` 对后端对象结构无效。
   即使 job 已完成且 `output.mime` 是 `image/png`，测试画布 Output 卡仍显示
   `Run to preview image`。
2. **输出分类为空时前端降级为文本**：`renderOutputs()` 在 `outputs` 为空时回退到
   `output` 对象，但对象没有 `kind`，只有 `filename`/`mime`；只要 `mime` 因某种
   ComfyUI 响应缺失，就会被当作文本卡而不是图片。
3. **输出 URL 与本地文件是隐式耦合**：所有 output index 都映射到同一个本地 png，
   且 `jobOutput()` 在文件不存在时返回 envelope JSON 404。浏览器 `<img>` 对 JSON
   错误只显示破图，用户看不到后端原因。
4. **异步轮询没有图片加载错误反馈**：`renderOutputs()` 未给 `<img>` 添加
   `onerror`/重试逻辑，无法区分 job 尚未写完、URL 404、Content-Type 错误或缓存问题。

### 建议修复

1. 统一输出 DTO，后端在 job 完成时始终返回至少一项：

   ```json
   {"kind":"image", "mime":"image/png", "url":"/api/v2/comfyui/jobs/{id}/image"}
   ```

   `outputs/{index}` 可保留兼容，但主预览使用单一 `/image` 路由。
2. 前端测试画布从 `currentJob.output` 对象构造 URL：

   ```js
   const output = currentJob?.outputs?.find(isImage)
     || (currentJob?.output?.mime?.startsWith('image/') ? currentJob.output : null);
   const outputURL = output?.url || (jobID ? `/api/v2/comfyui/jobs/${jobID}/image` : '');
   ```

3. `renderOutputs()` 使用 `isImage = kind === 'image' || mime.startsWith('image/') ||
   filename` 扩展名判断；对 `<img>` 增加 `onerror`，显示“图片加载失败（打开 Job
   详情查看日志）”。
4. `runJob()` 保存真实 MIME 和本地路径/URL（可在 `ImageJob` 增加 `OutputPath` 或
   `PreviewURL`），避免路由猜测文件名和扩展名。
5. `jobOutput()`/`job image` 路由在文件尚未落盘时返回明确的 `Retry-After`，前端在
   图片 404 时短暂重试 2-3 次；job 完成后仍 404 则展示后端错误。
6. 不要在前端吞掉 `persistCanvas()` 和 `refreshJob()` 错误；至少写入通知区，便于
   判断“job 成功但图片不显示”是媒体路由问题还是 UI 状态问题。

## 3. 回归验证清单

- 导入 API JSON，保存后 `GET /workflows/{id}` 的 `config.fields` 和
  `GET /workflows/{id}/canvas` 的 `fields` 数量一致。
- 勾选字段后刷新页面，左侧显示正确数量，节点边框显示 exposed 状态，Run 面板
  出现对应控件。
- 运行测试 job：pending -> queued -> running -> completed；`GET /jobs/{id}/image`
  返回 200 且 `Content-Type: image/*`，普通 Run 面板和 Test canvas 都能显示。
- 模拟 output 404、ComfyUI 非图片响应和浏览器缓存，页面显示可理解的错误状态。

