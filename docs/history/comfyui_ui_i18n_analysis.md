# ComfyUI 工作台中文化分析

## 范围与原则

本报告只分析前端可见文本，不修改实现。目标是把产品自有 UI、状态和错误提示统一为简体中文；ComfyUI API workflow 中的 `class_type`、节点标题、输入字段名和字段值属于用户导入的数据，保持原样显示。

建议在 `app.js` 增加集中式 `UI_TEXT`/`uiText(key, vars)` 映射，所有模板和 `notify()` 从映射取文案。不要对 workflow 数据调用翻译。后端返回的错误 `msg` 也应在前端按错误码映射，未知错误保留原文并加中文上下文。

## `index.html` 静态文案映射

| 当前文案 | 建议中文 |
| --- | --- |
| `ainovel Web Workbench` | `ainovel 网页工作台` |
| `Model not configured` | `模型未配置` |
| `Workbench` | `工作台` |
| `API` | `模型 API` |
| `Settings` | `设置` |
| `Writing prompts` | `写作提示词` |
| `ComfyUI` | `ComfyUI`（产品名保留） |
| `READY` | `就绪` |
| `Status` | `状态` |
| `Events` | `事件` |
| `Streaming output` | `流式输出` |
| `Details` | `详情` |
| `Unit image preview` | `单元图片预览` |
| `Current unit image`（alt） | `当前单元图片` |
| `Enter a request or steering instruction` | `输入请求或调整指令` |
| `Send` / `Pause` / `Stop` | `发送` / `暂停` / `停止` |
| `API configuration` | `API 配置` |
| `Model and provider configuration is managed by the project config.` | `模型和服务商配置由项目配置文件管理。` |
| `Provider` / `Model` / `API key reference` | `服务商` / `模型` / `API 密钥引用` |
| `Environment variable or config reference` | `环境变量或配置引用` |
| `Role overrides are read from the project config file.` | `角色模型覆盖配置来自项目配置文件。` |
| `Book import, imitation, replanning and writing rules.` | `导入书籍、仿写、重新规划和写作要求。` |
| `Import book` / `Imitate reference` / `Replan from chapter` / `Writing rules` | `导入书籍` / `仿写参考` / `从章节重新规划` / `写作要求` |
| `Save` | `保存` |
| `Prompt templates for each role, including Prompter.` | `配置各角色的提示词模板，包括图片提示词角色。` |
| `ComfyUI Canvas` | `ComfyUI 画布` |
| `Import a ComfyUI API workflow, expose only the fields you need, then test it on the canvas.` | `导入 ComfyUI API 工作流，在画布中选择需要配置的字段，然后直接测试。` |
| `Test connection` / `Save workflow` | `测试连接` / `保存工作流` |
| `Workflows` / `Import` / `New workflow` | `工作流` / `导入` / `新建工作流` |
| `Search workflows` | `搜索工作流` |
| `No workflows loaded` | `暂无工作流` |
| `Connection` / `Unknown` | `连接状态` / `未知` |
| `Workflow` / `Test canvas` | `工作流` / `测试画布` |
| `Zoom out` / `Zoom in` / `Fit` / `Fullscreen` | `缩小` / `放大` / `适配` / `全屏` |
| `Import or select a workflow to begin` | `请导入或选择工作流开始` |
| `Validate workflow` / `Export JSON` | `校验工作流` / `导出 JSON` |
| `Node` / `Run` | `节点` / `运行` |
| `Click a node on the canvas to configure exposed fields.` | `点击画布中的节点配置可编辑字段。` |
| `Close` / `Apply fields` | `关闭` / `应用字段` |
| `Run test` / `Idle` / `Run` / `Cancel` / `Retry` | `运行测试` / `空闲` / `运行` / `取消` / `重试` |
| `Expose fields from nodes to edit them here.` | `请先在节点中勾选字段，字段会显示在这里。` |
| `Results appear here` | `结果将在这里显示` |
| `Advanced connection settings` | `高级连接设置` |
| `Base URL` / `Timeout` / `Poll interval` / `Client ID` | `服务地址` / `超时时间` / `轮询间隔` / `客户端 ID` |
| `Max response bytes` / `Default workflow ID` | `最大响应字节数` / `默认工作流 ID` |
| `Enabled` / `Strict mode` | `启用` / `严格模式` |

