# ai-write-stage

本地优先的 AI 创作引擎：一句话开书，确定性引擎推进长篇；同一套出图链路覆盖小说插图、酒馆对话和剧场分镜。Go 实现，数据落在本地文件，浏览器里完成开书、干预和续写。

Go module：`github.com/Leixx98/ai-write-stage`

> 派生自 [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli)。上游面向高质量长篇，保留完整评审与重写智能体；本仓库把重心改成 **生文 × 生图**，裁掉主循环里的 Editor 评审，章节按小节推进（一节对应一张插画），并补上酒馆与剧场。

## 能做什么

**小说** — Architect 定设定，Chapter Planner 拆成场景卡和 writing unit，Writer 逐小节写。规划可用云端模型，正文可用本地模型。崩溃后打开同一工作区即可续跑。

**生图** — 小说小节、酒馆对话、剧场 beat 共用 `imagejob` → ComfyUI。作业状态机可在工作台里看：`prompting → validating → binding → submitting → queued → running → completed`。

**酒馆** — 导入 SillyTavern / Tavern 角色卡，多会话角色扮演。

**剧场** — 视觉小说式演出。开局生成 3～6 个压力点路线图；规划只填当前站；选项写入事实账本；收束由引擎判定。档位可选精简（适合小模型）或丰满（适合大模型）。

## 快速开始

需要 Go 1.25+。

```bash
git clone https://github.com/Leixx98/ai-write-stage.git
cd ai-write-stage
go build -o ai-write-stage ./cmd/ai-write-stage
./ai-write-stage
```

首次运行进入模型配置页，保存后进工作台。默认监听 `127.0.0.1:8080`，被占用则顺延。

```bash
./ai-write-stage                           # Web 工作台
./ai-write-stage --listen 0.0.0.0:8080     # 指定地址
./ai-write-stage --workspace 边城           # 打开已有工作区
./ai-write-stage --headless --workspace 边城 --prompt "写一本东方玄幻长篇"
./ai-write-stage eval --help
```

`--web` 仅为旧脚本兼容，与无参数启动相同。`--headless` 不跑首次配置，必须先在 Web 里配好模型，并指定 `--workspace`（或沿用上次打开的工作区）。

### Docker

```bash
mkdir -p config workspace
docker compose build
docker compose up
# 浏览器打开 http://localhost:8080
docker compose run --rm ainovel --headless --workspace 悬疑短篇 --prompt "写一本悬疑短篇"
```

开发时可用 `AINOVEL_ROOT` 指定软件根目录（`go run` 会回退到当前目录）。配置目录仍是 `~/.ainovel`，与旧工作区兼容。

## 特性

### 生文 × 生图

- **一节一图** — Planner 把每章拆成场景卡和有序 writing unit；小节粒度即插画粒度。小说生图开启且自动生成时，每个小节走 Prompter → Schema 校验 → 字段绑定 → ComfyUI，全部小节图片落盘后章节才算完成
- **出图可控** — 严格 / 非严格、取消、重试、提示词重生成、预览
- **ComfyUI 画布** — 导入 API workflow，在节点上暴露字段，Test canvas 试跑；暴露字段就是 LLM 与工作流的契约
- **角色外观连续** — Prompter 吃小节计划、正文和前文结尾，要求人物、服饰、时代、地点一致
- **三处同一条链** — 小说、对话、剧场共用 `imagejob`

### 小说引擎

- **确定性引擎 + 异构模型** — Engine 按事实表调度 Architect / Chapter Planner / Writer
- **Arbiter 可审计** — 选规划师、干预分诊、失败出路各一次 LLM 调用，裁定落盘
- **Step 级断点** — 工具成功即写 checkpoint，再次打开同一工作区从该步续跑
- **卷弧滚动规划** — 先规划 2 卷骨架 + 第 1 弧详章，后面写到再展开
- **自适应上下文** — 全量 / 滑窗 / 分层摘要，四级压缩，中文按 rune 估 token
- **底栏干预** — 继续 / 暂停、自由指令、重规划未写章节、重写要求、完结后续写
- **多模型** — OpenRouter / Anthropic / Gemini / OpenAI / DeepSeek / Qwen / GLM / Grok / Ollama / Bedrock / 自定义代理，角色可分开配

### 酒馆与剧场

- **角色卡** — Tavern / SillyTavern V1 / V2 `data` 信封 / 原生 JSON；`{{char}}` / `{{user}}` 宏；滑动窗口历史
- **剧场路线图** — 开局一次生成 3～6 个压力点（问题，不是剧情答案），选完不清
- **事实账本** — 选项带短 id 的 `set_facts`，选后写入 `facts.json`；规划必须尊重已发生事实
- **引擎收束** — 站走完或选项 `ending=true` 由引擎结束，不再让模型自己宣布完结
- **精简 / 丰满** — 精简：3～5 拍、近窗 4 屏、写手记忆 6 轮，空 `cg` 补 `keep`；丰满：5～10 拍、近窗 8 屏、记忆 20 轮。人在设置里选，不按本地 / 云端自动猜
- **布局随生图开关** — 对话看对话开关，剧场看剧场开关；未开启时藏图、文本居中

