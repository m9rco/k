# Design — Enhance Agent Harness & Chat Feedback

## Context

Agent 对话层现状（审计证据）：
- 入站：`transport/ws.go` 解析 `Inbound{Type, Text, Ref, Refs, Selection}`，`Type` 已支持 `"user_message"` 与 `"capsule_select"`（`ws.go:15`），`Selection` 字段已存在（`ws.go:19`）。
- 编排：`agent.Orchestrator.Handle`（`agent.go:108`）用 Eino `react.NewAgent`（MaxStep=12，自定义 `fullStreamToolCallChecker`，`ToolReturnDirectly=AsyncTaskTools()`）。Stream 循环把 `ReasoningContent`→`EventReasoning`、`ToolCalls`→`EventToolCall`、`Content`→`EventMessage{done:false}`，turn 末补 `EventMessage{done:true}`。
- 出站事件类型（`event.go`）：message / reasoning / tool_call / tool_result / **capsule（已定义未用）** / error / task_*。
- 前端（`controller.ts:263`）：`ws.onmessage` switch 覆盖 message/reasoning/tool_call/tool_result/task_created/error，**无 capsule case**；`sendMessage`（`controller.ts:292`）发出后不设任何 loading 标志。
- Window（`window.go`）：内存 sliding window，启发式 token 计数，截断式 summarizer，无持久化。

四个目标：(1) 即时 loading；(2) 结构化澄清互动；(3) 输出格式规范；(4) harness（prompt/tools/context/memory）。

## Goals / Non-Goals

**Goals**
- 用户发送后 <100ms 内界面进入 loading，与模型首 chunk 解耦。
- agent 可在意图不明确时返回可点 + 可编辑的选项，并能接住用户回传续接对话。
- 约束 agent 文本输出为 web 友好纯文本；结构化选择走 capsule。
- 修复/增强 harness：分层 prompt、澄清工具、更准的 token 估算与窗口状态下发、对话历史持久化与恢复。

**Non-Goals**
- 不替换模型、不改 provider 调用逻辑。
- 不引入真实 tokenizer 库（仍用改进的启发式，足够 windowing 决策）。
- 不做多 agent / 子 agent 编排。
- 不实现 Eino interrupt/resume checkpoint（capsule 用"结束当前轮 + 新轮续接"的轻量方式实现，见下）。

## Decision 1 — 即时 loading 信号：turn 生命周期事件

**问题**：loading 当前隐式依赖"首个 message/reasoning 事件到达"。生图意图下模型整轮决策耗时长，首事件迟到，界面空窗。

**方案**：后端在 `Handle` 入口（`w.Append` 用户消息之后、`ra.Stream` 之前）立即发一个 turn 开始事件；turn 结束（含错误）发 turn 结束事件。前端收到开始事件即渲染 loading 占位 assistant 气泡。

**复用 vs 新增**：复用既有 `EventMessage` 易与正文混淆。决定新增 `turn_start` / `turn_end` 两个事件类型（语义清晰、前端 case 独立）。`turn_end` 同时承载本轮是否有工具调用、是否产出 capsule 等收尾元信息，便于前端关闭 loading 并对齐打字机。

**时序保证**：`turn_start` → (reasoning 增量)* → (tool_call / message 增量)* → `turn_end`。"思考先行、结论随后"由模型与既有 reasoning/message 分流保证，本提案只在最前面补 `turn_start`。

**权衡**：新增两个事件类型增加协议面，但避免给 message 事件附加状态标志导致的语义重载；前端 loading 逻辑也更确定（不再靠 message 推断）。

## Decision 2 — 结构化澄清互动：capsule 工具 + 出/入站协议

**问题**：意图不明确时 agent 只能猜或泛泛回复；无法结构化反问。

**方案**：
- 新增工具 `clarify_intent`（ToolReturnDirectly）。参数：`question`（反问语）、`options[]`（每项含 `label` 展示文案、`value` 回传值、可选 `editable_hint` 预填可编辑文本）。
- agent 调用该工具时，orchestrator 在工具回调里发 `EventCapsule`，payload = {question, options[]}，并结束当前轮（ToolReturnDirectly 语义：不再迭代）。
- 前端渲染 capsule 气泡：每个 option 是一个可点 chip；点击直接以该 option 的 value 经 `capsule_select` 回传；同时提供一个可编辑输入框，预填 `editable_hint`，用户改写后发送同样经入站协议回传。
- 入站：复用 `Inbound{Type:"capsule_select", Selection, Text}`。orchestrator 收到后把用户选择/输入作为新一轮 user 输入续接（追加到 window，重新 `Handle`）。

