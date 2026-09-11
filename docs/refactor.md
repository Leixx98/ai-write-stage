# 重构方向

本文是目前唯一的设计文档。旧稿在 [history/](history/)，只供追溯，不约束实现。

八步重构已完成并经实测。后续改功能按下面三条边界落位，不再平行铺开大拆。

## 目标

降低后续 AI 改功能时的交叉污染，同时不回退已经能跑的小说引擎。不重写 Engine / Route / Arbiter。不把酒馆或 ComfyUI 塞进 `flow.Route`。

## 三条边界

以后新代码按这三条落位，违反就拒绝合入：

1. 小说事实只经 `internal/tools` 写 Store。
2. 图像作业只经独立的 `imagejob` 服务；web handler 不直调模型或 ComfyUI。
3. 酒馆会话只经 `internal/galgame` + `GalgameStore`。Host 只装配，不实现业务。

## 当前落位

```text
entry (Web/headless)
  ├─ 写作生命周期     → Host.Start/Resume/Steer/Abort/Snapshot
  ├─ 导入 / 仿写      → Host 独占入口（胶水在 host/import.go、host/simulate.go）
  ├─ 导出             → host/exp.Run
  ├─ 酒馆对话         → internal/galgame.Reply
  ├─ 酒馆存档         → store.GalgameStore
  └─ 出图             → imagejob.Service（StartTest / StartUnit / StartGalgame）

store.Open → Roots.Facts / Roots.Media / Roots.Tavern
Web HTTP   → /api/v2/* ；SSE → /api/v2/events、/api/v2/stream
```

酒馆新图：`tavern/sessions/{session_id}/images/{job_id}.png`。角色/会话文件名：`{角色名}_{时间}` / `{角色名}_{会话名}_{时间}`。

## 已完成的阶段

1. **抽配图执行链** — job 状态机落到 `internal/imagejob/service`；unit 图与酒馆图同一执行链、入口隔离。
2. **拆 Web 文件** — 后端 `api.go` / `settings.go` / `comfyui.go` / `jobs.go` / `units.go`；前端按顶栏拆 JS。
3. **收 Store / Host 边界** — `store.Open` 三个组合根；Host 只留生命周期、独占槽、模型和写作规则。
4. **收 API 面** — 删除无调用方的 legacy `/api/*`。

## 明确不做

- 不重写小说引擎，不引入 Coordinator / 通用工作流 DSL / 微服务。
- 不合并小说 cast 与酒馆角色卡。
- 不先上前端构建链。
- 不在本文件之外再堆分析稿。
