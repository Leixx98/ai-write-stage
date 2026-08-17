# ComfyUI 画布工作台使用指南

本指南描述当前 Web 工作台的推荐使用方式。ComfyUI 页面以画布为主：左侧选择工作流，中间查看和拖动画布节点，右侧暴露可编辑字段、运行测试并预览输出。旧的长 JSON、bindings 和 Job test 表单仍保留在后端作为兼容接口；日常使用不需要直接编辑这些内部结构。

如果要把小说 unit 自动转换为图片，请继续阅读 [小说 Unit 到 ComfyUI 图片桥接指南](novel-comfy-bridge-user-guide.md)。画布字段来源设为 `Prompter` 后，桥接层会自动生成 JSON、校验并提交图片任务；本页其余章节主要介绍工作流导入和手工测试。

## 1. 前置条件

运行前请准备：

- 已启动 ComfyUI，并确认浏览器或命令行可以访问其地址（默认 `http://127.0.0.1:8188`）。
- 工作流依赖的 checkpoint、LoRA、VAE、ControlNet 等模型已经安装。
- 从 ComfyUI 导出 **API workflow JSON**。它应是“节点 ID -> `{class_type, inputs}`”的对象，而不是包含 `nodes`、`links`、`extra` 的编辑器 UI workflow。
- ainovel Web 进程使用的输出目录可写。图片测试结果默认写入 `meta/images/tests/`；带章节和 unit 的任务写入 `drafts/<chapter>.units/<ordinal>.png`。

严格模式目前作用于 Prompter JSON 校验和图片任务提交；它还不是 Engine 写作循环的硬门禁。遇到错误时先检查 ComfyUI 服务、模型文件、工作流节点和显存，再重试。接口细节见 [api_contracts.md](api_contracts.md)。

## 2. 启动 Web 工作台

在仓库根目录执行：

```powershell
go run ./cmd/ainovel-cli --web --listen 127.0.0.1:8080
```

也可以先生成可执行文件再启动：

```powershell
go build -o ainovel-web-cli.exe ./cmd/ainovel-cli
.\ainovel-web-cli.exe --web --listen 127.0.0.1:8080
```

打开 `http://127.0.0.1:8080`。若端口被占用，把 `--listen` 改为其他本机地址，例如 `127.0.0.1:8081`。`--web` 不能与 `--headless`、`--prompt` 或 `--prompt-file` 同时使用；创作需求从页面输入框提交。Web 进程会在终端显示访问地址，运行日志写入当前输出目录的 `logs/web.log`。

旧版 `/api/state`、`/api/events`、`/api/stream` 仍兼容现有工作台；新页面优先使用 `/api/v2/*`。JSON 响应为 `{ "code": 0, "data": {}, "msg": "" }`。图片等媒体响应返回真实 `image/*` 内容，并附 `X-API-Code: 0`。

## 3. 最小 ComfyUI 设置

进入顶部 **ComfyUI** 页面，展开底部的 **Advanced connection settings**（高级连接设置）。首次只需要确认：

| 设置 | 推荐值 | 说明 |
| --- | --- | --- |
| Base URL | `http://127.0.0.1:8188` | ComfyUI 服务地址，只允许 `http`/`https`，不包含用户信息或非法端口 |
| Timeout | `600000` | 单个任务的总超时，单位毫秒 |
| Poll interval | `1000` | 查询 ComfyUI 历史状态的间隔，单位毫秒 |
| Client ID | `ainovel-web` | 提交到 ComfyUI 的客户端标识 |
| Enabled | 打开 | 允许提交任务 |
| Strict mode | 按需打开 | 打开后 Prompter 必须返回全部必填字段；失败停在校验阶段。它还不是 Engine 推进门 |

点击 **Save**，再点击 **Test connection**。地址格式错误会返回 `code: 2001`；服务不可达通常返回 `code: 3001`。错误提示不会回显密钥、完整 prompt 或本机绝对路径。

