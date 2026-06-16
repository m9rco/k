# Design: 诊断日志与 trace 链路追踪

## Context

排查模型幻觉与工具不执行需要"事后可复盘单次对话"的能力。现状是 stderr 纯文本、无 trace、按时间穿插多会话、关键字段（工具入参、压缩快照）缺失或被截断。本设计在不改变任何业务行为契约的前提下，叠加一层可落地、可追踪、结构化的诊断日志。

约束（来自 project.md）：
- Go 单二进制，标准库优先，小团队内部使用、不做安全加固。
- 错误显式 wrap，模型/供应商配置集中硬编码。
- 已有 `withSession(ctx, sessionID)` 的 ctx 注入模式；异步生成用 `context.WithoutCancel(ctx)` 保留 ctx 值。

## Goals / Non-Goals

Goals:
- 单次 turn 可凭 `trace_id` 在日志文件中拉出完整链路（含其触发的异步长任务）。
- 四类幻觉/工具排查关键节点有结构化、不截断的记录。
- 零业务行为改变；未配置时退化为当前 stderr 行为。

Non-Goals:
- 不引入分布式 tracing（OpenTelemetry）；同进程内一个高性能日志库足够。
- 不做日志采集/上报/可视化后端，不做跨进程 trace 传播。
- 不改前端事件协议（trace 仅服务端日志用，不下发前端）。
- 本期不做日志轮转（rotation）——文件无限追加，由运维侧（logrotate）或后续 change 处理。

## Decisions

### D1: 选用 zerolog 作为日志底座
**库选型**：在"高性能 + 少引用无用代码"两个约束下比较候选：

| 候选 | 性能 | 依赖成本 | 否决/选用理由 |
|------|------|----------|---------------|
| 标准库 `log/slog` | 中（有反射/分配） | 零 | 性能不及专用库，放弃 |
| `sirupsen/logrus` | 低（反射重、分配多） | 已间接引入 | 虽已在 go.sum，但性能差，不符合"高性能" |
| `uber-go/zap` | 高 | 较多（multierr 等） | 性能达标但 API 重、依赖多 |
| **`rs/zerolog`** | **最高（零分配）** | **极少** | **选用**：零分配 JSON、依赖几乎为零，且原生支持 context 携带 |

选 **zerolog**：链式 API 直接写 JSON、零分配热路径，额外间接依赖极少，契合单二进制内部工具。

`zerolog.New(w).With().Timestamp().Logger()` 作为根 logger。输出目标 `w`：
- 配置了日志文件 → 打开文件（`O_APPEND|O_CREATE|O_WRONLY`）。
- 开启 stderr 镜像 → `zerolog.MultiLevelWriter(file, os.Stderr)`（开发期可对 stderr 套 `ConsoleWriter` 美化，文件仍为纯 JSON）。
- 未配置文件 → 退回 `os.Stderr`（等价现状）。

> JSON 编码：zerolog 默认用内建编码器即可满足；项目已间接引入 `bytedance/sonic`，但不为此额外接线，保持最小改动。

### D2: trace_id 走 zerolog 原生 context 携带
新增 `internal/log`（拟）包，**薄封装** zerolog 的 context 能力，避免自造 context key 样板：
- `func WithTrace(ctx, traceID, sessionID) context.Context`：以根 logger 派生 `logger.With().Str("trace_id", traceID).Str("session_id", sessionID).Logger()`，再 `logger.WithContext(ctx)` 存入 ctx。
- `func From(ctx) *zerolog.Logger`：`zerolog.Ctx(ctx)` 取出已绑定 `trace_id`/`session_id` 字段的 logger；ctx 中无则返回根 logger（zerolog.Ctx 对空 ctx 返回 disabled/默认 logger，门面再兜底为根 logger）。

`runTurn`（main.go）在进入 `orch.Handle` 前生成 `trace_id`（复用 `id.New("trace")`）并 `WithTrace` 注入。`Handle` 内已有的 `ctx = withSession(...)` 与之并存。异步生成 `Start` 已用 `context.WithoutCancel(ctx)`，zerolog logger 作为 ctx 值自动随之进入 `run` goroutine——长任务日志因此天然回连到触发它的 turn。

> 备选：自建 context key + slog.With。否决——zerolog 已原生提供 `WithContext`/`Ctx`，复用它代码更少（呼应"避免过多引用无用代码"）。

### D3: turn 维度 trace，session 维度上下文
`trace_id` 一个 turn 一个（每条用户消息），`session_id` 始终随行。复盘单次幻觉用 `trace_id` 精确定位；复盘"这个会话反复出问题"用 `session_id` 把多个 trace 串起来。不引入 turn→tool→async 的多级 span（Non-Goal），因为同一 `trace_id` + `event` 字段已足以排序还原链路，避免实现复杂度。

### D4: 日志门面薄封装，渐进迁移
不强制一次性替换所有 `log.Printf`。门面提供 `log.From(ctx).Info().Str("event", "...").Msg(...)` 的 zerolog 链式风格；优先迁移四类关键节点 + 现有带 sessionID 的 agent 日志。无 ctx 的纯启动日志（main.go 的 `listening on ...`）可继续用根 logger 或保留 `log.Printf`，不阻塞本期。

### D5: 事件命名约定
每条日志带稳定的 `event` 字段（点分命名），便于过滤：
- `intent.classify`、`intent.hint_injected`、`remediation.decision`、`fakeack.retry`、`fakeack.honest_fail`
- `tool.start`（含完整 `args`）、`tool.end`、`tool.error`、`tool.zero_exec`
- `window.compress`（前后消息数、fold 数、锚点保留、摘要长度）
- `model.request` / `model.response`（model id、chunks、replyLen、degraded、duration_ms）、`stream.recv_error`
- `turn.start` / `turn.done`（聚合现有那条 turn done 诊断行）

工具入参可能含注入到生图 prompt 的用户内容——按 project.md 的注入防护原则，日志只记录、不回显到任何下发通道；日志文件属内部诊断产物。

## Risks / Trade-offs

- **日志体积**：工具入参不截断 + 压缩快照会显著增大日志量。缓解：分级（关键节点 Info，逐 chunk 等高频细节用 Debug，默认级别 Info 不打 Debug）；本期不做轮转但文档提示运维侧处理。
- **敏感内容落盘**：用户文案/prompt 会进日志文件。可接受——内部工具、无鉴权前提下日志本就内部可见；不外发即可。
- **迁移不彻底**：渐进迁移期间 stderr 与文件可能并存两种格式。缓解：开发期用 stderr 镜像，逐步收敛；本 change 的 tasks 覆盖四类节点 + 现有 sessionID 日志，余量留给后续。

## Migration Plan

1. 落地 `internal/log` 门面 + 配置项 + main 初始化（此时行为=stderr，无回归）。
2. `runTurn` 注入 trace_id。
3. 逐节点接入（意图/remediation → 工具 → 压缩 → 模型元数据），每步可独立验证。
4. 迁移现有 agent/generation/video 关键 `log.Printf`。

## Open Questions

- 日志文件默认路径：建议 `data/logs/app.log`（`data/` 已是项目数据目录）。除非另有偏好，按此默认。
