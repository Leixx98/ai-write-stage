# ainovel-cli

生文 × 生图一体化创作引擎。确定性引擎自动推进小说文本，每个**小节（writing unit）**自动桥接 ComfyUI 生成插画：一句话需求进去，「正文 + 逐小节插图」出来，全程无需人工干预。

> **本项目基于开源项目 [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli) 二次开发**。上游项目的目标是创作高质量长篇小说，为此保留了完整的评审、重写智能体组；本 fork 把重心重新调整为**「生文 × 生图」**：对文本的文学质量要求不高，因此裁剪了部分角色（Editor 评审已退出主循环），并把章节计划重构为**小节驱动**——每个小节对应一张插画，把更多资源投入到 ComfyUI 出图桥接、酒馆生图与剧场模式上。

## 特性

### 生文 × 生图（本 fork 的重心）

- **小节化生成，一节一图** — Chapter Planner 把每章拆成场景卡和有序 writing units，Writer 逐小节书写；小节粒度即插画粒度。图片桥开启后，每个小节自动走「Prompter 出提示词 → Schema 校验 → 字段绑定 → 提交 ComfyUI」，全部小节图片落盘后章节才算完成
- **出图全链路可控** — 严格/非严格模式、取消、重试、提示词重生成、出图预览；作业状态机 `prompting → validating → binding → submitting → queued → running → completed` 全程可在 Web 工作台实时查看
- **ComfyUI 画布工作台** — 直接导入 API workflow，在节点上暴露可填字段，Test canvas 试跑并预览图片；暴露的字段就是 LLM 与工作流之间的契约
- **角色一致性提示词** — Prompter 接收小节计划 + 正文 + 前文结尾作为事实输入，系统提示词要求保持人物外观、服饰、时代背景、地点和世界观连续
- **三处出图同一条执行链** — 小说小节插图、酒馆对话生图、剧场 beat 配图共用同一个 `imagejob` 服务，job 状态机统一

### 小说引擎（精简流水线）

- **确定性引擎 + 异构模型流水线** — Engine 按事实决策表调度 Architect / Chapter Planner / Writer；复杂规划可用云端模型，批量正文可用本地模型
- **语义裁定可审计** — 选规划师、干预分诊、失败出路等判断由 Arbiter 单次调用完成，每次裁定落盘可回放；越简单越稳定，拒绝复杂编排
- **Step 级断点恢复** — 每个工具执行成功后写入 checkpoint，崩溃后精确到步骤级恢复；同一目录再次启动自动续跑
- **卷弧双层滚动规划** — 初始只规划 2 卷弧骨架 + 第 1 弧详细章节，后续弧/卷在写作推进到时再由 Architect 展开，每次展开都参考前文，远期规划不空洞
- **自适应上下文策略** — 根据总章节数自动切换全量 / 滑窗 / 分层摘要；四级压缩管线 + CJK token 估算 + 压缩后恢复包；每章写作时还会从伏笔、角色出场、状态变化、关系四个维度自动推荐相关历史章节
- **用户实时干预** — 写作过程中随时在输入框注入修改意见（无需暂停），系统自动评估影响范围并重写受影响章节
- **多 LLM 支持** — OpenRouter / Anthropic / Gemini / OpenAI / DeepSeek / Qwen / GLM / Grok / Ollama / Bedrock 及任意自定义代理，各角色可独立配置模型

### 酒馆角色扮演与剧场

- **角色扮演对话** — 支持导入 Tavern / SillyTavern 角色卡（V1 扁平结构 / V2 data 信封 / 原生 JSON），`{{char}}` / `{{user}}` 宏展开、token 预算管理与滑动窗口历史；酒馆模型可独立配置
- **剧场模式** — AI 驱动的视觉小说式剧场：Architect / Planner / Writer 三段式编排剧情，玩家选项 gate 处暂停等待选择，buffer-ahead 预写后续 beat，每个 beat 可配图
- **酒馆生图与切图** — 与小说插图共用出图服务，会话图片按会话归档

## 架构