如果使用多个 ComfyUI 实例，可通过后端实例接口维护实例，并在左栏选择默认实例。画布运行会优先使用请求指定的实例，其次使用工作流实例、默认实例或队列最短的可用实例。

## 4. 导入工作流

1. 在 ComfyUI 页面左栏点击 **Import**。
2. 选择从 ComfyUI 导出的 API JSON 文件。
3. 工作流出现在左侧列表，中间画布会自动生成节点和连线。
4. 点击 **Save workflow** 持久化工作流。

导入请求使用 `POST /api/v2/comfyui/workflows/import`。工作流定义按项目共享，写在 `.ainovel/comfyui/workflows/`；画布与 config 同行：

```text
<project>/.ainovel/comfyui/workflows/<id>.api.json
<project>/.ainovel/comfyui/workflows/<id>.config.json
<project>/.ainovel/comfyui/workflows/<id>.canvas.json
```

如果选择的是 UI workflow，页面会提示先导出 API 格式，不会把 `nodes`/`links` JSON 静默保存成可运行模板。导入校验失败时检查返回的 `data.errors`，常见原因是节点缺少 `class_type`/`inputs` 或绑定引用了不存在的节点。

## 5. 在画布中配置字段

### Workflow 模式

中间工具栏默认处于 **Workflow** 模式：

- 鼠标滚轮缩放；拖动画布空白区域平移。
- 拖动节点改变位置；点击节点打开右侧 Node inspector（窄屏时打开节点弹窗）。
- 使用 `−`、`+`、**Fit** 调整视口，**Fullscreen** 可将画布扩展到全屏。
- 画布容器有固定高度，节点内容不会撑大整个页面。

在节点字段编辑器中，对需要在运行时修改的输入执行以下操作：

1. 勾选字段的暴露开关。
2. 填写显示名称。
3. 选择控件类型：`text`、`textarea`、`number`、`slider`、`dropdown` 或 `boolean`；图像/视频/音频输入使用媒体控件。
4. 填写默认值；数值控件可设置 `min`、`max`、`step`。
5. 点击 **Apply fields**，再点击页面顶部 **Save workflow**。

暴露字段会同步到右侧 Run 面板和左栏参数摘要。服务端运行前会再次检查类型、范围和枚举，不要依赖浏览器直接修改原始 workflow。

### Test canvas 模式

切换顶部 **Test canvas** 后，完整 API 拓扑隐藏，画布显示 Prompt、媒体输入、ComfyUI 和 Output 测试卡。已暴露字段直接显示为可编辑控件；媒体字段先通过上传接口保存引用，再参与运行。测试卡的位置与视口也会保存到 `.canvas.json`。

## 6. 运行、取消、重试和预览

1. 在右侧 Inspector 点击 **Run**，或在 Test canvas 的运行卡提交测试。
2. 状态会从 `pending`、`submitting`、`queued`、`running` 进入 `completed`，失败时为 `failed`、`timeout` 或 `cancelled`。
3. 运行中点击 **Cancel**。后端先取消本地 context，再尽力调用 ComfyUI `/interrupt`；远端中断失败也不会让本地任务继续占用。
4. 失败后点击 **Retry**。重试会增加 attempt 并重新提交 ComfyUI prompt，不会覆盖原始错误记录。
5. 成功后 Output 区显示图片预览。点击图片可查看大图；其他视频、音频或文件输出仍保存在 job metadata 中。

推荐使用的端点如下：

```text
POST /api/v2/comfyui/workflows/{id}/run
GET  /api/v2/comfyui/jobs/{job_id}
POST /api/v2/comfyui/jobs/{job_id}/cancel
POST /api/v2/comfyui/jobs/{job_id}/retry
GET  /api/v2/comfyui/jobs/{job_id}/outputs
GET  /api/v2/comfyui/jobs/{job_id}/outputs/{index}
```

运行请求的核心字段为：

```json
{
  "mode": "test",
  "instance_id": "local-8188",
  "field_values": {"6::text": "a quiet mountain village"},
  "mini_test_values": {},
  "client_id": "ainovel-web"
}
```