**为何不用 Eino interrupt/resume**：capsule 本质是"问一个问题、等一个回答"，用"结束当前轮 → 用户回传作为下一轮输入"的无状态方式即可达成，避免引入 checkpoint 持久化复杂度。代价：agent 看到的是一条新 user 消息而非严格的 tool 续接，但 window 历史完整，语义可接受。

**capsule 与 message 并存**：一轮里 agent 要么给文本结论、要么发 capsule 反问（调 clarify_intent 即 ToolReturnDirectly 结束轮）。system prompt 约束二选一，避免同屏既有正文又有选项的混乱。

## Decision 3 — 输出格式规范

在 system prompt 增加"输出格式"层：面向 web 渲染，禁止 markdown 语法（`#`、`*`、表格、围栏代码块等），用简短自然语句；需要让用户在多个具体值之间选择时，必须调用 `clarify_intent` 产出 capsule，而不是在文本里列 `1. xxx 2. yyy`。前端继续按纯文本渲染气泡（不引入 markdown 解析器），与该约束一致。

## Decision 4 — System prompt 分层重构

把 `SystemPrompt()` 从单段拼接重构为分层 section（角色、能力白名单、工具使用规范、**交互与澄清规范（新）**、**输出格式规范（新）**、安全规范、语言）。新增核心指令："当用户意图缺少必要信息（要改的图、改成什么、目标尺寸/平台等）而无法安全调用工具时，先调用 clarify_intent 反问并给出 2-4 个具体选项，而不是猜测或泛泛确认。" 既有 7 条规则语义保留并归入对应层。

## Decision 5 — Context 压缩改进

- **Token 估算**：现 `(runes+3)/4` 对 CJK 偏低。改为分别估算：ASCII 字节按 ~4 char/token、CJK 字符按 ~1.5 char/token（经验值），合计。仅用于 windowing 决策，无需精确。
- **窗口状态下发**：复用既有 `ContextState`（`agent.go` 估算 token/预算/压缩标记），在 turn 结束时随 `turn_end` 或独立事件下发，前端显示"上下文 N%"。当前前端已有 `refreshContext` 轮询端点；改为事件推送以即时反映压缩。
- **Summarizer 扩展点**：保留注入式 summarizer 接口，本提案默认仍用改进的截断式（按语义边界截断而非粗暴 200 字符），但显式留出 model-backed 实装位（非本提案目标）。

## Decision 6 — 跨会话持久化 memory

**问题**：window 在内存，重启即丢。

**方案**：
- SQLite 新增 `messages` 表：`id, session_id, role, content, tool_refs, created_at`（content 为文本；大块工具结果仍只存引用 id，与"大块不入 context"原则一致）。
- 写入时机：每轮 `Handle` 结束，把本轮 user 与 assistant 消息追加落库。
- 恢复时机：`window(sessionID)` 首次构建时，从 SQLite 按 session 读取历史消息重建 window（仍经 token 预算压缩，超预算则恢复后即压缩）。
- 向后兼容：无历史记录时按空窗口启动 == 现状。

**权衡**：持久化全文有体积增长，但小团队内部工具规模可接受；不存大块二进制（仍引用 id）控制了主要膨胀源。

## Risks / Open Questions

- capsule 续接用"新 user 轮"而非 tool 续接，极端情况下模型可能未把用户回答关联到原问题；靠 window 历史 + prompt 约束缓解。
- 持久化历史与 context 压缩需协调：恢复后窗口仍可能立即触发压缩（预期行为，记为 scenario）。
- 新增 turn_start/turn_end/capsule 三类前端 case，需保证旧事件行为不回归（既有 message/reasoning 时序测试需保留）。

## Migration

- 数据库：新增 messages 表为增量 DDL，老库自动建表，无破坏性变更。
- 协议：新增事件类型为加法；旧客户端忽略未知事件类型不报错（前端 switch default 不抛错）。
