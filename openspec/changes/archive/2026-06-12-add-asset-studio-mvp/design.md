# Design: Asset Studio MVP

## Context
全新代码库。后端 Go 单二进制 + `embed` 嵌入前端，供小团队内部使用，无鉴权、密钥硬编码。核心是一个对话式 Agent，按用户意图分发到生图/裁剪等工具，结果回填到可操作的工作区。本设计聚焦跨系统决策：Agent 框架、实时传输、context 管理、prompt 注入防护、存储分层。生视频/爬取/长期记忆为后续 change，但架构需为其预留扩展点。

## Goals / Non-Goals
- Goals
  - 单二进制可运行的对话 → 生图 → 裁剪 → 下载闭环
  - 工具边界清晰、可独立 BDD 测试
  - 长任务异步、可见状态、可部分重试
  - context 滑窗防胡言乱语；预设意图外礼貌拒绝
- Non-Goals（MVP 不做）
  - 用户鉴权 / 多租户 / 安全加固
  - 生视频、物料爬取、长期记忆与自进化引擎
  - 模型可选（服务端硬编码）

## Decisions

### D1: Agent 框架用 Eino
- 选 CloudWeGo Eino：内置 `ChatModelAgent`（ReAct）、工具调用、流式编排、interrupt/resume checkpoint，官方 Claude/OpenAI provider，DeepSeek 走 OpenAI 兼容。
- interrupt/resume 直接支撑"切尺寸弹胶囊按钮等用户选择"这类 human-in-the-loop 步骤。
- 备选：LangChainGo（checkpoint 较弱，需手搓）；自写 loop（控制力最强但维护成本高）。

### D2: 实时传输 = WebSocket（对话）+ SSE（任务进度）
- WS：双向，承载对话消息、工具调用事件、用户在胶囊步骤的选择回传。
- SSE：单向，承载单个长任务（生图）的 queued→running→progress→done/failed 进度。每个任务一条 SSE 流，前端按任务 id 订阅。
- 理由：对话天然双向用 WS；任务进度是服务端单向推送，SSE 更省、断线重连语义更简单。

### D3: Context 滑动窗口（裁剪 + 压缩）
- 维护一个消息窗口：超过 token 预算时，保留 system + 最近 N 轮原文，对更早的轮次做摘要压缩成一条 summary 消息。
- 工具调用的大块结果（如 base64 图）不进 LLM context，仅以引用 id 进入；防止 context 爆炸与胡言乱语。

### D4: 意图识别 = 白名单分发
- agent 在 system prompt 中约束：仅识别预设意图（换角色/背景/文案、切尺寸、下载、[预留] 生视频/爬取）。
- 命中 → 调对应工具；未命中 → 不执行，返回礼貌说明 + 可做的事清单。

### D5: Prompt 注入防护
- 用户对已生成图"二次调整"时，其自由文本不直接拼进生图 prompt。
- 服务端用结构化 slot（目标尺寸/背景描述/角色描述/文案）承接用户输入，再由服务端模板组装最终 prompt；对用户文本做长度与控制指令过滤（剥离"ignore previous / system:"等模式）。

### D6: 颜色适配（生图硬指标）
- 换角色/背景/文案后整体色调需协调。MVP 做法：从原图/参考图提取主色板（dominant palette），作为结构化约束注入生图 prompt（"harmonize with palette #xxxxxx ..."），并在 prompt 模板中固化"avoid abrupt/jarring color contrast"。
- 验收以 Scenario 描述行为，不强约束具体算法实现。

### D7: 存储分层
- 短期：进程内内存（活跃 session 的消息窗口、任务状态）。
- 长期：SQLite（session 记录、生成产物元数据与本地文件路径、为后续记忆系统预留 preference/feedback 表的迁移位）。
- 生成的图存本地文件，DB 存路径与元数据；下载/打包从本地读。

### D8: 平台尺寸为数据驱动配置
- 尺寸 list 是配置数据（平台 → [{name, w, h, orientation}]），非代码。胶囊按钮由该配置渲染。裁剪为纯图像处理（Go 图像库 resize/crop），不走 AI。
- 具体平台与规格由用户在 apply 阶段提供（见 Open Questions）。

## Risks / Trade-offs
- Eino 版本演进快（v0.9.x）→ 锁定 minor 版本，工具层对框架做薄封装隔离。
- gpt-image-1 两供应商行为差异 → 抽象 ImageProvider 接口，主失败切备，记录 provider 来源。
- SSE 在某些代理后缓冲 → 设置正确的 no-buffer header；必要时回退轮询。
- 颜色适配是"软指标"，模型不一定每次达标 → 提供二次调整 + 部分重试兜底。

## Migration Plan
全新项目，无迁移。建库即初始 schema；后续记忆系统通过增量 migration 加表。

## Open Questions（apply 阶段需用户提供）
- 各模型的 base URL / API key / 精确 model id（含 DeepSeek 测试模型名、两个生图供应商端点）
- 平台尺寸清单（平台名 + 各广告位横/竖尺寸）
- 生图本地存储目录与产物保留策略（保留时长/清理）
