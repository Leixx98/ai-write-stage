# 小说 Unit 到 ComfyUI 图片桥接指南

本功能把已经落盘的小说 writing unit 转换为 ComfyUI 图片任务：Writer 完成 unit 后，`Prompter` 根据当前 unit 和工作流字段 Schema 返回 JSON，后端校验并绑定 JSON 字段，然后提交 ComfyUI。浏览器不直接访问模型或 ComfyUI。

## 一次配置

1. 启动 ComfyUI 和 ainovel Web 工作台，先在 **ComfyUI** 页面保存地址并完成连接测试。
2. 导入 ComfyUI 导出的 **API workflow JSON**。API 格式的顶层必须是节点 ID 到 `{ "class_type": ..., "inputs": ... }` 的对象；带有 `nodes`/`links` 的 UI workflow 需要先在 ComfyUI 中导出 API 格式。
3. 在画布中点选节点，勾选需要由图片提示词生成器填写的输入字段。为每个字段设置稳定的 **字段键**，例如 `positive_prompt`、`negative_prompt`；字段键必须在工作流内唯一，并以字母或下划线开头。
4. 将字段来源设为 **Prompter**，保存字段并保存工作流。固定值（如采样器、尺寸）应设为 **Default**；只在手工测试时填写的值可设为 **Runtime**。
5. 打开右侧 **桥接** 检查器，选择工作流，开启 **启用桥接**。需要每个 unit 完成后自动出图时，再开启 **单元完成后自动生成**。
6. 选择严格模式并保存。严格模式会要求 Prompter 返回所有必填字段；失败时图片任务会停在校验阶段，页面会显示需要检查的环境或字段原因。

保存后可以在 **提示词 JSON Schema** 区域查看实际传给 Prompter 的字段名、类型、枚举和范围。Schema 是由当前画布字段生成的，不需要手工复制 ComfyUI 节点路径。

## Prompter 返回格式

Prompter 必须返回一个 JSON object，字段键必须与 Schema 完全一致。例如工作流暴露了两个字段时：

```json
{
  "positive_prompt": "a cinematic night scene, detailed lighting",
  "negative_prompt": "blurry, low quality, watermark"
}
```

不要返回 Markdown 代码围栏、解释文字或完整 ComfyUI workflow。后端 JSON Parser 会依次检查：JSON 是否为单一 object、字段键是否存在、值类型、枚举以及最小/最大值。解析失败时不会提交 ComfyUI。

在 **JSON 解析诊断** 区域粘贴一份 Prompter 输出并点击“检查并解析”，可在实际运行前确认字段映射。非严格模式会忽略未知键，并允许有默认值的字段缺省；类型错误、非法枚举和没有默认值的必填字段仍然会失败。

## 自动生成与手工生成

- **自动生成**：Writer 成功提交 unit 后创建一个图片任务。任务会保存 unit、工作流、Schema hash、Prompter 原始输出和已解析的 `prompt_values`，因此普通重试不会再次调用模型。
- **重新生成提示词**：在图片任务操作区点击“重新生成提示词”，或重试请求中设置 `regenerate_prompt=true`，才会重新调用 Prompter。
- **手工生成**：在桥接面板填写章节号和 unit 序号，点击“生成单元图片”。这与自动任务共用相同的 Schema 校验、字段绑定和 ComfyUI 执行器，不会改变小说推进状态。
- **取消**：取消会停止本地任务，并尽力调用 ComfyUI 的 `/interrupt`。远端中断失败时任务仍会保存为 `cancelled`。
- **重试**：失败或超时后点击“重试”。原始错误记录保留，新的 attempt 使用已保存的提示词值和工作流快照；只有明确重新生成提示词时才消耗新的模型调用。

任务阶段通常为 `prompting -> validating -> binding -> submitting -> queued -> running -> downloading -> completed`。失败、超时和取消会显示对应阶段及可恢复性。

## 严格模式和推进行为

严格模式目前作用于 JSON Parser 和图片任务提交：

- Prompter 调用失败、超时、JSON 不合法、字段校验失败、绑定失败或 ComfyUI 任务失败时，当前 unit 图片任务失败，后续 unit/章节不会继续推进。
- 非严格模式下图片任务异步执行，写作可以继续；失败仍会持久化并在 Web 页面显示，不能视为图片已生成。
- 运行中的任务使用启动时的桥接、提示词和模型快照。修改配置或提示词组合后，重启 ainovel 进程才会用于新的写作任务；已运行任务不会热替换。

## 常见问题

| 现象 | 检查项 |
| --- | --- |
| Schema 显示 0 个字段 | 节点字段是否已勾选为 Exposed、来源是否为 Prompter、字段键是否已保存工作流；确认没有重复或非法字段键。 |
| Parser 提示缺字段 | Prompter 返回的键必须与 Schema 完全一致；固定输入改为 Default 并填写默认值。 |
| 图片任务没有提交 | 先看任务阶段和错误 code；Parser 或绑定失败不会触发 ComfyUI。 |
| ComfyUI 无法连接 | 检查地址 scheme、主机和端口，确认 ComfyUI 正在监听且防火墙/代理未拦截。 |
| 任务超时 | 检查 ComfyUI 控制台、模型文件和显存，再调整 ComfyUI timeout；Prompter timeout 与 ComfyUI timeout 是两个独立设置。 |
| 输出无法预览 | 确认 workflow 有可选的 image output，检查任务 Outputs；浏览器应访问 ainovel 的 `/api/v2/comfyui/jobs/{job_id}/outputs/{index}`，不要直接访问 ComfyUI。 |

相关接口契约见 [api_contracts.md](api_contracts.md) 第 12 节，画布操作见 [comfyui-canvas-user-guide.md](comfyui-canvas-user-guide.md)。
