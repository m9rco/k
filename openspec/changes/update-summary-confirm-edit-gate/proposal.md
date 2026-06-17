# Change: 宣发摘要确认门控——编辑暂停超时、重新分析、修复编辑回写

## Why
当前「可编辑宣发摘要确认」门控存在三个互相关联的问题:

1. **编辑后仍会被超时自动放行**:用户点「编辑」只暂停了**前端** 3 秒倒计时,但后端 `awaitSummaryConfirm` 的 8 秒安全超时(`internal/agent/agent.go:326`)仍在独立计时。用户编辑一段 4 行摘要通常超过 8 秒,门控因此超时返回 `(original, false)`,以 grok 原报告继续适配——用户的编辑被丢弃。
2. **编辑后的宣发意图没有回写 db**:上述超时一旦触发,门控注销了等待 channel;待用户提交修改时 `DeliverSummaryConfirm` 已找不到 channel → no-op,`gateSummaryConfirm` 的回写分支(`tools.go:749`)根本不会执行。这就是「修改过后的宣发意图似乎没有回写 db」的根因。
3. **缺少「重新分析」入口**:用户进入编辑态后,只能手改文本,无法让 grok 基于同一组参考图重新产出一份分析作为新起点。

## What Changes
- **编辑暂停后端超时**:新增**编辑态信号**入站消息。前端进入编辑态时通知后端,后端据此**取消安全超时**,改为无限等待用户的显式确认(或本轮被 `cancel_turn` 中断)。倒计时结束(用户未编辑)仍按原逻辑自动确认。
- **修复编辑回写**:消除问题 2 的根因——编辑态下门控不再因 8 秒超时提前放行,用户提交时 channel 仍在,回写分支正常执行,编辑版覆盖写入 `vision_reports`。补充回归测试覆盖「编辑耗时 > 旧超时仍能回写」。
- **重新分析按钮**:编辑态新增「重新分析」入口。新增**重新分析**入站消息;后端用本次适配的**同一组参考图**重跑 `grok-4-fast` 视觉分析,把新报告**流式**回传到分析块并预填进编辑器,门控继续等待;用户可在新报告基础上再编辑或直接确认。
- **门控记住参考图**:门控等待期间后端持有本次适配的参考图标识(已发布的公网 URL 组),供重新分析复用,避免再次发布。

## Impact
- Affected specs:
  - `marketing-analysis`(MODIFIED:门控等待语义、确认前重绘门控;ADDED:门控期重新分析)
  - `realtime-transport`(ADDED:宣发摘要编辑态入站协议、宣发摘要重新分析入站协议)
  - `frontend-experience`(MODIFIED:可编辑宣发摘要确认面板——编辑暂停、重新分析按钮)
- Affected code:
  - `internal/agent/agent.go`:`awaitSummaryConfirm` 超时改为可被编辑信号取消;新增 `DeliverSummaryEditing` / `DeliverSummaryReanalyze`;门控持有参考 URL 组
  - `internal/agent/tools.go`:`gateSummaryConfirm` / `visionThemeReport` 传递参考 URL 组给门控
  - `cmd/server/main.go`:新增 `summary_editing` / `summary_reanalyze` 入站分支
  - `internal/transport/ws.go`:`Inbound` 新增编辑/重新分析消息字段
  - `internal/transport/event.go`:复用既有 `analysis` message 事件回传重新分析的流式报告
  - `web/src/store/controller.ts`:`editSummary` 发送编辑态信号;新增 `reanalyzeSummary`;重新分析的流式报告回填编辑器
  - `web/src/components/chat/analysis-block.tsx`:编辑态新增「重新分析」按钮与 loading 态
  - 测试:`internal/agent/summary_gate_test.go` 等
