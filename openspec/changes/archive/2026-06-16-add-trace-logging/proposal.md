# 丰富诊断日志并引入 trace 链路追踪

## Why

当前全部日志用标准库 `log.Printf` 打到 **stderr，无落地文件、无结构、无追踪标识**。排查两类高频问题非常困难：

1. **模型幻觉**：模型凭空"确认"已完成、引用不存在的产物。根因常在 context window 压缩切掉了 tool-exchange 锚点、或注入的提示/编号上下文被截断，但当前日志看不到压缩前后的窗口快照，无法复盘模型当时究竟看到了什么。
2. **工具不执行**：模型在 prose 里假装调用工具（fake-exec ack）却没有真正触发 tool node。当前虽有零散的 `edit_image: invoked ...`、`fake-exec ack detected` 等日志，但散落在 stderr、按时间穿插多个会话、且工具入参被 `truncate` 截断，难以还原"模型该调没调、调了什么参数、为什么 remediation 走了哪个分支"的完整链路。

更现实的痛点：服务跑起来后日志只在终端滚动，进程一停就没了，没法事后 grep/jq 定位某次具体对话。

## What Changes

- **引入双层 trace 标识**：每次用户消息（一个 turn）生成一个 `trace_id`，并始终携带 `session_id` 作为上下文维度。`trace_id` 通过 `context.Context` 贯穿 turn 的全过程，并跨异步生成 goroutine（已有 `context.WithoutCancel` 保留 ctx 值的机制）继续携带，使一次生图/生视频长任务的日志能回连到触发它的那一轮对话。
- **结构化日志写文件**：新增一个轻量日志门面，底座选用高性能日志库 **zerolog**（`github.com/rs/zerolog`，零分配、JSON 原生、额外依赖几乎为零），每行一条 JSON（含 `ts/level/trace_id/session_id/turn/event/...`），写入可配置的日志文件，并支持可选同时镜像到 stderr（开发期）。日志文件路径、级别、是否镜像 stderr 通过配置/环境变量控制。zerolog 自带 `WithContext`/`Ctx` 的 context 携带能力，trace logger 直接走其原生机制，避免自造样板代码。
- **丰富四类关键节点的日志**（均带 trace_id/session_id）：
  1. **意图分类与 remediation 决策**：`ClassifyIntent` 结果、是否注入意图提示/补救提示、fake-exec 重试、honest-fail/clarify/refuse 走了哪个分支。
  2. **工具调用全量入参/结果**：tool `OnStart` 的完整 `arguments`（不截断）、`OnEnd` 的结果摘要、`OnError` 的完整错误，外加"模型本轮应调用工具但 tracker 记录为零次"的显式判定。
  3. **context window 压缩前后快照**：压缩触发时记录压缩前/后消息数、折叠批次数、是否保留 tool-exchange 锚点、摘要长度，以及压缩后窗口是否仍含完整 tool exchange。
  4. **模型请求/响应元数据**：每次模型调用的 model id、流式 chunk 数、reply 长度、是否降级 one-shot、耗时。
- **改造既有 `log.Printf` 调用点**：把 agent / generation / video / transport 里现有的关键日志迁移到新门面，补齐 trace 字段，保留信息量的同时获得结构化与落地能力。

## Impact

- Affected specs: **新增** `diagnostic-logging`（横切能力）；conversation-orchestration、image-generation 等不改变行为契约，仅在实现层接入日志门面，故不产生 spec delta。
- Affected code:
  - 新增 `internal/log`（或 `internal/observ`）日志门面包：封装 zerolog 根 logger 初始化与 trace context helper。
  - 新增依赖 `github.com/rs/zerolog`（高性能、零分配；额外间接依赖极少）。
  - `internal/agent/agent.go`、`intent.go`、`tools.go`、`window.go`、`chatmodel.go`、`stream.go`：接入门面 + 补 trace 字段 + 新增四类节点日志。
  - `internal/generation/service.go`、`internal/video/service.go`、`internal/video/provider.go`：异步任务日志携带 trace。
  - `cmd/server/main.go`：启动时初始化日志门面（读配置，打开日志文件），`runTurn` 生成并注入 `trace_id`。
  - `internal/config`：新增日志相关配置项（文件路径、级别、stderr 镜像开关）。
  - `.env.example`：补充日志配置示例。
- 非破坏性：日志为旁路增强，默认行为（除日志去向外）不变；未配置日志文件时回退到 stderr，与现状一致。
