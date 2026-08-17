# 提示词组合与写作要求预设方案

## 目标

本方案为 Web 工作台增加两类可命名预设：

1. **提示词组合**：一次保存 architect、chapter_planner、writer、editor、prompter 等角色的完整模板。顶部导航显示当前组合，可快速切换、另存为或覆盖保存。
2. **写作要求预设**：保存 `settings/workflow` 中的写作要求文本。设置页面显示当前预设，可另存为不同风格并随时切换。

ComfyUI 工作流中的 `class_type`、节点 ID 和输入名仍按原样显示；只有工作台固定文案和预设操作使用中文。

## 现状与兼容策略

当前 `GET/PUT /api/v2/settings/prompts` 和 `/api/v2/settings/workflow` 接收 `map[string]any`，文件分别是：

```text
meta/web/prompts.json
meta/web/workflow.json
```

服务端不应删除或重命名旧字段。新文档使用 `version: 2`，同时保留旧字段镜像：

- `prompts` 始终等于当前激活组合的角色模板；旧前端只提交 `{ "prompts": {...} }` 时，更新当前组合并继续成功。
- `writing_rules` 始终等于当前激活写作要求预设的 `text`；旧前端只提交 `writing_rules` 时，更新当前预设并继续成功。
- 读取 `version` 缺失或小于 2 的文件时，按旧结构包装成“默认配置”/“默认要求”，不改动用户原文本；首次新格式保存时再写入 `version: 2`。
- 未配置的默认角色模板从项目现有 assets 读取：`architect-short.md`（无短版时回退 `architect-long.md`）、`chapter-planner.md`、`writer.md`、`editor.md`。`prompter` 没有文件时使用内置图片提示词模板；模板中的 `{{VOICE}}` 保留，由运行时文风层替换。
- 默认写作要求由 `assets/voice.md` 与 `assets/styles/default.md` 合并生成，并附加“按章节计划完成当前 unit、保持人物/世界观连续、只输出正文”的基础要求。文件不可读时使用同一内容的内置短文本，不能返回空白预设。

## 提示词组合数据契约

### GET `/api/v2/settings/prompts`

成功响应继续使用统一 envelope：

```json
{
  "code": 0,
  "data": {
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
        },
        "updated_at": "2026-01-01T00:00:00Z"
      }
    },
    "prompts": {
      "architect": "...",
      "chapter_planner": "...",
      "writer": "...",
      "editor": "...",
      "prompter": "..."
    }
  },
  "msg": ""
}
```

`updated_at` 仅用于界面显示和冲突提示，不参与提示词内容。预设名去除首尾空白，不能为空，建议限制为 1-64 个 Unicode 字符；同名保存必须显式选择覆盖。

### PUT `/api/v2/settings/prompts`

新前端使用动作字段，服务端应返回保存后的完整文档（而不是只返回 `saved: true`），便于前端更新下拉框：

```json
{ "action": "activate", "name": "默认配置" }
```

```json
{ "action": "save", "name": "默认配置", "prompts": {"writer": "..."}, "overwrite": true }
```

```json
{ "action": "save_as", "name": "悬疑风格", "source": "默认配置", "prompts": {"writer": "..."} }
```

规则：

- `activate` 只切换 `active_preset`，不修改其他预设。
- `save` 更新指定名称；不存在时创建，已存在且 `overwrite` 不为 true 时返回 `code: 1003`。
- `save_as` 默认从 `source` 或当前激活组合复制所有角色，再用请求中的 `prompts` 覆盖；目标同名一律要求 `overwrite: true`。
- `prompts` 可只包含部分角色，服务端与源组合合并；未知角色保留，避免未来新增 role 时数据丢失。
- 兼容旧请求 `{ "prompts": {...} }`：视为对当前激活组合的 `save`，隐式 `overwrite: true`。
- 激活或保存后，响应 `data` 必须是完整的 GET 文档，保证顶部选择器与编辑器立即同步。

### 前端交互