## 架构

**事实层确定，语义层自主。** 可枚举的状态迁移由 Engine + Route 执行；边界判断交给 Arbiter；正文和出图提示词是有界的单次 LLM 调用。

```
┌─────────────────────────────────────────────────┐
│              Host / Engine（确定性）              │
│  读 Store → Route → 直接运行 Worker → 循环        │
│  启动裁定 / 干预分诊 / 失败僵局 → 按需咨询 Arbiter  │
└───┬────────┬──────────┬────────────────────────┘
    │        │          │
┌───▼──────┐ ┌▼──────────┐ ┌▼──────┐   ┌──────────┐
│Architect │ │Chapter    │ │Writer │   │ Arbiter  │
│(短/长篇)  │ │Planner    │ │(逐unit)│   │(LLM函数) │
└───┬──────┘ └─────┬─────┘ └───┬───┘   └──────────┘
    └──────────────┴───────────┘
              │ 工具调用（IO + checkpoint）
┌─────────────▼───────────────────────────────────┐
│                     Store                        │
└─────────────┬───────────────────────────────────┘
              │ unit 完成（桥开启时）
┌─────────────▼───────────────────────────────────┐
│  Prompter → Schema / 字段绑定 → imagejob → ComfyUI │
└─────────────────────────────────────────────────┘
```

剧场另有一条短链：`Spine`（路线图）→ 当前站 `Architect` → `Planner` 分镜 → `Writer` 逐拍；选择写入账本后只规划下一站。

| 角色 | 职责 | 工具 |
|---|---|---|
| **Architect** | 前提、大纲、角色、世界规则；弧/卷边界展开 | `novel_context` `save_foundation` `revise_outline` `audit_foundation` |
| **Chapter Planner** | 当前章拆成场景卡和 unit；**小节数即插画数** | `novel_context` `read_chapter` `plan_chapter` |
| **Writer** | 按 unit 写正文，只提交 `content` | `novel_context` `write_chapter_unit` |
| **Arbiter** | 启动选规划师、干预分诊、失败出路 | 无（单次结构化裁定） |
| **Prompter** | 小节 / 对话 / 剧场 beat 的出图 JSON | 无（Schema 校验） |
| **Play Spine / Architect / Planner / Writer** | 剧场路线图、当前站、分镜、台词 | 结构化 JSON，无小说工具 |

上游 Editor 七维评审已离开主循环（`save_review` 等工具仍在仓库里，不再自动派发）。默认策略是文本够用，把算力留给出图和演出。

### 写作与出图

```
需求 → Arbiter 选规划师 → Architect 设定
     → Planner 拆章（小节 = 一张图）
     → Writer 逐小节 → Engine 提交整章
     →（生图开启）Prompter → ComfyUI → 图片落盘 → 下一章
```

状态分两层：Phase 只前进（`init → premise → outline → writing → complete`）；Flow 在写作期内切换（`writing / reviewing / rewriting / polishing / steering`）。长篇用指南针 + 骨架弧滚动展开，上下文按卷/弧/章分层摘要，超窗后从低到高压缩。

## Web 工作台

顶栏：小说、酒馆、API、设置、小说写作提示词、生图设置。欢迎页列出 `workspaces/`，可新建或打开；独占作业（写作 / 导入 / 剧场）进行中不能切工作区。

小说页三栏：运行状态、事件与流式输出、作品详情与大纲。底栏：

| 按钮 | 作用 |
|---|---|
| 继续 / 暂停 | 恢复或打断 |
| 其他指令 | 未开书当需求，写作中当干预，暂停时当继续说明 |
| 重新规划 | 从指定章起改**未写**大纲 |
| 重写 | 记下要求，不自动回改已写正文 |
| 完结后续写 | 完结后重开 |
| 导出 | TXT / EPUB（只读已完成章；EPUB 带插图） |
| 沉浸式阅读 | 已提交章节 |

酒馆页同一舞台切「对话」和「剧场」。剧场设置里选精简 / 丰满。

- **API** — 共享模型库、本书默认模型与角色分配
- **设置** — 导入已有小说、写作要求
- **小说写作提示词** — 保存或切换后需重启才用于后续写作
- **生图设置** — 场景策略、方案、ComfyUI 画布

## 工作区与配置

每本书是 `{软件根}/workspaces/<名字>/`。上次打开的工作区记在 `{软件根}/.ainovel-last-workspace`。全局模型库在 `~/.ainovel/models.json`；该书选择在 `workspaces/<名字>/.ainovel/`。

1. `~/.ainovel/models.json` — 全局模型库（含 API Key）
2. `~/.ainovel/rules/` — 全局写作规则（可选）
3. `workspaces/<名字>/.ainovel/config.json` — 该书模型 / 角色 / 风格
4. `workspaces/<名字>/.ainovel/rules/` — 该书规则（可选）

