# gate-adaptation-on-editable-summary

## Why

今天 `adapt_to_platform` 的视觉宣发分析（grok-4-fast）是「分析完即用」：报告流式输出到折叠面板后，工具**立即**拿它作为主题约束开始 AI 重绘，用户没有任何介入机会。但这份摘要直接决定适配产物「必须保留什么、主题是什么」——一旦 grok 漏掉关键卖点或把主体描述错了，整批尺寸都会跟着错，用户只能等全部重绘完才发现，再推倒重来，既慢又费额度。

用户需要一个轻量的「过目即改」机会：分析出报告后给一个**3 秒确认窗口**，用户可按既有格式就地修改摘要；3 秒内不动则默认采用 grok 的版本继续适配。这样把纠偏点提前到「重绘之前、零额度消耗」的位置，且不打断「不修改就自动往下走」的顺滑感。

## What Changes

- **可编辑分析面板 + 倒计时**：分析报告流式输出完毕、自动折叠后，面板进入一个 3 秒可编辑确认态——展示当前摘要、一个倒计时、一个「编辑」入口。用户可就地按既有 4 行格式（核心主题/IP/游戏名、主体、宣发意图、必须保留）修改文本。
- **适配门控（等待确认后才开始）**：`adapt_to_platform` 在产出报告后、AI 重绘前**暂停**，等待前端回传「确认/编辑后的摘要」或「倒计时到期默认采用」信号后才继续。期间不发起任何重绘请求。
- **倒计时到期默认提交**：前端 3 秒倒计时归零且用户未编辑，自动以 grok 原始报告作为确认值回传，适配无缝继续——用户无需任何点击。
- **用户编辑回写缓存复用**：用户改过的摘要 SHALL 回写按 md5/图片集指纹键的 `vision_reports` 缓存，覆盖该 key 原报告。同批图后续适配/改图复用**编辑版**，不再被 grok 原版覆盖。
- **新增确认入站协议**：WebSocket 入站新增一类「摘要确认」消息（携带最终摘要文本与图片集 cache key），后端据此解除门控、续接适配。以加法式向后兼容方式引入，旧客户端不识别即忽略。
- **缓存命中也进入确认窗口**：已写入 `vision_reports`（预热版/此前的编辑版）的报告在本次适配前同样弹出 3 秒可编辑确认态；用户改动后回写**覆盖**该 key，下次复用拿到最新版。只有「报告为空、COS/vision 不可用导致无报告」这类**无报告可确认**的降级路径才跳过确认、直接按无主题约束适配。

## Impact

- Affected specs: `marketing-analysis`（新增确认门控与编辑回写）、`realtime-transport`（新增摘要确认入站协议）、`frontend-experience`（可编辑分析面板 + 倒计时）
- Affected code:
  - `internal/agent/tools.go` — `visionThemeReport` 在产出报告后增加确认等待点；接收编辑后的摘要并回写 `InsertVisionReport`
  - `internal/agent/agent.go` — `NotifyAnalysis` 之后挂接确认通道；门控的 await/timeout 编排
  - `internal/transport/event.go` / `ws.go` — 新增摘要确认 `Inbound` 类型与（可选）出站「进入确认态」事件
  - `cmd/server/main.go` — 入站 handler 分发摘要确认到 orchestration
  - `web/src/store/controller.ts`、`web/src/store/types.ts`、`web/src/components/chat/*` — 分析块进入可编辑确认态、倒计时、回传确认
- 缓存语义变化：`vision_reports` 中某 key 的值在用户编辑后会被覆盖为编辑版（此前只由 grok 写入）。进程重启后缓存失效，重新分析（可接受，沿用既有约定）。