核心设计：**事实层确定，语义层自主**。可枚举的状态迁移由确定性代码执行（Engine + Route）；边界清晰的判断按需咨询 LLM 函数（Arbiter）；开放式创作与出图提示词交给有界 LLM 单次调用。一句话概括：一个串行确定性 Engine、三类 Worker、一个出图 Prompter、少数几个 Arbiter 裁定函数、一个文件系统事实层。

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
│(LLM循环) │ │(LLM循环)   │ │(LLM循环)│  └──────────┘
└───┬──────┘ └─────┬─────┘ └───┬───┘
    └──────────────┴───────────┘
              │ 工具调用（IO + checkpoint）
┌─────────────▼───────────────────────────────────┐
│                     Store                        │
│  Progress / Checkpoint / Outline / Drafts / ...  │
└─────────────┬───────────────────────────────────┘
              │ unit 完成（桥开启时）
┌─────────────▼───────────────────────────────────┐
│  Prompter（LLM 单次 JSON）→ Schema 校验 / 字段绑定 │
│  → imagejob 状态机 → ComfyUI 出图 → 图片落盘      │
└─────────────────────────────────────────────────┘
```

### 智能体职责

| 角色 | 职责 | 工具 |
|--------|------|------|
| **Architect**（`architect_short` / `architect_long`） | 生成前提、大纲、角色档案、世界规则；弧/卷边界时展开后续规划 | `novel_context` `save_foundation` `revise_outline` `audit_foundation` |
| **Chapter Planner** | 按 Writer 上下文预算把当前大纲章拆成场景卡和写作片段卡；**小节数即插画数** | `novel_context` `read_chapter` `plan_chapter` |
| **Writer** | 按片段卡逐小节完成正文；章节号和 unit ID 由宿主注入，只提交 `content` | `novel_context`（unit 投影） `write_chapter_unit` |
| **Arbiter** | 语义裁定：启动选规划师、用户干预分诊、失败/僵局出路 | 无（单次 LLM 调用，输出结构化决策） |
| **Prompter** | 出图提示词：为小节 / 酒馆 / 剧场 beat 生成符合工作流 schema 的结构化 JSON | 无（单次 LLM 调用，Schema 校验兜底） |

> 与上游的差异：上游的 Editor 七维评审、弧级评审与摘要已从确定性主循环移除（`save_review` / `save_arc_summary` 等工具代码保留，但不再自动派发）。本 fork 的质量策略是：**文本够用即可，把评审省下的心智和算力做出图**。

### 写作与出图流程

```
用户需求 → Arbiter 选规划师 → Architect 设定/大纲 → Planner 拆章为场景卡 + units
                                                    │（小节 = 一张插画）
                                                    ▼
                          Writer 逐小节写作 → units 全部完成 → Engine 程序级提交整章
                                                    │（图片桥开启时）
                                                    ▼
                     Prompter 为每个小节生成 JSON 提示 → Schema 校验/字段绑定 → ComfyUI
                                                    │
                                  全部小节图片落盘 → 章节完成 → 下一章