`aria-label`、按钮 `title` 和图片 `alt` 也属于可见/可访问文本，应同步中文化，例如 `Workbench navigation` -> `工作台导航`、`Import API JSON` -> `导入 API JSON`、`Workflow graph` -> `工作流图`。

## `app.js` 运行时文案映射

### 工作台状态与详情

- `Model not configured` -> `模型未配置`；`READY` -> `就绪`。
- 状态字段 `Runtime`, `Phase`, `Flow`, `Chapter`, `Completed`, `Words`, `Context`, `Cost` -> `运行状态`, `阶段`, `流程`, `章节`, `完成进度`, `字数`, `上下文`, `费用`。
- `Chapter` -> `第 N 章`；`Untitled` -> `未命名`。
- 详情标题 `Work`, `Agents`, `Outline`, `Premise`, `Characters` -> `作品`, `智能体`, `大纲`, `故事前提`, `角色`；空值 `None` -> `暂无`。
- 事件默认分类 `EVENT` -> `事件`；错误分类 `ERROR` -> `错误`。

### 通用错误、设置和连接

建议采用以下模板（冒号和动态错误信息保留）：

| 当前英文 | 建议中文 |
| --- | --- |
| `State refresh failed: ...` | `状态刷新失败：...` |
| `Model config failed: ...` | `模型配置加载失败：...` |
| `Settings saved` / `Settings save failed: ...` | `设置已保存` / `设置保存失败：...` |
| `Prompts saved` / `Prompt load failed: ...` / `Prompt save failed: ...` | `提示词已保存` / `提示词加载失败：...` / `提示词保存失败：...` |
| `ComfyUI config failed: ...` | `ComfyUI 配置加载失败：...` |
| `ComfyUI config saved` / `Connection settings saved` | `ComfyUI 配置已保存` / `连接设置已保存` |
| `Save failed: ...` | `保存失败：...` |
| `Connection failed` / `Connection succeeded, configuration not saved` | `连接失败` / `连接成功，配置尚未保存` |
| `phase` / `retryable` | `阶段` / `可重试` |
| `No instances loaded; the legacy Base URL is still available below.` | `暂无实例，仍可使用下方的兼容服务地址。` |
| `Instance list unavailable: ...` / `Instance save failed: ...` | `实例列表不可用：...` / `实例保存失败：...` |
| `Instance health refreshed` / `Health refresh failed: ...` | `实例健康状态已刷新` / `健康状态刷新失败：...` |
| `Workflow load failed: ...` / `Workflow save failed: ...` | `工作流加载失败：...` / `工作流保存失败：...` |
| `Workflow saved` / `Workflow imported...` | `工作流已保存` / `工作流已导入，请点击节点选择字段后保存。` |
| `JSON import failed: ...` | `JSON 导入失败：...` |
| `Import a ComfyUI API JSON first` | `请先导入 ComfyUI API JSON` |
| `Save the workflow before testing` | `请先保存工作流再测试` |
| `Job submit failed: ...` / `${action} failed: ...` | `任务提交失败：...` / `任务${action}失败：...`（action 先映射为取消/重试） |
| `No active job` | `没有正在运行的任务` |
| `Run a job to preview outputs` / `Output preview failed: ...` | `请先运行任务再预览输出` / `输出预览失败：...` |
| `Choose an input media file first` / `Input media uploaded` / `Media upload failed: ...` | `请先选择输入媒体文件` / `输入媒体已上传` / `媒体上传失败：...` |

