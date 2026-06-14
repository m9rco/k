# Change: 推理过程流式渲染的 P0/P1/P2 状态机

## Why
当前后端已强制 `stream:true` 并在流式开启失败时降级为一次性补发（`internal/agent/chatmodel.go`），前端也已对思考/回答做打字机渲染。但「等待态」是单一的三点 `LoadingBubble`：它既无法表达「正在启动深度思考」的语义，也无法区分「模型在正常思考、首包在路上」与「本轮根本不会流式（已降级/网络阻塞）」两种本质不同的处境。结果是用户在慢首包时面对一个无语义、无差别的静态加载，违背「深度思考优先、严禁无差别静态 Loading」的体验目标。

本提案把发送后的等待态正式收敛为三级优先状态机（P0 打字机 / P1 毫秒级微提示 / P2 静态兜底），并让后端在「本轮非流式/已降级」时显式告知前端，使 P2 可被确定性触发而非仅靠超时猜测。

## What Changes
- 在轮生命周期事件中新增**流式能力标记**：`turn_start` 携带 `streaming: true|false`（默认 `true`）；当后端在开启流式失败而降级为一次性补发时，发出携带 `degraded: true` 的信号，使前端可确定性切到 P2。**这是加法式协议演进**，旧客户端忽略新字段不受影响。
- 前端把发送后的等待态重构为 P0/P1/P2 三级状态机：
  - **P0**：收到首个思考或回答增量后进入打字机渲染（沿用现状）。
  - **P1（新默认等待态）**：`turn_start` 后、首包到达前，以轻量微提示（带「正在启动深度思考…」文案的微动效）替代现有三点气泡。
  - **P2（兜底）**：由后端 `streaming:false`/`degraded:true` 信号**或**前端 ~1.5s 仍无任何首包增量的超时**双保险**触发，切换为更明确的静态局部加载态。
- 明确「严禁在已知支持流式时长时间停留在静态 Loading」为前端可验收约束。

非目标：不改动后端流式消费/降级补发的既有真·流式逻辑（已满足 P0）；不改动思考块折叠、空轮兜底等既有行为。

## Impact
- Affected specs: `conversation-orchestration`（流式对话输出 — 新增流式能力标记）、`realtime-transport`（轮生命周期事件 — turn_start 携带流式标记）、`frontend-experience`（新增「等待态分级状态机」要求，并修订「交互流畅度」的发送后 loading 场景）
- Affected code:
  - `internal/transport/event.go`、`internal/agent/agent.go`、`internal/agent/chatmodel.go`（turn_start 载荷增字段、降级时发信号）
  - `web/src/store/controller.ts`、`web/src/store/types.ts`（等待态状态机、超时升级、消费新字段）
  - `web/src/components/chat/loading-bubble.tsx`（拆分为 P1 微提示与 P2 静态兜底两种呈现）
