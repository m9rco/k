# Tasks: 推理过程流式渲染的 P0/P1/P2 状态机

## 1. 后端：轮开始流式标记与降级信号
- [x] 1.1 在 `internal/transport/event.go` 为 `turn_start` 定义携带 `streaming bool`（缺省 true）与 `degraded bool` 的载荷结构（或文档化 `Data` 约定）
- [x] 1.2 在 `internal/agent/agent.go` 的两处 `EventTurnStart` 发出点带上 `streaming: true`
- [x] 1.3 在 `internal/agent/chatmodel.go` 降级到 `fallbackStream` 时，将「本轮已降级」沿调用链回传至 orchestrator，由其补发一条 `turn_start{streaming:false, degraded:true}`（确保对同一轮幂等、不重置 turn 状态）
- [x] 1.4 Go 表驱动单测：流式正常轮 turn_start 带 `streaming:true`；降级轮额外发出 `streaming:false` 信号；旧载荷（无字段）仍可解析

## 2. 前端：等待态状态机
- [x] 2.1 在 `web/src/store/types.ts` 为等待态引入级别（如 `waitLevel: "p1" | "p2"`）与计时器引用所需状态
- [x] 2.2 在 `web/src/store/controller.ts` 将 `showLoading`/`clearLoading` 重构为分级等待态：`turn_start` 默认进入 P1 并启动 ~1500ms 升级计时器
- [x] 2.3 消费 `turn_start.streaming === false` / `degraded`：立即切 P2 并清除计时器；对重复 turn_start 幂等（不重置 `producedRef`、不重复插入气泡）
- [x] 2.4 首个 `message`/`reasoning` 增量到达时取消升级计时器并转入 P0（沿用现有 `clearLoading` 转打字机）
- [x] 2.5 在 `turn_end`、`error`、`cancel_turn`/中断路径清理升级计时器，防止悬挂

## 3. 前端：等待态呈现组件
- [x] 3.1 将 `web/src/components/chat/loading-bubble.tsx` 拆分/扩展为 P1 微提示（含"正在启动深度思考…"文案 + 微动效）与 P2 静态兜底两种呈现，遵循项目极简/留白与 `transition-all duration-200 ease-out` 规范
- [x] 3.2 等待气泡按 `waitLevel` 切换内部呈现，保持同一气泡实例避免布局跳动

## 4. 验证
- [x] 4.1 浏览器手测：正常流式轮停留 P1 后转打字机、不出现静态加载；降级轮（构造非 SSE 供应商响应）确定性进入 P2；慢首包 >1.5s 超时进入 P2；首包到达即停超时
- [x] 4.2 验证中断/插队/队列场景下计时器被正确清理、无残留 P2 误触发
- [x] 4.3 `openspec validate add-reasoning-stream-state-machine --strict` 通过