旧客户端也可以使用 `POST /api/v2/comfyui/jobs/test`；它与 workflow `/run` 共享同一任务状态机。所有 JSON 响应均为：

```json
{"code": 0, "data": {}, "msg": ""}
```

图片 bytes 是受控媒体响应，不是 JSON；响应带有 `X-API-Code: 0`。浏览器不直接访问 ComfyUI。

## 7. 保存、导出和兼容接口

画布视口、节点位置、字段投影和测试卡通过以下接口保存：

```text
GET /api/v2/comfyui/workflows/{id}/canvas
PUT /api/v2/comfyui/workflows/{id}/canvas
```

工作流详情 `GET /api/v2/comfyui/workflows/{id}` 同时返回 `workflow`、`config` 和 `canvas`。导出时使用：

```text
GET /api/v2/comfyui/workflows/{id}/export?format=api
GET /api/v2/comfyui/workflows/{id}/export?format=config
```

`format=api` 是可以重新导入 ComfyUI 的原始 API JSON；`format=config` 是字段控件配置。CanvasDocument 只能作为 ainovel 的编辑投影，不能直接提交给 ComfyUI `/prompt`。

以下接口继续保留给高级设置、迁移工具和旧客户端：

```text
GET/PUT /api/v2/comfyui/config
GET/PUT /api/v2/comfyui/instances
POST    /api/v2/comfyui/instances/{id}/test
POST    /api/v2/comfyui/media/upload
GET     /api/v2/comfyui/media/{id}
```

旧的无 `/v2` 路由（如果部署中仍存在）只用于兼容，不建议新页面调用，也不会删除已有工作流数据。

## 8. 端到端验收清单

准备可用 ComfyUI 和最小 API workflow 后，可按下面顺序验收：

```powershell
# 1. 编译
go build -o ainovel-web-cli.exe ./cmd/ainovel-cli

# 2. 单元测试（记录当前基线失败）
go test ./...

# 3. 启动 Web
.\ainovel-web-cli.exe --web --listen 127.0.0.1:8080
```

浏览器验收：

1. 打开页面，进入 ComfyUI，保存地址并通过连接测试。
2. 导入 `qa-fixtures/minimal-api.json` 或真实 API workflow。
3. 确认节点和连线出现；点击节点，暴露一个文本或数值字段并保存。
4. 刷新页面，确认节点位置、视口和字段仍存在。
5. 切换 Test canvas，修改字段并 Run；确认 job 进入终态并显示图片。
6. 对运行中的 job 分别验证 Cancel 和 Retry；对不可达地址验证严格错误提示。
7. 在桌面和窄屏视口检查画布固定高度、缩放/平移和输出预览不重叠。

当前 `go test ./...` 可能仍包含与 ComfyUI 画布无关的基线失败（例如 `assets` 标题、`internal/agents`、`internal/agents/ctxpack` 和 `internal/host`）。这些失败应单独记录，不应把它们误判为画布回归；发布前仍需由集成环境重新执行并跟踪处置。

## 9. 常见错误排查

| 现象 | 优先检查 |
| --- | --- |
| `code: 2001` | Base URL 的 scheme、主机、端口；不要填写 `file:` 或带用户信息的 URL |
| `code: 3001` | ComfyUI 是否启动、端口是否监听、防火墙/代理是否拦截 |
| `code: 3002` | 是否导入 API JSON；节点是否有 `class_type` 和 `inputs`；字段绑定是否指向真实节点 |
| `failed` 且有节点名 | ComfyUI 控制台错误、模型文件、节点依赖和显存 |
| `timeout` | 减少分辨率/steps，确认 ComfyUI 队列，适当增大 Timeout |
| `cancelled` 后仍出图 | 本地任务已取消；检查 ComfyUI 是否支持 `/interrupt`，不要重复提交相同 job |
| 没有图片输出 | 检查 workflow 的 output selector；确认末端节点返回 `images`，并重新 Validate |
