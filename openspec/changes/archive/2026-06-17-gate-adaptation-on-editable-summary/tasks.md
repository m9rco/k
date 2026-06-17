# Tasks

## 1. Transport：摘要确认入站协议
- [x] 1.1 在 `internal/transport/ws.go` 的 `Inbound` 增加摘要确认类型（`Type: "summary_confirm"`），携带字段：`cacheKey`（图片集指纹）、`summary`（最终摘要文本）、`edited`（bool）。
- [x] 1.2 在 `internal/transport/event.go` 增加出站「进入确认态」事件 `EventSummaryConfirm`（携带 `cacheKey`），供前端原样回传；不识别该事件类型的旧客户端忽略。
- [x] 1.3 在 `internal/transport/transport_test.go` 补充入站解析/向后兼容测试（`TestInboundSummaryConfirmParsing`，含 edited 默认 false）。

## 2. 后端门控：工具内阻塞等待 confirm
- [x] 2.1 在 orchestration（`internal/agent/agent.go`）维护 per-session 的 confirm 通道注册表（`summaryConfirms`，按 `sessionID|cacheKey` 寻址），并在 `cmd/server/main.go` 入站 handler 的 `summary_confirm` 分支调用 `DeliverSummaryConfirm` 投递。
- [x] 2.2 在 `ToolDeps` 增加确认门控钩子 `AwaitSummaryConfirm func(ctx, cacheKey, original string) (final string, edited bool)`，由 `agent.go` 注入；hub 为 nil（测试/无传输）时不注入，钩子为 nil 直接放行保持降级。
- [x] 2.3 在 `internal/agent/tools.go` 的 `visionThemeReport`：实时分析产出非空报告后、return 前，调用门控钩子等待 confirm；`awaitSummaryConfirm` 内 `select` 三路兜底：收到值 / `ctx` 取消（用户中断当前轮）/ 服务端安全上限超时（8s）→ 超时按 grok 原版续接。
- [x] 2.4 门控仅在「实时分析且报告非空」生效；缓存命中、报告为空、COS/vision 不可用路径**不**进入门控（保持既有 early-return）。
- [x] 2.5 确认携带 `edited=true` 且文本与原报告不同时，`InsertVisionReport(cacheKey, final)` 覆盖原报告；未编辑不重复写。
- [x] 2.6 进入门控前下发 `EventSummaryConfirm`（带 `cacheKey`），使前端开始倒计时并知道回传用哪个 key。

## 3. 前端：可编辑分析面板 + 倒计时
- [x] 3.1 `web/src/store/types.ts` 扩展 `analysis` 块：增加 `cacheKey`、`confirming`、`secondsLeft`、`editing`、`confirmed` 等态。
- [x] 3.2 `web/src/store/controller.ts`：收到 `summary_confirm` 事件后，对刚 `done` 的实时分析块进入确认态并启动 3s 倒计时；倒计时归零未编辑则自动发送 `summary_confirm`（原文、`edited=false`）。
- [x] 3.3 新增 `web/src/components/chat/analysis-block.tsx`（参考 `reasoning-block.tsx` / `capsule-bubble.tsx`）渲染倒计时与「编辑」入口；点编辑暂停倒计时、进入就地多行编辑（按 4 行格式），提交发送 `summary_confirm`（编辑文本、`edited=true`）。
- [x] 3.4 提交后禁用面板（`confirmed` 幂等，单块只确认一次）；缓存命中/复用提示的分析块不收到事件、不进入确认态。
- [x] 3.5 遵循 CLAUDE.md UI 规范（zinc 调色、`transition-all duration-200 ease-out`、克制留白），倒计时与编辑态视觉与既有折叠面板一致。

## 4. 验证
- [x] 4.1 后端：表驱动单测 `summary_gate_test.go` 覆盖门控（确认/编辑回写、ctx 取消默认原版、空值回退原版、无等待者 no-op）。
- [x] 4.2 `go build ./...` 与 `go vet ./...` 通过；前端 `npm run build`（embed 产物）通过。
- [x] 4.3 手动联调：实时分析→3s 不动自动续接；编辑→适配用编辑版且同批图二次适配复用编辑版、不再弹确认。（联调发现「倒计时自动提交回传空 cacheKey → 后端门控错过 deliver、干等满 60s 安全超时」的批处理 bug，已修复：前端改用 ref 同步持有 cacheKey/text，安全超时降至 8s 作二次兜底。）
- [x] 4.4 `openspec validate gate-adaptation-on-editable-summary --strict` 通过。