```

1. Chapter Planner 用 `novel_context(context_mode="chapter_plan")` 读取上下文并调用 `plan_chapter`，保存叙事场景与有序 writing units；
2. Writer 调用无参数 `novel_context()`，宿主强制绑定当前章节并只投影当前 unit，Writer 每次仅向 `write_chapter_unit` 提交 `content`；
3. 每个 unit 独立落盘，units 全部完成后由 **Engine 程序级提交整章**——提交环节零 LLM 开销；
4. 图片桥开启（`enabled + auto_generate`）时，每个小节自动触发图片作业；Engine 跟踪逐小节图片进度（job 完成且图片落盘才算完成），出图进度直接参与章节完成判定。

### 状态迁移规则

系统内部把运行状态拆成两层：

- **Phase** — 大阶段，只前进不回退：`init -> premise -> outline -> writing -> complete`
- **Flow** — 写作期内的活跃流程：`writing / reviewing / rewriting / polishing / steering` 之间按规则切换

这些规则由代码中的轻量校验统一约束（`domain/transitions.go`），避免状态回退或跳到不合理的流程分支。

### 长篇滚动规划

传统方案一次规划所有章节，300+ 章时大纲空洞、节奏像赶进度。本系统采用**指南针 + 视野滚动规划**：

```
初始规划                     弧结束时                      卷结束时
┌────────────────────┐    ┌─────────────────────┐    ┌─────────────────────┐
│ 终局方向（指南针）   │    │ 弧摘要 + 角色快照    │    │ 卷摘要               │
│ 起步 2 卷，后续按需  │    │ Architect 展开下一弧 │    │ Architect 自主创建   │
│ 第1弧详细章节       │ →  │ Writer 继续写作      │ →  │ 下一卷 + 更新指南针   │
└────────────────────┘    └─────────────────────┘    └─────────────────────┘
```

- **指南针（Compass）** — 终局方向 + 活跃长线 + 规模估计，每次卷边界由 Architect 更新
- **骨架弧** — 只有 goal + 预估章数，到达时再展开详细章节
- **渐进细化** — 每次展开都参考前文摘要、角色快照、风格规则，越往后写越精确

### 长篇上下文管理

500+ 章小说采用三级摘要 + 四级压缩管线 + 智能推荐：

```
卷（Volume）→ 卷摘要
└── 弧（Arc）→ 弧摘要 + 角色快照 + 风格规则
    └── 章（Chapter）→ 章摘要（滑窗最近3章）
```

当对话超出模型上下文窗口时，按代价从低到高逐级压缩：

```
ToolResultMicrocompact → LightTrim → StoreSummaryCompact → FullSummary
     清理旧工具结果        截断长文本      store 零 LLM 压缩      LLM 摘要兜底
```

- **StoreSummaryCompact** — Writer 专用，用 store 中已有的章节摘要、角色快照直接替换旧消息，零 LLM 开销
- **压缩后恢复包** — FullSummary 后自动注入当前章节计划、大纲和角色快照，防止 Writer 压缩后"失忆"
- **熔断器** — 压缩连续失败时自动跳过并显式告警，半开模式下轮自动重试
- **CJK Token 估算** — 中文 `runes × 1.5`，不会因 `bytes/4` 低估导致压缩触发滞后
- **相关章节推荐** — 每章写作时从伏笔、角色出场、状态变化、关系四个维度反查历史章节，推荐 Writer 按需回读

## 快速开始

```bash
# 从源码构建（需要 Go 1.25+）
git clone https://github.com/Leixx98/ainovel_wirte.git
cd ainovel_wirte
go build -o ainovel-cli ./cmd/ainovel-cli

# 首次运行，自动进入引导流程（选择 Provider → 输入 API Key → Base URL → 模型名）
./ainovel-cli
```

默认启动 Web 工作台；`--web` 仅为旧脚本兼容保留，不改变行为：

```bash
./ainovel-cli              # Web 工作台（默认 127.0.0.1:8080，占用则顺延）
./ainovel-cli --web        # 兼容旧脚本，与无参数启动相同
./ainovel-cli --listen 0.0.0.0:8080  # 指定 Web 监听地址
./ainovel-cli --headless --prompt "写一本东方玄幻长篇，主角从边陲小城起步"   # 无人值守
./ainovel-cli eval --help  # 离线评测入口
```

### Docker

Docker Compose 默认在 `0.0.0.0:8080` 启动 Web 工作台，并将配置和作品目录挂载到宿主机：

```bash
mkdir -p config workspace
docker compose build
docker compose up
# 浏览器打开 http://localhost:8080
docker compose run --rm ainovel --headless --prompt "写一本悬疑短篇"
```

Web 工作台提供创作输入、运行状态、模型设置、导入、导出、ComfyUI 画布、酒馆和剧场功能。首次运行会先打开 Web 配置页，保存模型配置后进入工作台。

`--headless` 不执行首次配置；请先启动默认 Web 配置页完成配置。Headless 可用 `--prompt`、`--prompt-file <路径>` 或 `--prompt-file -` 从标准输入读取需求；不提供 prompt 时仅恢复当前目录已有会话。

### 管理多本小说

每本小说绑定到启动目录，产物落在 `{cwd}/output/novel/`。换目录启动 = 换一本，`cd` 回去启动 = 自动从最近 checkpoint 恢复。配置 `~/.ainovel/config.json` 全局共享，无需复制。

## 配置文件

首次运行时，Web 配置页会生成配置文件 `~/.ainovel/config.json`。工作台的模型设置可新增或编辑 Provider、保存多个模型、设置上下文窗口并切换各角色使用的模型。

配置文件查找顺序（后者覆盖前者）：

1. `~/.ainovel/config.json` — 全局配置
2. `./.ainovel/config.json` — 项目级覆盖（可选）

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
        { "name": "google/gemini-2.5-flash", "context_window": 200000 },
        { "name": "google/gemini-2.5-pro", "context_window": 1000000 }
      ]
    }
  },
  "style": "default"
}
```

