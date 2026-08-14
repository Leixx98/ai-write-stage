# ComfyUI 字段取消、提示词预设与设置默认值分析

## 范围

本报告只分析现有实现，不修改业务代码。检查对象为 `internal/entry/web/static/app.js`、`index.html`、`internal/entry/web/v2.go`、`internal/comfyui/canvas.go`、`internal/comfyui/schema.go` 以及现有设置存储。

## 1. 取消字段的复现与状态链路

复现步骤：

1. 打开 ComfyUI 画布并选择一个已保存工作流。
2. 点击节点，打开字段 Modal，确认 `text` 已勾选。
3. 取消 `text` 的 checkbox，点击“应用字段”。
4. 再次打开同一节点或刷新页面，字段仍显示为已配置，左侧计数也没有下降。

现有链路：

```text
Modal checkbox
  -> readFieldEditor(#modal-field-editor)
  -> saveNodeFields() 更新 selectedWorkflow.config.fields
  -> saveWorkflowDefinition() PUT /workflows/{id}
  -> persistCanvas() PUT /workflows/{id}/canvas
  -> loadWorkflows/selectWorkflow()
  -> GET /workflows/{id}/canvas
  -> canvasFields() / renderNodeEditor()
```

关键代码位置：

- `renderNodeEditor` 只在 `#modal-field-editor` 渲染真实字段；`#node-field-editor` 被写成提示文本。因此 Modal 应是唯一读取源。
- `readFieldEditor` 通过 `querySelector('[data-field-expose]')?.checked` 过滤字段。取消勾选理论上会把该字段从返回数组删除。
- `saveNodeFields` 对当前节点使用 `keep.concat(added)`，并重新生成 `config.fields/bindings/defaults`。
- `persistCanvas` 从 `canvasFields()` 生成 `CanvasDocument.fields`，目前会无条件写 `exposed: true`。
- `selectWorkflow` 读取 canvas 时只判断 `canvas.fields.length`，没有过滤 `exposed:false` 字段。
- 后端 `workflow` PUT 在 `req.Canvas == nil` 时保留磁盘上旧 canvas；它不会从 `req.Config.Fields` 重建 canvas。
- 后端 `/canvas` PUT 的 `mergeCanvasPayload` 在 payload 带 `format` 且 `nodes` 为数组时完整反序列化，因此空 `fields: []` 可以清空持久化字段。

### 可能的根因

1. **旧 canvas 中存在 `exposed:false` 字段**：前端 `canvasFields` 和 `selectWorkflow` 没有按 `Exposed` 过滤，导致“取消配置”仍被计数和渲染。虽然当前 `persistCanvas` 总是写 `true`，但历史版本或手工配置可以留下该状态。
2. **工作流 PUT 与 canvas PUT 的双存储时序**：后端 workflow PUT 保留旧 canvas；若之后 canvas PUT 失败（当前 `persistCanvas` 捕获异常并静默忽略），刷新会恢复旧字段。应让 canvas PUT 错误可见，并在工作流保存请求中同时传 `canvas`，或者由后端根据请求 config 原子更新 canvas。
3. **前端存在重复的 `saveWorkflowDefinition` 声明**（约 299 行和文件尾部再次声明）。后一个函数会覆盖前一个，容易导致维护时误判保存顺序。应合并为单一实现。
4. **双按钮事件源**：Inspector 和 Modal 都有 `data-action="save-node-fields"`。当前读取固定 Modal，隐藏按钮虽不应触发，但应只保留一个保存入口或在事件处理器中校验 `event.currentTarget`，避免状态源再次分裂。

### 建议修复验收

- `readFieldEditor` 返回空数组时，必须向后端发送 `fields: []`，并且后端 canvas JSON 的 `fields` 也必须为空。
- `canvasFields`、`workflowFieldCount`、`renderNodeEditor`、`renderDynamicFields` 均只处理 `exposed !== false` 的字段（兼容旧数据时缺省视为 true）。
- `/canvas` PUT 失败不得静默；页面应提示“字段保存失败”，不得显示成功 toast。
- 保存后立即 GET `/canvas`，断言该字段不存在；刷新浏览器后再次断言。
- 增加一个只有 `exposed:false` 字段的旧 canvas 回归用例，计数应为 0、左侧运行面板不渲染该字段。

