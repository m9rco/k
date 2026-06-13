# Enhance Agent Harness & Chat Feedback

## Why

当前 agent 对话层在交互反馈、互动能力与 harness 工程上存在明显短板，审计（基于 `internal/agent/*.go`、`internal/transport/*.go`、`web/src/store/controller.ts`）发现以下问题：

1. **发送后空窗期**：用户发消息后，前端不显示任何 loading 态；后端 agent 需等模型首个 stream chunk（实测生图意图下整轮 reasoning+决策可达数秒）才发出第一个事件。期间界面无反馈，体验上像"卡住"。
2. **缺乏澄清/互动能力**：当用户意图不明确（如"帮我改改这张图"未说改什么）时，agent 只能凭猜测调用工具或用纯文本泛泛回复，无法结构化反问并给出可点选项。`EventCapsule` 事件类型早已定义（`internal/transport/event.go:23`）但**后端无人发送、前端 controller 无 case 处理**，是一段未接通的死基础设施。
3. **输出无交互规范**：agent 回复是自由文本，未约束禁用 markdown，也没有"返回可点按钮 / 选项 / 状态"的 web 交互协议；前端只能把回复当纯文本气泡渲染。
4. **Harness 工程缺口**：
   - System prompt（`internal/agent/prompt.go`）是单段拼接字符串，无"不确定时反问"指令、无结构化输出约束。
   - 无澄清类工具，agent 无法主动发起结构化互动。
   - Context 窗口（`internal/agent/window.go`）token 计数用 `~4 字符=1 token` 启发式，对 CJK 偏差大；摘要为截断式（每条取前 200 字符），易丢失语义。
   - 对话历史**完全在内存**，进程重启即丢，无跨会话持久化与恢复。

## What Changes

- **立即 loading 反馈**：用户消息一旦被后端接收，立即下发一个"思考开始"信号（turn 生命周期事件），前端据此即时进入 loading 态，无需等模型首 chunk。reasoning 增量与最终回复时序保持"思考先行、结论随后"。
- **结构化互动协议（capsule）**：接通 `EventCapsule`。agent 在意图不明确时通过新增的澄清工具返回一组**可点选项**，每个选项既可点击直接回传、也可作为可编辑文本由用户改写后发送（选项气泡 + 自由输入）。前端渲染 capsule 并把用户选择/输入经既有 `capsule_select` 入站协议回传给 agent 续接对话。
- **交互输出规范**：在 system prompt 中约束 agent 输出为面向 web 的简洁纯文本（禁止 markdown 标记、代码块、表格等富文本语法），需要让用户选择时改用澄清工具产出 capsule，而非在文本里堆叠选项。
- **System prompt 重构**：分层组织（角色 / 能力白名单 / 工具使用规范 / 交互与澄清规范 / 输出格式规范 / 安全规范），新增"意图不明确时先澄清再行动"指令。
- **反问/交互工具**：新增 `clarify_intent`（或等价）工具，agent 调用它产出结构化 capsule；该工具 ToolReturnDirectly，结束当前轮并等待用户回应。
- **Context 压缩改进**：token 计数对 CJK 与 ASCII 分别估算以降低偏差；将窗口状态（估算 token、预算、是否已压缩）通过事件下发给前端用于显示；保留摘要可注入 model-backed summarizer 的扩展点。
- **跨会话持久化 memory**：对话消息（user / assistant / 关键工具引用）落 SQLite，按 session 维度存储；会话重连或进程重启后，从持久化历史重建 context 窗口，恢复对话连续性。

本提案**不改动**：生图/裁剪/生视频/爬取的底层 provider 逻辑、模型硬编码策略、既有任务 SSE 进度协议。

## Capabilities & Specs

- `conversation-orchestration`（MODIFIED）：turn 生命周期事件、澄清工具与结构化互动、输出格式约束、System prompt 分层、context 压缩改进。
- `realtime-transport`（MODIFIED）：capsule 出站事件与 capsule_select 入站协议的契约。
- `frontend-experience`（MODIFIED/ADDED）：发送后立即 loading、capsule 渲染与回传交互、窗口状态显示。
- `session-management`（ADDED）：对话历史持久化与会话恢复。

## Impact

- 后端：`internal/agent/`（prompt、tools、agent orchestrator、window）、`internal/transport/`（event/ws capsule 协议）、`internal/store/`（对话历史表）、`internal/session/`（恢复）。
- 前端：`web/src/store/controller.ts`（loading 时序、capsule 事件处理与回传）、chat 相关组件（capsule 气泡 + 可编辑选项渲染）。
- 数据：SQLite 新增对话消息表；向后兼容（无历史时按空窗口启动，与现状一致）。
- 风险：capsule 协议与既有流式 message/reasoning 事件并存需明确时序；持久化历史需与 context 压缩协调（恢复后仍受 token 预算约束）。