> 覆盖规则：标量字段后者覆盖前者；`providers` 和 `roles` 按 key 合并；项目级 `.ainovel/` 是全局 `~/.ainovel/` 的镜像。该目录含密钥，已默认加入 `.gitignore`。

上下文窗口按「模型专属值 → 旧顶层 `context_window` → 模型注册表 → 200K 兜底」的顺序解析，只影响本地上下文压缩时机，不改变远端 API 的真实请求限制。

### 按角色使用不同模型

通过 `roles` 字段为不同智能体分配不同的模型，未配置的角色使用默认模型。典型用法：**规划用强模型、正文用便宜的本地模型、出图提示词用快模型**：

```jsonc
{
  "roles": {
    "architect": { "provider": "openrouter", "model": "google/gemini-2.5-pro", "reasoning_effort": "low" },
    "writer": { "provider": "ollama", "model": "qwen3:latest" },
    "prompter": { "provider": "openrouter", "model": "google/gemini-2.5-flash" },
    "galgame": { "provider": "openrouter", "model": "anthropic/claude-sonnet-4" }
  }
}
```

可配置的角色：

| 角色 | 说明 |
|---|---|
| `architect` | 规划师（短篇/长篇规划共用） |
| `chapter_planner` | 章节规划（未配置时落到 architect） |
| `writer` | 正文写作 |
| `prompter` | 出图提示词生成 |
| `galgame` | 酒馆角色扮演对话 |
| `import_segment` / `import_analyze` / `import_synthesize` | 导入管线三个语义函数档位（未配置时落到 architect；可把机械性更强的切分指到更便宜的模型） |

语义裁定 Arbiter 统一使用默认模型，不开放独立角色配置。

### 自定义代理与本地模型

选择任意 Provider 后填写代理地址即可，或使用 Custom Proxy 并指定 API 协议类型：

```jsonc
{
  "provider": "my-proxy",
  "model": "gpt-4o",
  "providers": {
    "my-proxy": {
      "type": "openai",
      "base_url": "https://proxy.example.com/v1"
    }
  }
}
```

支持的 Provider：`openrouter` / `anthropic` / `gemini` / `openai` / `deepseek` / `qwen` / `glm` / `grok` / `ollama` / `bedrock` 及任意自定义代理。

本地 `ollama` 配置（适合跑批量正文）：

```jsonc
{
  "provider": "ollama",
  "model": "qwen3:latest",
  "providers": {
    "ollama": {
      "base_url": "http://localhost:11434/v1"
    }
  }
}
```

`providers.<name>.extra` 为 provider 级配置（`user_agent`、`headers`、`anthropic_beta` 等），`extra_body` 是请求体扩展参数，两者不要混用。更多高级配置参考仓库根目录的 `config.example.jsonc`。

## ComfyUI 图片桥接

出图是本 fork 的重心，默认 Web 工作台提供完整配置与监控界面。

### 画布工作台

- 导入 ComfyUI API workflow，解析节点结构
- 在节点上**暴露字段**（字段 ID + 说明），这些字段就是 Prompter 的填空位
- 切换 Test canvas 直接试跑并预览图片，验证 workflow 本身可用

