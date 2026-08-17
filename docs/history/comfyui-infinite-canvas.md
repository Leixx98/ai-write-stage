# ComfyUI 工作台：Infinite Canvas 参考实现

本项目参考 `Infinite-Canvas-main` 的后端代理和灵活参数面板，把 ComfyUI 做成 Web 工作台中的独立页面。浏览器只访问 ainovel 的 Go API；ComfyUI 请求、文件写入、任务状态和输出预览仍由后端负责。Infinite Canvas 的画布文档不是本项目当前的输入格式，也不能直接提交到 ComfyUI `/prompt`。

## 当前页面能力

ComfyUI 页面按三部分组织：

1. **Instances**：维护多个 ComfyUI 地址，显示健康状态、队列长度和选择策略。
2. **Workflows**：导入 API workflow，浏览节点和动态字段，编辑 JSON、bindings、defaults 及输出选择器。
3. **Job test**：提交一次测试任务，查看状态、日志、多媒体输出，并预览图片结果。

页面支持输入媒体上传、节点输入浏览、从 workflow 推断字段，以及对推断结果进行手动覆盖。动态控件包括文本、长文本、数字、滑块、布尔值、下拉选项和图片/视频/音频文件等；最终类型转换和路径校验在后端完成。

## 多实例

实例和选择策略保存在 `meta/comfyui/instances.json`：

```json
{
  "instances": [
    {
      "id": "local-8188",
      "name": "Local ComfyUI",
      "base_url": "http://127.0.0.1:8188",
      "enabled": true,
      "priority": 100,
      "max_concurrency": 1,
      "selection_weight": 1,
      "health": "unknown"
    }
  ],
  "settings": {
    "default_instance_id": "local-8188",
    "strategy": "least_queue",
    "fallback_instance_ids": [],
    "health_ttl_ms": 10000,
    "queue_probe": true,
    "sticky_unit": true
  }
}
```

选择优先级为显式任务实例、workflow 绑定实例、项目默认实例，再按策略选择。当前实现会查询 ComfyUI `/queue`，`least_queue` 按可观测队列和优先级选择；任务开始后会记录 `instance_id`，不会在执行中漂移。地址必须是无 userinfo、query 和 fragment 的 `http`/`https` URL。

接口：

```text
GET  /api/v2/comfyui/instances
PUT  /api/v2/comfyui/instances
POST /api/v2/comfyui/instances/{id}/test
```

## API workflow 与 config 双文件

项目把 ComfyUI 原始 API workflow 与工作台配置分开管理：

```text
meta/comfyui/workflows/<id>.api.json       # ComfyUI /prompt 接受的 node map
meta/comfyui/workflows/<id>.config.json    # fields、bindings、defaults、outputs、instance_id
```

为了兼容旧版本，服务端还会维护 `<id>.json` 聚合文档；读取时可以从聚合文档或双文件恢复，保存时更新对应文件。API workflow 的根对象必须是：

```json
{
  "3": {"class_type": "KSampler", "inputs": {}},
  "6": {"class_type": "CLIPTextEncode", "inputs": {"text": ""}}
}
```

UI workflow 或 Infinite Canvas canvas JSON（例如含 `nodes`、`links`、`connections`、`resources`）不会被静默转换，导入时返回 `code: 3002` 并提示先导出 ComfyUI API JSON。

接口：

```text
POST /api/v2/comfyui/workflows/import
GET  /api/v2/comfyui/workflows
GET  /api/v2/comfyui/workflows/{id}
PUT  /api/v2/comfyui/workflows/{id}
DELETE /api/v2/comfyui/workflows/{id}
GET  /api/v2/comfyui/workflows/{id}/export?format=api|config|canvas
```

`format=canvas` 仅在未来存在画布文档时可用；当前页面只编辑 API workflow 和 config。

## 节点浏览、动态字段与 bindings

工作台可以选择节点查看 `class_type` 和 inputs。后端根据节点类型和输入名推断候选字段，例如：

| 输入 | 默认控件 | 典型逻辑 key |
| --- | --- | --- |
| `CLIPTextEncode.inputs.text` | textarea | `positive_prompt` / `negative_prompt` |
| `KSampler.seed/steps/cfg` | number | `seed` / `steps` / `cfg` |
| `EmptyLatent.width/height` | number | `width` / `height` |
| sampler/scheduler choices | dropdown | `sampler` / `scheduler` |
| LoadImage 等输入 | image | `input_image` |

