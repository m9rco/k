## Context
找图（`search_images`）是一次"一任务 → N 张产物"的异步操作，但它没有像生图/生视频那样接入两条既有基础设施：

- **即时占位通道**：`generation`/`video`/`text-to-image` 三个 Service 都实现了 `TaskAnnouncer`，由 `cmd/server/main.go` 的 `taskAnnouncer` 在建任务瞬间经 WS 广播 `task_created{task_id, kind}`，前端 `controller.ts` 收到后 `ensureTaskPlaceholder` 立刻占位并订阅该任务 SSE（见 `frontend-experience` 规格「长任务即时占位反馈」）。`websearch.Service` 没有 announcer，只向 per-task broker `Publish(EventTaskQueued)`，而此时前端尚未订阅该 task，事件无人接收。
- **任务类型贯通**：`toolKind`/`AnnounceTask` 的 `kind` 与前端 `TaskKind`、时间轴 `kindFromTask/kindFromAsset` 一致映射出节点类型。找图全程复用了 `generate`，导致归类错误。

此外 `search_images` 仍在 `AsyncTaskTools()`（`ToolReturnDirectly`）集合内，其固定话术会作为最终消息直送用户，与模型同轮自发的确认句叠加成两遍。

## Goals / Non-Goals
- Goals：
  - 找图建任务瞬间即按"请求张数 N"占位，逐张回填，无需手动刷新。
  - 同一找图轮仅一句确认话术。
  - 找图产物在时间轴聚合为单个【搜图】批次节点。
- Non-Goals：
  - 不改动找图的搜索源（搜狗/Bing）、下载与去重逻辑。
  - 不改网格视图既有按状态分段的呈现。
  - 不引入新的存储字段或数据库迁移（资产 `kind="searched"` 已存在）。

## Decisions

### D1：引入端到端 `search` 任务类型
- 后端找图任务 `InsertTask(Kind: "search")`（替换现有 `"generate"`）；`toolKind("search_images") → "search"`；`AnnounceTask` 传 `kind="search"`。
- 前端 `TaskKind` 增加 `"search"`；`kindFromTask("search") → "search"`；`kindFromAsset("searched") → "search"`；时间轴节点 kind 增 `"search"`，`KIND_META.search = { label: "搜图", Icon: Search }`。
- 备选：沿用 `generate` 仅在前端按 `Intent` 前缀（`search_images:`）区分。否决——脆弱且与既有 kind 贯通模式不一致。

### D2：为 `websearch.Service` 接入 `TaskAnnouncer`，并在 announce 中携带请求张数 `count`
- 复用既有 `taskAnnouncer`，将 `AnnounceTask(sessionID, taskID, kind)` 扩展为携带 `count`（找图归一化后的 `Limit`，即 1/3/6/12）。其余调用方（generation/video）传 `count=1` 或缺省，语义不变。
- 前端收到 `task_created{kind:"search", count:N}` 时，为该单一任务渲染 **N 个占位槽**；随后每个 `task_progress{asset_id}` 到达即回填一槽（`refreshWorkspace` 已是按资产全量刷新，占位与产物在时间轴同一【搜图】节点内聚合呈现）。
- 备选：不带 count，仅占一个槽。否决——不满足"识别到找几张，占位几张"。

### D3：消除重复确认话术（单一来源）
- 找图轮确认话术只保留一处。推荐：保留 `search_images` 在 `ToolReturnDirectly` 内、由固定话术作为唯一确认来源；在系统提示层面让模型对找图**不再额外自述确认句**（找图触发即直接调用工具）。
- 备选 A：将 `search_images` 移出 `AsyncTaskTools()`，改用 `asyncMarshal` 并依赖模型单句确认。需评估对多任务串联（`await_result`）的影响。
- 最终在实现期二选一并以测试固化"同一轮仅一句确认"。

### D4：时间轴搜图批次聚合
- 在 `buildTimeline` 中把 `search` 视为可聚合类型（与 `crop`/`crawl` 同列），按 `源(parentId 缺省为搜图任务)+kind+同秒/同任务` 归并，使 N 张搜图产物收敛为一个【搜图 ×N】节点；运行中表现为该批次的 N 个占位槽。

## Risks / Trade-offs
- `AnnounceTask` 签名变更影响 generation/video/t2i 三处调用 → 以新增 `count` 参数（默认 1）最小化改动，并跑 `go test ./...` 验证。
- D3 两方案择一需防回归：以单测断言"找图轮 `message` 事件 + ReturnDirectly 文本合计仅一句确认"。
- 聚合键若设计不当可能把不同批次找图并到一个节点 → 以搜图任务 id 作为聚合锚点，避免跨批次误并。

## Migration Plan
- 纯行为修复，无数据迁移。历史以 `Kind="generate"` 落库的旧找图任务在时间轴仍显示为【生成/编辑】（仅影响历史会话，可接受）；新找图任务按 `search` 归类。
- 回滚：还原 `Kind`、`toolKind`、前端映射与 announcer 接线即可。

## Open Questions
- D3 最终取哪一方案，由实现期结合 `await_result` 串联场景的回归测试确定。