### 画布、字段和输出

- 节点提示 `click to expose inputs` -> `点击选择可配置输入`；`N exposed` -> `已选择 N 个字段`。
- `No nodes`、`No inputs`、`No dynamic fields inferred`、`No defaults configured` -> `暂无节点`、`暂无输入`、`未推断出可配置字段`、`暂无默认值`。
- 编辑器占位符 `Display name`, `Default value`, `Min`, `Max`, `Step` -> `显示名称`, `默认值`, `最小值`, `最大值`, `步长`。
- 控件类型 `text`, `textarea`, `number`, `slider`, `dropdown`, `boolean`, `image` 是配置值，建议显示中文标签但 `value` 保持英文：`文本`, `多行文本`, `数字`, `滑块`, `下拉框`, `开关`, `图片`。
- `Choose fields exposed in the Run inspector` -> `选择要在运行面板中编辑的字段`；`Node fields updated` -> `节点字段已更新`。
- `No outputs yet`、`Results appear here` -> `暂无输出`、`结果将在这里显示`；输出类型 `image` / `file` 仅作为元数据，可显示为 `图片` / `文件`，原始 MIME 和文件名保持原样。
- 测试卡片 `Prompt`, `Describe the image...`, `Writes to positive_prompt`, `No workflow selected`, `Run test`, `Output`, `Latest output`, `Run to preview image` -> `提示词`, `描述要生成的图片……`, `将写入 positive_prompt`, `未选择工作流`, `运行测试`, `输出`, `最新输出`, `运行后预览图片`。

## 必须保留英文的内容

1. ComfyUI 节点 `class_type`、节点 ID 和导入 JSON 的输入字段名（例如 `text`, `seed`, `steps`）。
2. 工作流名称和字段自定义名称：用户输入什么就显示什么。
3. API 路径、JSON key、控件 `value`（如 `textarea`、`number`）和任务状态值（后端协议字段），仅在视觉标签处翻译。
4. 模型名、服务商名、文件名、MIME 类型和错误详情中的第三方原文。

## 发现的结构性问题

`app.js` 约第 1-245 行是旧版表单客户端，约第 246-336 行又追加了画布覆盖实现。`workflowAPI`、`loadWorkflows`、`selectWorkflow`、`testJob`、`renderJob`、`renderOutputs`、`newWorkflow`、`importWorkflow`、`renderDynamicFields` 等函数被重复定义。虽然末尾定义通常会覆盖前面的函数，但旧代码已经注册事件监听器并在脚本加载时执行 `refresh()/replay()/connect()`，导致：

- 同一按钮可能绑定两套处理器，提示词和输出区域可能被重复渲染。
- 旧版字段 schema 与画布 `config.fields` 的结构不同，容易造成左侧 `exposed fields` 数量显示为 0。
- 两个 `renderOutputs` 使用不同的 URL、alt 和空态文案，输出图片失败时难以判断实际调用了哪一套。
- 中文化应先收敛为单一实现，再建立映射，否则翻译一处仍会被另一套覆盖。

## 推荐落地顺序

1. 删除/隔离旧版表单渲染和重复函数，仅保留画布实现及通用工作台状态代码。
2. 在 `app.js` 顶部定义 `UI_TEXT` 和 `formatError()`，将所有静态模板、状态、空态、错误、任务状态通过映射生成。
3. 为 `workflow-list` 的字段数统一从 `config.fields` 计算，并在保存成功后用服务器返回值刷新，避免本地字段和列表字段数脱节。
4. 输出图片统一使用后端返回的 `url/preview_url`；没有 URL 时调用 `/api/v2/comfyui/jobs/{job}/outputs/{index}`，并在 `<img>` 增加加载失败提示和调试信息。
5. 使用浏览器验收：切换所有顶部页面、导入 workflow、点击节点、勾选字段、保存、运行测试、查看图片和错误提示。