### 小说小节插图桥（Unit Bridge）

桥配置（Web 工作台维护）控制小说出图的自动化程度：

| 配置项 | 说明 |
|---|---|
| `enabled` | 桥总开关 |
| `auto_generate` | 开启后每个小节完成后**自动**触发出图作业，图片进度参与章节完成判定 |
| `workflow_id` | 使用的画布工作流 |
| `strict` | 严格模式：Prompter 的 JSON 必须精确匹配 schema（未知字段/缺字段即拒绝）；非严格模式允许用字段默认值兜底 |
| `prompter_timeout_ms` | 提示词生成超时 |
| `previous_tail_chars` | 传给 Prompter 的前文结尾字符数，保证画面连续性 |

出图链路：

```
unit 完成 → Prompter（LLM 单次调用，JSON 模式）
         → Schema 校验（strict/non-strict）
         → 字段绑定进 workflow
         → 提交 ComfyUI /prompt → 轮询 /history
         → 下载图片落盘（Content-Type 校验 + 大小上限）
```

每个作业是独立状态机：`prompting → validating → binding → submitting → queued → running → completed / failed / timeout / cancelled`，支持取消、重试与提示词重生成。

### 酒馆与剧场出图

酒馆对话配图、剧场 beat 配图与小说小节插图共用同一个 `imagejob` 服务（同一状态机、同一 ComfyUI 客户端），只是触发源和落盘路径不同：

```
galgame/sessions/{session_id}/images/{job_id}.png
```

## 酒馆角色扮演与剧场模式

Web 工作台的「酒馆」标签页：

- **角色卡导入** — 兼容 Tavern / SillyTavern 角色卡（V1 扁平结构、V2 `data` 信封、原生 JSON），支持 `alternate_greetings`、`system_prompt`、`post_history_instructions`、`{{char}}`/`{{user}}` 宏
- **会话管理** — 每个角色多会话；提示词按「主指令 → 角色描述 → 人格 → 场景 → 用户 persona」组装，带 token 预算管理与滑动窗口历史
- **剧场模式** — AI 驱动的视觉小说式剧场：
  - Architect 定设定 → Planner 出 beat 分镜 → Writer 逐 beat 写作
  - 玩家选项处（gate）暂停等待选择，剧情因选择分叉
  - buffer-ahead 预写后续 beat（默认 8 条），翻页无等待
  - 每个 beat 可自动配图

## 导入 / 导出 / 诊断 / 仿写

- **导入**（Web 设置页）— 把已有小说**语义编译**进项目用于续写：源文件快照 → LLM 识别章节边界 → 逐章提取事实 → 分层归纳 → 发布 Foundation；分阶段断点恢复，中断重跑只补缺失部分
- **导出**（Web 工作台）— 合并已完成章节为 TXT（纯文字）或 EPUB（图文），只读操作，写作中途随时可用
- **诊断** — 运行结束或错误返回时生成已脱敏的 `meta/diag-export.md`；诊断内核仍可供评测和内部调用复用
- **仿写画像** — 相关分析与导入内核保留，供现有程序化调用方使用

## 写作风格与自定义规则

- **风格预设**（`style` 字段）— `default` / `suspense` / `fantasy` / `romance`，可在 `<输出目录>/style/styles/` 或 `~/.ainovel/style/styles/` 新增自定义风格（文件名即风格名）
- **去 AI 味基线** — 内置机械黑名单（commit 时确定性检查）+ 语义判据，无需配置即生效
- **自定义规则**（`~/.ainovel/rules/` 全局 / `./.ainovel/rules/` 本书）— 用大白话写偏好即可（如「每章 3000 字左右」「主角别写成圣母」），系统会归一化为结构化约束，写作时自动遵循、提交时机械自检

> 本 fork 对文本质量的默认要求较为宽松，这些机制保留为可选项：对出图场景来说，规则目录里写清楚「画面感强的场景多给细节」往往比文学性约束更有价值。

## 输出结构

所有创作数据保存在 `output/novel/` 目录中，中断后重新运行自动从上次进度续写：