## 2. 写作提示词现状

页面入口为 `index.html` 的 `#prompts`，仅渲染五个 textarea（architect、chapter_planner、writer、editor、prompter）。

当前前端：

- `loadPrompts()` GET `/api/v2/settings/prompts`，读取 `data.prompts || {}`；没有数据时所有 textarea 为空。
- `savePrompts()` 将 textarea 收集为 `{prompts:{...}}` 后 PUT。
- `showView('prompts')` 每次切换都会调用 `loadPrompts()`。

当前后端：

- `settingsDocument("prompts")` 只是读取/写入 `meta/web/prompts.json` 的任意 map。
- 文件不存在时返回 `{}`，没有内置默认提示词，也没有版本、活动配置或预设列表。
- 内置提示词实际存在于 `assets/prompts/*.md`，由 `assets.Load().Prompts` 加载；当前 `Prompts` 结构没有 Prompter 字段，仓库也没有 `assets/prompts/prompter.md`。

### 建议提示词预设模型

保持现有 envelope，GET 返回：

```json
{
  "active_preset": "默认配置",
  "presets": {
    "默认配置": {
      "architect": "...assets 默认内容...",
      "chapter_planner": "...",
      "writer": "...",
      "editor": "...",
      "prompter": "..."
    }
  },
  "prompts": { "architect": "...当前活动配置..." },
  "version": 1
}
```

PUT 接受 `active_preset`、`name`、`prompts` 和可选 `delete` 操作。保存新组合时复制当前 prompts 到新名称，不覆盖“默认配置”；切换组合只改变 `active_preset`。服务端首次 GET 应从 `assets.Load` 生成默认值并写入文件，避免前端硬编码大段模板。

默认提示词来源建议：architect_short/long 选择一个稳定默认（或分别展示两个角色）、chapter_planner、writer、editor 来自 `assets.Load`; Prompter 应新增内置模板并纳入 `assets.Prompts`，否则页面只能显示空值。

## 3. 设置页现状与默认值

`#app-settings` 当前包含导入书籍、仿写参考、从章节重新规划、写作要求四个字段。页面加载时没有 `loadWorkflowSettings()`，因此 GET 从未被调用；只有点击保存才 PUT `/api/v2/settings/workflow`。

后端仍是通用 JSON 文件：`meta/web/workflow.json`，不存在时返回 `{}`。没有字段说明、格式校验或写作要求预设。

建议 GET 返回带默认值和说明的结构：

```json
{
  "active_preset": "默认要求",
  "presets": {
    "默认要求": {
      "writing_rules": "遵守当前大纲和用户规则；保持人物、时间线和设定一致；只输出小说正文。"
    }
  },
  "import_source": "",
  "imitate_reference": "",
  "replan_from": 0,
  "writing_rules": "...默认要求...",
  "help": {
    "import_source": "支持 .txt、.md；导入后系统会先分段、分析并合并为本书资料。",
    "imitate_reference": "填写参考作品路径或文本，用于提取叙事风格，不会覆盖故事事实。",
    "replan_from": "填写大于等于 1 的章节号，将从该章重新生成后续规划。",
    "writing_rules": "填写长期写作偏好；会作为用户规则参与后续规划和写作。"
  }
}
```

写作要求默认文本可由 `internal/rules.SystemDefaults()` 转为可读说明，再叠加用户输入；不要把 `meta/user_rules.json` 当作 Web 表单唯一事实源，二者职责不同。

设置预设建议复用提示词预设的 `active_preset + presets` 结构，PUT 时对名称做非空和路径字符校验，删除活动预设时自动回退“默认要求”。

## 4. 接口与回归要求

- 所有 GET/PUT 继续使用 `{code,data,msg}` envelope。
- 保存字段、提示词预设、写作要求预设都应返回保存后的完整活动对象，前端直接用响应更新状态，避免再次读到旧副本。
- 任何持久化失败必须显示错误，不得 `catch (_) {}` 静默吞掉。
- 回归覆盖：字段取消后刷新、空字段数组、旧 `exposed:false` canvas、默认提示词首次加载、提示词新建/切换/重启、设置页首次加载和写作要求切换。