- 顶部导航在“写作提示词”旁增加紧凑选择器：显示 `active_preset`，选项来自 `presets`；切换前若有未保存修改，弹窗确认“保存/放弃/取消”。
- 页面按钮：`保存`（覆盖当前）、`另存为`（输入新名称）、`恢复默认`（激活“默认配置”，不覆盖当前文本）。
- 加载组合后按角色渲染编辑器；角色名中文化，模板正文原样显示。
- 保存失败不改变 active 状态，并在页面内显示中文错误；网络重试不得重复创建另存预设。

## 写作要求预设数据契约

`settings/workflow` 保留导入/仿写/重新规划字段，在其上增加版本和预设集合：

```json
{
  "version": 2,
  "active_writing_rules_preset": "默认要求",
  "writing_rule_presets": {
    "默认要求": {
      "name": "默认要求",
      "text": "...",
      "updated_at": "2026-01-01T00:00:00Z"
    }
  },
  "writing_rules": "...",
  "import_source": "",
  "imitate_reference": "",
  "replan_from": 0
}
```

### GET `/api/v2/settings/workflow`

服务端返回上述完整结构。旧文件只有 `writing_rules` 时包装为“默认要求”；旧文件没有写作要求时创建默认要求文本。`import_source`、`imitate_reference` 和 `replan_from` 原样保留。

### PUT `/api/v2/settings/workflow`

预设操作沿用提示词组合语义：

```json
{ "action": "activate_writing_rules", "name": "简洁风格" }
```

```json
{ "action": "save_writing_rules", "name": "简洁风格", "text": "...", "overwrite": true }
```

```json
{ "action": "save_writing_rules_as", "name": "长篇叙事", "source": "默认要求", "text": "..." }
```

同一个 PUT 可以同时携带 `import_source`、`imitate_reference`、`replan_from`；这些普通设置的更新不能重置预设集合。为兼容旧页面，若请求包含 `writing_rules` 且没有 `action`，视为覆盖当前激活预设并更新镜像字段。

### 设置页交互与说明文案

每个输入项下方增加 `<small class="field-help">`，不参与提交：

- 导入书籍：支持 `.txt`、`.md` 或项目导出的 JSON；UTF-8 编码，导入后会解析基础设定和章节内容。
- 仿写参考：填写参考文件或目录路径；只用于提取风格与结构，不会覆盖当前正文。
- 重新规划：填写起始章节号（大于等于 1）；系统只重规划尚未完成的章节。
- 写作要求：自然语言描述长期偏好，建议包含题材、叙事视角、节奏、禁用内容和篇幅；保存后会作为用户规则参与规划和写作。

写作要求区域旁显示预设选择器、`保存`、`另存为`、`恢复默认`。切换预设只改变编辑器文本，点击保存工作流后才提交普通设置；预设另存/激活调用对应 action 并立即刷新当前文本。

## 业务使用约束

- Host 当前仍从 `internal/rules`/用户规则快照读取实际写作要求；Web 预设只是配置来源。保存写作要求后，下一次开始或显式刷新规则时才生效，不应在 HTTP handler 中直接启动 Agent。
- 提示词组合保存不自动重启运行中的 Agent；运行中任务继续使用启动时快照。
- API key、模型密钥引用和本机绝对路径不得写入或回显预设内容的日志。
- 所有错误遵循 `{code,data,msg}`；名称冲突使用 `code: 1003`，名称非法使用 `1001`，文件损坏使用 `2001`。

## 验收清单

- 无配置首次打开：出现“默认配置”和“默认要求”，角色模板与默认写作要求非空。
- 旧版 `prompts.json`/`workflow.json` 可读取，旧 PUT 请求仍成功，保存后保留旧镜像字段。
- 新建、切换、覆盖、另存提示词组合；刷新页面后 active 状态和文本一致。
- 新建、切换、覆盖、另存写作要求预设；导入/仿写/重新规划字段不丢失。
- 未保存编辑切换时出现确认；取消操作不会改变当前预设。
- API 返回始终是 `{code,data,msg}`，冲突和非法输入有可读中文提示。
