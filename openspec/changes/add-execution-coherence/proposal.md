# Change: 执行连贯性专项（修复假执行重复文字 + 真实工具调用一致性）

## Why
弱会话模型常「只用文字假装执行」（如连发两遍「好的，正在把这张图生成视频，完成后会出现在左侧工作区。」）而不真正发出 function call。现有 fakeAck 自纠正重试虽能让第二次补调工具，但每次 attempt 的 prose 都被 `streamOnce` 实时流式推给前端、且前端按 `target += text` 累积只在 `done:true` 重置，导致重试时第一遍假文字残留、第二遍再拼接 → 用户看到**重复确认文字**。更严重的是：当重试耗尽仍未真正调工具时，假确认文字会原样泄漏成最终回复，用户以为已执行、实际工作区空空——系统对用户**不诚实**。此外，当用户反馈「你没生成那个视频/icon」时，系统缺少确定性识别把上一轮真实意图重新触发的机制。

## What Changes
- **修复重复文字 Bug**：fakeAck 自纠正重试前，向前端下发一条「轮内增量重置」信号，使前端丢弃本轮已渲染的流式增量（assistant 气泡 + reasoning 块），下一次 attempt 从干净状态重新流式。保留真·流式（不退回缓冲到结尾），仅在罕见重试路径丢弃脏增量。
- **假执行兜底诚实化**：当重试耗尽仍 `toolCalls==0` 且回复仍像假执行 ack 时，系统 SHALL 不把假确认文字当作正常回复呈现，而是替换为一段**诚实的失败反馈**（明确告知「未能真正执行，请补充信息或重试」），并据预分类意图在可澄清时发起 clarify_intent。
- **用户反馈驱动的连贯互动**：当用户文本表达「上一轮某操作没真正发生 / 没生成某产物」（确定性关键词识别）且上一轮确实零工具调用时，系统 SHALL 注入一条结构化「补救提示」，强引导本轮真正调用上一轮本应调用的工具（可推翻），而非再次只回文字。
- 在 system prompt 中补强「绝不假执行」的约束措辞，使其与上述运行时兜底一致。

## Impact
- Affected specs: `conversation-orchestration`（新增/修改：假执行兜底、反馈驱动补救）、`realtime-transport`（新增：轮内增量重置事件）、`frontend-experience`（新增：处理重置信号）
- Affected code:
  - `internal/agent/agent.go`（重试循环下发重置信号、耗尽后诚实兜底）
  - `internal/agent/fakeack.go`（兜底决策表扩展、反馈识别）
  - `internal/agent/prompt.go`（约束措辞）
  - `internal/transport/event.go`（新增事件类型）
  - `web/src/store/controller.ts`（消费重置信号、清空 in-flight 气泡）
  - 对应单测：`fakeack_test.go`、`agent_test.go`、`stream_test.go`
