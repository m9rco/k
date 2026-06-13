# Tasks — Enhance Agent Harness & Chat Feedback

按"协议/后端 → 工具/编排 → 持久化 → 前端 → 验证"顺序推进。标 [P] 的可并行。

## 1. 传输协议与事件类型
- [x] 1.1 在 `internal/transport/event.go` 新增事件类型：`turn_start`、`turn_end`（capsule 已存在，复用）。
- [x] 1.2 在 `internal/transport/ws.go` 确认/补全入站 `capsule_select` 解析（`Inbound.Selection`/`Text`），补单测覆盖解析。
- [x] 1.3 定义 capsule 出站 payload 结构（question + options[{label,value,editable_hint}]）与 turn_end 元信息（toolUsed、hasCapsule）。

## 2. System Prompt 重构
- [x] 2.1 将 `internal/agent/prompt.go` 的 `SystemPrompt()` 重构为分层 section（角色/能力/工具规范/交互与澄清/输出格式/安全/语言）。
- [x] 2.2 新增"意图缺少必要信息时先调用澄清工具反问"指令；新增"禁止 markdown、选择类走澄清工具"输出规范。
- [x] 2.3 保留并归类既有 7 条约束；补/改 `prompt_test.go` 断言关键指令存在（澄清优先、禁 markdown、注入防护、引用 id）。

## 3. 澄清工具与编排
- [x] 3.1 在 `internal/agent/tools.go` 新增 `clarify_intent` 工具（params：question、options[]），加入 `AsyncTaskTools()`（ToolReturnDirectly）。
- [x] 3.2 在工具实现/回调中，于 `internal/agent/agent.go` 发出 `EventCapsule`（携带 question + options），并按 ToolReturnDirectly 结束本轮。
- [x] 3.3 在 `Handle` 入口（append 用户消息后、`ra.Stream` 前）发 `turn_start`；在 turn 结束（含 error / capsule）发 `turn_end`（带元信息）。
- [x] 3.4 处理 `capsule_select` 入站：把用户选择/改写文本作为新一轮 user 输入续接 `Handle`（追加 window 后重新处理）。
- [x] 3.5 补 `agent_test.go`：意图不全→产出 capsule（断言 EventCapsule 发出、本轮未调执行类工具）；capsule_select 续接→信息充分后调用对应工具。

## 4. Context 压缩改进
- [x] 4.1 在 `internal/agent/window.go` 改进 token 估算：CJK 字符与 ASCII 字节分别估算后合计；补表驱动单测对比中英混合样本。
- [x] 4.2 暴露窗口状态（估算 token/预算/已压缩）并在 `turn_end` 事件携带下发。
- [x] 4.3 保留 summarizer 注入点；将默认 summarizer 改为按语义边界截断（不破坏行/句），补单测。

## 5. 对话历史持久化（session-management）
- [x] 5.1 在 `internal/store/store.go` 新增 `messages` 表 DDL（id, session_id, role, content, tool_refs, created_at）与 Insert/ListBySession 方法 + 单测。
- [x] 5.2 在 `Handle` 结束时落库本轮 user/assistant 消息（仅文本 + 引用 id，不存二进制）。
- [x] 5.3 在 `window(sessionID)` 首次构建时从 `messages` 恢复历史并重建窗口（恢复后即受预算压缩）；补恢复路径单测（重启后续接、无历史空启动、超预算压缩）。

## 6. 前端：loading 时序
- [x] 6.1 `web/src/store/controller.ts`：`sendMessage` 后立即渲染 loading 占位 assistant 气泡（本地即时），并处理 `turn_start`/`turn_end` 事件 case。
- [x] 6.2 `turn_end` 收束 loading 并据元信息对齐打字机/工具卡片收尾；`turn_end` 携带窗口状态时即时更新 context 展示。

## 7. 前端：capsule 渲染与回传
- [x] 7.1 `controller.ts` 新增 `capsule` 事件 case，存入 chat 流为一种新气泡类型。
- [x] 7.2 新增 capsule 气泡组件：问题文案 + 选项 chip（点击直接回传 value）+ 每选项可编辑输入（预填 editable_hint，改写后提交回传）。
- [x] 7.3 回传经既有 WS 入站发送 `capsule_select`；回应后该 capsule 置为已回应态，禁止重复提交。

## 8. 验证与回归
- [x] 8.1 `go build ./...` 通过；`go test ./internal/...` 全绿（新增 store/agent/window/transport 单测）。
- [x] 8.2 前端 `tsc -b` + `npm run build` 通过。
- [x] 8.3 端到端手测（Playwright）：发送即 loading（150ms 内 3 点）；意图不全弹 capsule；点选项/改写均能续接；重启后历史恢复（回忆"晴日沙滩"）；回复无 markdown。
- [x] 8.4 回归既有流式：reasoning 打字机、tool_call/tool_result 卡片、message done 时序不退化。
- [x] 8.5 `openspec validate enhance-agent-harness --strict` 通过。

## 补充：输出无 markdown 的双重保障
- [x] 9.1 前端新增 `stripMarkdown`（`web/src/lib/utils.ts`）并应用于 assistant 气泡：即使模型违反 prompt 规则吐出 `**`/`##`/```，渲染层也兜底剥离（运行时模型为 deepseek-v4-flash，不总是遵守 prompt）。