```
output/novel/
├── chapters/           # 终稿（Markdown）
├── summaries/          # 章节摘要（JSON）
├── drafts/             # 章节草稿（含逐 unit 工件）
├── reviews/            # 评审报告（上游遗留结构）
├── timeline.jsonl      # 时间线事实（追加日志）
└── meta/
    ├── premise.md / outline.json / characters.json / world_rules.json
    ├── layered_outline.json / compass.json    # 长篇分层大纲与指南针
    ├── progress.json                          # 进度状态
    ├── checkpoints.jsonl                      # Step 级 checkpoint
    ├── decisions.jsonl                        # Arbiter 裁定审计日志
    ├── comfyui/workflows/                     # 画布工作流定义
    ├── images/jobs/                           # 图片作业与产物
    └── runtime/tasks/                         # 运行时任务状态
```

酒馆数据（角色卡 / 会话 / 会话图片）存放在项目的 `galgame/` 目录下。

## 断点恢复

写一部长篇可能需要数小时甚至数天，崩溃、断网、Ctrl+C 都是常见情况。系统在**同一目录再次运行时自动恢复**，无需手动操作：

1. 读取 `progress.json` + 最近 checkpoint + 待处理信号
2. 精确到 step 级生成恢复指令
3. Engine 直接从 store 重算路由续跑——没有会话需要恢复，checkpoint 幂等保证重复派发安全

文件写入使用 temp + fsync + rename 原子操作，即使在写入过程中断电也不会损坏已有数据。出图作业同样可恢复：重启后未完成的小节图片作业可以重新触发。

## 实时干预

创作过程中可以随时通过 Web 工作台输入框注入修改意见，**不需要暂停或重启**：

```
❯ 把感情线提前到第4章，增加男女主的对手戏
```

系统自动：记录干预指令（崩溃恢复用）→ Arbiter 裁定（查询秒级回显；控制类动作在章节边界安全提交）→ 按裁定执行（修改设定 / 重写章节 / 落盘写作规则），每次裁定审计可回放。

## 设计理念

> **事实层确定，语义层自主。** 模型自由在验证不可能的地方（写什么、画什么），被约束在验证可能的地方（顺序、幂等、阶段、schema）。

- **可枚举的迁移归代码** — "下一个派谁"是读事实查表（`flow.Route` 纯函数，万级组合穷举测试），零 LLM 开销
- **边界清晰的判断归 Arbiter** — 事实进、结构化决策出、机械校验兜底、每次裁定落盘可回放
- **开放式创作归 Worker** — 小节之内 Writer 完全自主；工具失败时返回结构化错误与出路提示
- **出图提示词归 Schema** — Prompter 只输出 JSON，strict 校验 + 字段绑定保证 workflow 永远拿到合法输入
- **工具只返事实** — 单文件原子 IO + 显式错误 + 幂等重放；返回值是 JSON 事实字段，不夹带任何指令字符串
- **拒绝复杂编排** — 没有 task queue、没有 policy engine，一个串行循环 + 一张决策表 + 几个裁定函数就是全部控制流

## 技术栈

- **Go 1.25** — 主语言
- **Web 工作台** — 默认交互入口，提供写作、设置、导入导出、ComfyUI、酒馆与剧场功能
- **[agentcore](https://github.com/voocel/agentcore)** — 极简 Agent 内核（tool-calling + streaming）
- **[litellm](https://github.com/voocel/litellm)** — 统一 LLM 接口适配（本仓库 vendor 于 `third-party/litellm`）
- **ComfyUI** — 图片生成后端（HTTP API）

## 致谢与许可证

本项目派生自 [voocel/ainovel-cli](https://github.com/voocel/ainovel-cli)（作者 voocel），感谢上游项目打下的引擎基础。上游的设计文档归档在本仓库 `docs/history/`，当前重构方向见 [docs/refactor.md](docs/refactor.md)。

本仓库许可证见根目录 [LICENSE](LICENSE)（Apache-2.0）。使用本项目的衍生作品请同时遵守上游项目的许可证要求。
