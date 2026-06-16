# Tasks: 丰富诊断日志并引入 trace 链路追踪

## 1. 日志门面与配置
- [x] 1.1 新增 `internal/log` 包：基于 zerolog 的根 logger 初始化（`Init(opts)`），输出目标按"文件 / 文件+stderr 镜像 / 仅 stderr"三态选择（镜像用 `zerolog.MultiLevelWriter`）
- [x] 1.2 在门面提供 `WithTrace(ctx, traceID, sessionID) context.Context`（派生子 logger + `WithContext`）与 `From(ctx) *zerolog.Logger`（`zerolog.Ctx` 取出绑定 `trace_id`/`session_id` 的 logger；无则兜底根 logger）
- [x] 1.3 `go get github.com/rs/zerolog`（仅新增 zerolog + go-colorable 升级，增量极小）
- [x] 1.4 `internal/config` 新增 `LogConfig`（File / Level / MirrorStderr）；默认路径 `data/logs/app.log`，空 File 回退 stderr
- [x] 1.5 `.env.example` 补充 `LOG_FILE` / `LOG_LEVEL` / `LOG_MIRROR_STDERR` 示例与 jq 用法注释
- [x] 1.6 `cmd/server/main.go` 在 config 之后调用 `applog.Init`，失败 `fmt.Errorf` wrap 返回；`defer logCloser.Close()`
- [x] 1.7 表驱动单测：三态输出选择、级别过滤、`WithTrace`/`From` 往返、未配置回退 stderr、nil ctx

## 2. trace 贯穿
- [x] 2.1 `runTurn`（main.go）生成 `trace_id`（`id.New("trace")`）并 `WithTrace` 注入 ctx 后再调 `orch.Handle`；同时打 `turn.start` / `turn.error`
- [x] 2.2 确认 ctx 链路：`runTurn` → `Handle` → `ra.Stream(ctx)` → tool node → `Generation.Start` → `context.WithoutCancel(ctx)` → run goroutine，trace logger 全程保留
- [x] 2.3 单测 `TestWithTraceFromRoundTrip`：异步 goroutine（`WithoutCancel`）内 `From(ctx)` 仍取到触发 turn 的 `trace_id`

## 3. 意图分类与补救决策日志
- [x] 3.1 记录 `intent.classify`（intent 标签 + `hint_injected`）
- [x] 3.2 记录 `fakeack.retry`（attempt、本轮真实执行计数 attempt_exec、intent）
- [x] 3.3 记录 `remediation.decision`（clarify / refuse / honest_fail，含 intent）+ `remediation.missing_output_hint`
- [x] 3.4 迁移 `missing-output complaint`、`fake-exec ack ... honest-fail` 到门面并补 trace 字段（保留原 `log.Printf` 作为 stderr 兼容）

## 4. 工具调用全量入参/结果日志
- [x] 4.1 `toolCallbackHandler` OnStart 记录 `tool.start` + **完整未截断** `args`（前端事件仍截断，UI 关注点）
- [x] 4.2 OnEnd 记录 `tool.end`（摘要）、OnError 记录 `tool.error`（完整错误）
- [x] 4.3 turn 结束真实工具执行为零且 `hint.Whitelisted` 时记录 `tool.zero_exec`（含 confidence）
- [x] 4.4 迁移 `tools.go` 各工具的 `duplicate suppressed` / `missing description` 为 `tool.duplicate_suppressed` / `tool.missing_param`；`invoked` 行由 `tool.start` 覆盖故删除；移除已无用的 `log` import

## 5. context 窗口压缩快照日志
- [x] 5.1 `window.go` 每个压缩周期记录 `CompressionEvent`（前/后消息数、fold 数、摘要长度）
- [x] 5.2 `CompressionEvent.ToolExchangeKept` 复用 `recentHasToolExchange` 记录锚点保留
- [x] 5.3 compressLocked 无 ctx：缓存 `pendingCompressions`，由持有 turn ctx 的 `Handle` 调 `DrainCompressions()` 打 `window.compress`

## 6. 模型请求/响应元数据日志
- [x] 6.1 `streamOnce` 记录 `model.response`：chunk 数、replyLen、耗时（model id 因 streamOnce 不持有 turnModel，改在 `turn.done` 事件携带，见 6.4）
- [x] 6.2 记录 `stream.recv_error`（与原 `log.Printf` 并存）
- [x] 6.3 `chatmodel.go` 记录 `model.degraded`（provider/model/err）
- [x] 6.4 聚合 `turn done ...` 为 `turn.done` 结构化事件（model、compressed、has_tool_exchange、tool_calls、reply_len、capsule、cancelled、intent）；generation 异步任务补 `gen.provider_call` / `gen.provider_ok` / `gen.provider_failed` / `gen.done`

## 7. 验证与收尾
- [x] 7.1 `go build ./...` 与 `go test ./...` 通过；`go vet` / `gofmt -l` 干净
- [x] 7.2 冒烟：通过 `internal/log` 全链路演示（turn.start→intent→tool.start→（WithoutCancel 异步）gen.done→turn.done）落同一文件，`jq 'select(.trace_id=="...")'` 成功拉出完整链路（含跨 goroutine 事件）。注：未跑真实生图对话（需 API key 且 8080 被占用），链路贯穿由该演示 + `TestWithTraceFromRoundTrip` 覆盖
- [x] 7.3 `TestInitNoFileFallsBackToStderr` 确认未配置日志文件时仅 stderr，等价现状
- [x] 7.4 清理冒烟临时文件（demo test、/tmp/*.log、二进制）
