## 1. 后端：门控等待与超时
- [ ] 1.1 `agent.go`：`summaryConfirm` 等待结构新增「编辑态」与「重新分析」信号通道（或扩展 `summaryConfirms` 注册值为带多通道的结构体）
- [ ] 1.2 `awaitSummaryConfirm`：收到编辑态信号后停止 `safetyTimeout`，转为只等 `summary_confirm` / `ctx.Done()`（二段 select）
- [ ] 1.3 新增 `DeliverSummaryEditing(sessionID, cacheKey)`：向门控投递编辑态信号；无等待时 no-op
- [ ] 1.4 单测：编辑信号到达后，超过原 8s 仍能在后续 confirm 时返回编辑版且 `edited=true`（覆盖问题 1+3 根因）

## 2. 后端：门控期重新分析
- [ ] 2.1 `tools.go`：`visionThemeReport` 构造 `reanalyze func(ctx) (string, error)` 闭包，捕获已发布 URL 组 / 参考 md5、`VisionAnalyzer`、`notifyAnalysis`
- [ ] 2.2 `gateSummaryConfirm`：接收 `reanalyze` 闭包；门控等待协程收到重新分析信号时流式跑新分析、用新报告更新「当前报告」并重发 `summary_confirm`
- [ ] 2.3 新增 `DeliverSummaryReanalyze(sessionID, cacheKey)`：向门控投递重新分析信号；无等待时 no-op
- [ ] 2.4 重新分析失败：保留当前报告、`notifyAnalysis` 收尾提示、门控继续等待
- [ ] 2.5 单测：重新分析信号触发重跑、新报告成为门控基准；失败时不退化

## 3. 传输协议
- [ ] 3.1 `ws.go`：`Inbound` 复用 `CacheKey` 承载 `summary_editing` / `summary_reanalyze`（无需新字段，仅新增 `Type` 取值）
- [ ] 3.2 `main.go`：新增 `case "summary_editing"` → `DeliverSummaryEditing`；`case "summary_reanalyze"` → `DeliverSummaryReanalyze`

## 4. 前端
- [ ] 4.1 `controller.ts`：`editSummary` 在清前端定时器后，经 WS 发送 `summary_editing`（从 `pendingConfirmRef.cacheKey` 取 key）
- [ ] 4.2 `controller.ts`：新增 `reanalyzeSummary(id)` 发送 `summary_reanalyze`，并把分析块置 `reanalyzing` 加载态
- [ ] 4.3 `controller.ts`：重新分析的 `message{analysis:true}` 流式增量正确进入对应块；新 `summary_confirm` 重开确认窗口并刷新编辑器预填文本
- [ ] 4.4 `analysis-block.tsx`：编辑态新增「重新分析」按钮 + loading 态；loading 时禁用确认/提交；`useEffect` 同步重新分析后的最新 text 到 draft
- [ ] 4.5 重新构建前端产物（`web/static/assets/*`）

## 5. 验证
- [ ] 5.1 `go test ./...` 全绿（重点 `internal/agent`、`internal/transport`）
- [ ] 5.2 手动 E2E：实时分析 → 点编辑 → 停留 >8s → 提交 → 确认编辑版被采用且回写（重启后同图复用编辑版）
- [ ] 5.3 手动 E2E：编辑态点「重新分析」→ 新报告流式回填编辑器 → 编辑/确认正常
- [ ] 5.4 回归：未编辑路径 3s 倒计时默认、旧客户端无新消息时安全超时兜底仍生效
- [ ] 5.5 `openspec validate update-summary-confirm-edit-gate --strict`