推断结果只是候选，页面显示 `source: inferred`，用户可编辑 label、control、value type、默认值、范围、选项和 required。多个候选不会按节点 ID 静默选取；应由用户确认 bindings。

一个 binding 示例：

```json
{
  "key": "positive_prompt",
  "node_id": "6",
  "path": "inputs.text",
  "control": "textarea",
  "type": "string",
  "required": true
}
```

运行任务时后端深拷贝 API workflow，再应用本次参数和 defaults，不修改模板文件。支持的路径和类型仍受后端 validator 限制；非法节点、字段、数组索引或类型转换会返回统一错误 envelope。

相关接口：

```text
GET /api/v2/comfyui/workflows/{id}/schema
GET /api/v2/comfyui/workflows/{id}/config
PUT /api/v2/comfyui/workflows/{id}/config
POST /api/v2/comfyui/workflows/{id}/validate
```

## 输入媒体与输出预览

本地输入媒体先上传到 ainovel：

```text
POST /api/v2/comfyui/media/upload
GET  /api/v2/comfyui/media/{id}
```

文件保存到 `assets/input/<sha256>.<ext>`，metadata 保存到 `meta/comfyui/media/<sha256>.json`。任务参数只携带受控的 `media_ref`/`storage_key`，不允许浏览器提交任意本地路径。

ComfyUI 输出会按 `image`、`video`、`audio`、`text`、`file` 分类，保存节点 ID、输出 key、class type、MIME 和预览能力。任务接口：

```text
GET /api/v2/comfyui/jobs/{job_id}/outputs
GET /api/v2/comfyui/jobs/{job_id}/outputs/{index}
```

图片输出通过受控媒体 URL 预览；非图片输出返回 metadata 或文本摘要。unit 主图仍落盘到 `drafts/<chapter>.units/<ordinal>.png`，同一 job 的其他输出只保存在 job metadata/测试页面中。

## Job 与兼容路由

当前页面优先使用：

```text
POST /api/v2/comfyui/workflows/{id}/run
POST /api/v2/comfyui/jobs/test
GET  /api/v2/comfyui/jobs/{job_id}
POST /api/v2/comfyui/jobs/{job_id}/cancel
POST /api/v2/comfyui/jobs/{job_id}/retry
```

旧版接口仍保留：

```text
GET /api/units/{chapter}/{ordinal}
GET /api/v2/units/{chapter}/{ordinal}/image
GET /api/v2/units/{chapter}/{ordinal}/image-job
```

新 JSON 接口统一返回 `{ "code": 0, "data": {}, "msg": "" }`；图片等媒体响应返回真实 `image/*` 内容，并附 `X-API-Code: 0`。旧 `/api/state`、`/api/events` 和 `/api/stream` 继续为现有工作台提供兼容能力。

## 明确未完成边界

以下项目仍未完成，不应根据当前页面推断为已接入：

1. `write_chapter_unit` 尚未自动创建 Prompter 图片任务，当前 Job test 是手动验证入口。
2. 远端 ComfyUI 实例的输入媒体同步尚未完成。现在上传文件保存于本地，任务会记录引用，但不会自动调用每个远端实例的 `/upload/image` 并改写远端文件名；涉及远端 LoadImage 节点时需使用可访问的共享路径或等待后续同步实现。
3. ComfyUI job 事件尚未接入 Host SSE 广播。页面通过提交后刷新/轮询获取 job 状态，不能依赖 `comfyui.job.*` SSE 事件恢复任务。
4. `strict` 配置已保存并在任务策略中保留，但尚未成为 `Host` 的 unit/章节推进门禁；图片失败不会自动阻止现有写作流程。
5. 当前没有 Infinite Canvas 的可编辑无限画布、节点连线和 canvas JSON 持久化页面；`format=canvas` 只是迁移接口预留。

在这些边界完成前，建议把 ComfyUI 页面用于实例健康检查、workflow 配置和手动 Job test；接入小说自动写作前，应先补齐 Prompter、媒体同步、SSE 和 strict gate 的集成测试。