```jsonc
{
  "provider": "openrouter",
  "model": "google/gemini-2.5-flash",
  "reasoning_effort": "medium",
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-xxx",
      "base_url": "https://openrouter.ai/api/v1",
      "models": [
        { "name": "google/gemini-2.5-flash", "context_window": 200000 }
      ]
    }
  },
  "style": "default",
  "roles": {
    "architect": { "provider": "openrouter", "model": "google/gemini-2.5-pro" },
    "writer": { "provider": "ollama", "model": "qwen3:latest" },
    "prompter": { "provider": "openrouter", "model": "google/gemini-2.5-flash" },
    "galgame": { "provider": "openrouter", "model": "anthropic/claude-sonnet-4" }
  }
}
```

| 角色 | 说明 |
|---|---|
| `architect` | 小说规划；剧场路线图和当前站也用它 |
| `chapter_planner` | 章节规划与剧场分镜（未配则落到 architect） |
| `writer` | 小说正文与剧场台词 |
| `prompter` | 出图提示词 |
| `galgame` | 酒馆对话 |
| `import_*` | 导入管线（未配则落到 architect） |

Arbiter 用默认模型，不单独开角色。上下文窗口按「模型专属值 → 旧顶层 `context_window` → 注册表 → 200K」解析，只影响本地压缩时机。

自定义代理：

```jsonc
{
  "provider": "my-proxy",
  "model": "gpt-4o",
  "providers": {
    "my-proxy": { "type": "openai", "base_url": "https://proxy.example.com/v1" }
  }
}
```

支持 `openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` / `ollama` / `bedrock` 及任意自定义代理。更多字段见 `config.example.jsonc`。

## 生图

三个场景默认关闭，在「生图设置」里分别打开：

| 场景 | 触发 |
|---|---|
| 小说 | 可选「unit 完成后自动生成」；图片进度参与章节完成判定 |
| 对话 | 每轮 / 每 N 轮 / 仅手动 |
| 剧场 | 可选 CGNew 自动生成 |

方案绑定 Provider（当前主要是 ComfyUI）、提示词预设、workflow、实例。场景选默认方案，会话或剧场局可覆盖。

画布在「生图设置 → ComfyUI」：导入 **API workflow JSON**，暴露字段给 Prompter，Test canvas 试跑。

## 剧场怎么跑

1. 开局（或旧局第一次续写）生成 `spine.json`：3～6 个压力点
2. 引擎指定当前站，Architect 只填这一站，Planner 拆 3～5（精简）或 5～10（丰满）拍
3. 未收束站最后一拍必须是选项；选项带 `set_facts`，选后写入 `facts.json`
4. 已问过的题不能再出同一道；`ending=true` 或站走完则引擎收束
5. 旧局没有路线图时会补生成，但不会补出早该发生的事实

小模型请用精简档。丰满档需要模型能稳定填完整 JSON（含 `set_facts`、`cg`）。

## 导入 / 导出 / 规则

- **导入** — 设置页把已有小说编译进项目：切章 → 抽事实 → 归纳 → 发布 Foundation，可中断续跑
- **导出** — 小说页 TXT 或 EPUB，只读已完成章
- **诊断** — 结束或出错时写脱敏的 `meta/diag-export.md`
- **风格** — `default` / `suspense` / `fantasy` / `romance`，也可在 `style/styles/` 或 `~/.ainovel/style/styles/` 加文件
- **规则** — `~/.ainovel/rules/` 或书级 `rules/` 里用白话写偏好，系统归一化后写作时遵循

## 输出结构

```
workspaces/边城/
├── chapters/           # 终稿
├── summaries/          # 章摘要
├── drafts/             # 草稿与 unit
├── reviews/            # 上游遗留评审结构
├── timeline.jsonl
└── meta/
    ├── premise.md / outline.json / characters.json / world_rules.json
    ├── layered_outline.json / compass.json
    ├── progress.json / checkpoints.jsonl / decisions.jsonl
    ├── comfyui/workflows/
    ├── images/jobs/
    └── runtime/tasks/

galgame/
├── characters/
├── sessions/{id}/images/
└── plays/{id}/
    ├── meta.json / progress.json / outline.json
    ├── spine.json / facts.json
    ├── beats/ / writer_session.json
    └── runtime.log
```

再次打开同一工作区会读 `progress.json` + checkpoint 续跑。文件写入是 temp + fsync + rename。未完成的出图作业重启后可再触发。

## 设计要点

模型自由写内容和画面，顺序、幂等、阶段、JSON schema 由代码管。没有 task queue，一个串行循环 + 一张决策表 + 几个裁定函数。

## 技术栈

- Go 1.25
- Web 工作台（TUI 已移除）
- [agentcore](https://github.com/voocel/agentcore)
- [litellm](https://github.com/voocel/litellm)（vendor 在 `third-party/litellm`）
- ComfyUI HTTP API

## 致谢与许可证

派生自 [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli)（voocel）。上游设计文档在 `docs/history/`，当前方向见 [docs/refactor.md](docs/refactor.md)。

许可证见 [LICENSE](LICENSE)（Apache-2.0）。衍生作品请同时遵守上游许可证。
