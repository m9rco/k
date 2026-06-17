## Context
平台适配 `adapt_to_platform` 在产出非空宣发分析报告后、发起 AI 重绘前会**门控**等待前端的摘要确认(零额度纠偏点)。门控由三段构成:
- 后端 `gateSummaryConfirm`(`tools.go:739`)调用注入的 `AwaitSummaryConfirm` 钩子;
- 钩子(`agent.go:584`)先发 `summary_confirm` 出站事件,再调 `awaitSummaryConfirm`(`agent.go:310`)阻塞;
- `awaitSummaryConfirm` 用一个 `chan summaryConfirm` 等前端回传,带 **8 秒安全超时**;前端 3 秒倒计时归零或用户提交时通过 `summary_confirm` 入站消息经 `DeliverSummaryConfirm` 投递。

前端侧(`controller.ts`):`onSummaryConfirm` 起 3 秒倒计时,`editSummary` 进入编辑态并**仅清掉前端定时器**,`submitSummaryConfirm` 发送 `summary_confirm`。

## Goals / Non-Goals
- Goals:
  - 用户进入编辑态后,门控**不因超时自动放行**,等待显式确认,使编辑版可靠回写 `vision_reports`。
  - 编辑态提供「重新分析」:同一组参考图重跑 grok,流式回填编辑器,门控继续等待。
  - 全程加法式、向后兼容:旧客户端不发新消息,行为不退化(倒计时默认 + 安全超时兜底仍在)。
- Non-Goals:
  - 不改变缓存命中也进入确认态的既有语义(本变更只调整等待/超时与新增重新分析)。
  - 不改 `vision_reports` 表结构与 cache key 计算。
  - 不引入多并发摘要确认(适配仍是单图组单门控)。

## Decisions

### D1. 编辑态取消后端安全超时(修复问题 1+3 的根因)
新增入站消息 `summary_editing { cacheKey }`。前端在 `editSummary` 时发送。后端新增 `DeliverSummaryEditing(sessionID, cacheKey)`,向门控等待协程投递一个「进入编辑」信号,使 `awaitSummaryConfirm` **停止安全超时计时**(转为只等 `summary_confirm` 或 `ctx.Done()`)。

实现:`awaitSummaryConfirm` 的 `select` 增加一个 `editing` 通道分支;收到编辑信号后将 `timer` 停掉并进入「仅等确认/取消」的二段 select。这样用户编辑多久都不会被放行,提交时 channel 仍注册 → `DeliverSummaryConfirm` 命中 → `gateSummaryConfirm` 的 `edited && final != report` 回写分支执行。

- Alternatives considered:
  - *延长安全超时到 60s*:治标不治本,慢用户仍可能被截断,且拖慢真实超时兜底;放弃。
  - *前端编辑时周期性 heartbeat 续期*:复杂、易抖动;一个一次性「编辑开始」信号更简单可靠。

> 兜底不变:用户**未进入编辑**时,8 秒安全超时与前端 3 秒倒计时默认仍生效——旧客户端、断线客户端行为不退化。

### D2. 门控期重新分析(问题 2)
新增入站消息 `summary_reanalyze { cacheKey }`。后端新增 `DeliverSummaryReanalyze(sessionID, cacheKey)`。

门控需在等待期持有重跑分析所需的上下文。`visionThemeReport` 已经在本地算出参考图的已发布**公网 URL 组**(`urls`,实时分析路径)或可由参考 md5 重建。决定:`gateSummaryConfirm` 接收一个 `reanalyze func(ctx) (string, error)` 闭包(由 `visionThemeReport` 构造,捕获已发布 URL 组 + `VisionAnalyzer` + `notifyAnalysis`),门控等待协程收到重新分析信号时:
1. 调 `notifyAnalysis` 起一个**新的分析块**(done=false)流式输出新报告;
2. 完成后把新报告作为门控的「当前报告」更新(后续确认/超时默认都以新报告为基准),并重新 `summary_confirm` 出站事件,前端据此对新块重开确认窗口;
3. 重新分析失败 → `notifyAnalysis` 收尾提示,保留原报告,门控继续等原确认。

缓存命中路径若用户要重新分析:此时 `urls` 未必在手,但 cacheKey 对应的参考 md5 可从 `refs` 重新发布(复用 `UploadIfAbsent` 的 md5 去重,零额外成本)。`reanalyze` 闭包统一封装「(必要时)发布 → 分析」。

- Alternatives considered:
  - *重新分析另起一个 WS 轮*:会与门控状态割裂,且 `adapt_to_platform` 工具调用仍阻塞在门控里;放弃。在既有门控协程内重跑、复用 `analysis` 出站事件最自然。

### D3. 前端编辑器回填与重新分析 loading
- `editSummary`:在原有清前端定时器基础上,通过 WS 发 `summary_editing`(携带 `pendingConfirmRef.cacheKey`)。
- 新增 `reanalyzeSummary(id)`:发 `summary_reanalyze`,并把分析块置 `reanalyzing` loading 态。
- 重新分析的流式 `message{analysis:true}` 增量沿用 `onAnalysisDelta`;`done` 后新的 `summary_confirm` 重开确认窗口。编辑器在重新分析完成后用新报告文本重新预填(`AnalysisBlock` 已有 `useEffect([editing, text])` 同步 draft,需扩展为也随重新分析后的 text 刷新)。

## Risks / Trade-offs
- **门控等待无限期(编辑态)** → 若用户既不提交也不取消,工具调用会一直阻塞。缓解:`ctx.Done()` 分支仍在——用户发任何新消息触发 `cancel_turn` 即解除;且这是用户已显式介入的主动态,不同于断线。
- **重新分析期间并发确认** → 重新分析进行中用户又点确认。缓解:门控协程串行处理信号(单 goroutine select 循环),重新分析以阻塞段完成后再回到等待,或以「重新分析中忽略确认」简单处理(前端在 loading 态禁用确认/编辑提交)。
- **cacheKey 路由** → 重新分析/编辑信号必须携带与门控注册一致的 cacheKey;前端从 `pendingConfirmRef` 取,与既有 `summary_confirm` 同源,保持一致。

## Migration Plan
纯加法式协议演进,无数据迁移。后端先上线(识别新消息;旧前端不受影响),前端随后发布。回滚:前端回退即停用新按钮/信号,后端新分支对无消息的客户端是 no-op。

## Open Questions
- 重新分析是否限次(防滥用)?倾向:不限次但每次都流式可见、由用户主动触发,成本可控;如需可后续加每门控最多 N 次。
