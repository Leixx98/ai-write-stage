# Novel-ComfyUI Bridge 静态 QA 报告

## 范围

审查小说 unit、Prompter、JSON Parser、ComfyUI job 和 Web 工作台之间的桥接改动。按照当前 `AGENTS.md`，本轮只做源码与路由静态检查，不执行编译、测试、启动或浏览器验证。

## 已确认问题

1. **自动生成尚未接入（高）**
   `BridgeConfig.AutoGenerate` 只被前端和存储读写；`write_chapter_unit`/`SaveWritingUnit` 完成路径没有调用 unit 图片生成服务。当前只能手工调用 `POST /api/v2/units/{chapter}/{ordinal}/image/generate`。

2. **严格模式 API 默认值不一致（中）**
   `POST /api/v2/comfyui/prompter/parse` 将缺省的 `strict` 解码为 `false`，不会读取已保存的 `bridge.strict`。未显式传参的调用可能绕过配置中的严格校验。

3. **关闭桥接仍要求完整工作流（中）**
   `PUT /api/v2/comfyui/bridge` 无论 `enabled` 是否为 false，都要求 `workflow_id` 指向启用工作流并且存在 Prompter 字段。用户无法先关闭/清空桥接配置。

4. **工作流图片生成路由缺失（中）**
   契约中若使用 `/api/v2/comfyui/workflows/{id}/image/generate`，当前 dispatch 没有该路由；实现路径是 unit 生成路由和 workflow `/run` 手工测试路由。

5. **取消存在状态覆盖竞态（中）**
   cancel handler 先将 job 保存为 `cancelled`，后台 Prompter/ComfyUI goroutine 可能随后继续并保存 `completed`/`failed`。各阶段需要检查 context，并在持久化前做状态一致性保护。

6. **输出 MIME/扩展名假设为 PNG（低）**
   下载结果统一写为 `.png`，`jobs/{id}/image` 和 unit image 端点也固定返回 `image/png`。工作流返回 JPEG/WebP 等格式时会产生 MIME 与实际字节不一致。

## 已检查且通过静态审查的部分

- `ParseAndValidate` 覆盖 JSON fence、重复 key、尾随 token、类型、enum、范围及 strict/non-strict 分支。
- Prompter retry 分支已处理 `regenerate_prompt=true`；普通 retry 复用持久化 `PromptValues`。
- `CanvasField.ID` 被用于 Prompt Schema、PromptValues 和 ComfyUI binding，手工测试与 unit 执行共用 `mergeCanvasRuntime`/`ApplyBindings`/`runJob`。
- API JSON 响应使用 `{code,data,msg}`；图片二进制输出端点为浏览器展示所需的非 envelope 响应。

## 建议的验证用例

- 写作 unit 保存后，在 `auto_generate=true` 与 false 两种配置下确认是否创建图片 job。
- omit `strict` 的 Parser 请求，分别验证 bridge strict=true/false 的实际行为。
- 关闭桥接、删除 workflow、切换 workflow 时验证配置保存行为。
- 取消 prompting、queued、running、downloading 各阶段的任务，确认最终状态不会被后台 goroutine 覆盖。
- 用 PNG/JPEG/WebP 三种输出验证响应头、文件扩展名和浏览器预览。

