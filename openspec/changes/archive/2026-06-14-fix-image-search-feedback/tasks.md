# Tasks: 修复找图占位回传、重复话术与时间轴归类

## 1. 后端：搜图任务类型与即时占位（Bug #1 / #3 后端侧）
- [x] 1.1 `internal/websearch/service.go`：新增 `TaskAnnouncer` 接口与 `SetAnnouncer`（对齐 `generation.Service`），并把 `StartImageSearch` 的 `InsertTask` 的 `Kind` 由 `"generate"` 改为 `"search"`。
- [x] 1.2 `internal/websearch/service.go`：在 `StartImageSearch` 入队后调用 `announce.AnnounceTask`，携带 `kind="search"` 与归一化后的张数 `p.Limit`（1/3/6/12）。另给 searched 资产盖 `ParentID=taskID` 作为批次聚合锚点。
- [x] 1.3 `cmd/server/main.go`：扩展 `TaskAnnouncer.AnnounceTask` 签名携带 `count`（generation/video 传 1），`taskAnnouncer.AnnounceTask` 在 `task_created` 载荷中加入 `count`；为 `webSearchSvc` 接线 `SetAnnouncer(announcer)`。
- [x] 1.4 `internal/agent/agent.go`：`toolKind("search_images")` 返回 `"search"`。

## 2. 后端：消除重复确认话术（Bug #2）
- [x] 2.1 采用 design D3 方案 A：`search_images` 的 standalone marshal 返回空串（`asyncMarshal("")`）。原因：search_images 属 `ToolReturnDirectly`，非空固定话术会拼到模型同轮自述确认句之后形成两遍；返回空串符合系统提示契约"工具返回空内容表示任务已提交"，只留模型一句确认。
- [x] 2.2 `await_result=true` 串联不受影响：await 路径仍返回完整 JSON（`asyncMarshal` 在 `asset_id` 非空时输出 JSON），asset_id 可继续链式传递；空串仅作用于 standalone 路径。

## 3. 前端：任务类型、时间轴归类与搜图聚合（Bug #3 前端侧）
- [x] 3.1 `web/src/lib/types.ts`：`TaskKind` 增加 `"search"`，`Task` 增加 `count?`。
- [x] 3.2 `web/src/lib/timeline.ts`：`TimelineNode["kind"]` 增 `"search"`；`kindFromTask("search") → "search"`；`kindFromAsset("searched") → "search"`；以搜图任务 id（资产 `parentId`）为锚点把整批收敛为一个节点；运行中以 nowMs 浮到活跃端并标 `running`。
- [x] 3.3 `web/src/components/workspace/timeline-node.tsx`：`KIND_META.search = { label: "搜图", Icon: Search }`，多产物显示 ×N。

## 4. 前端：按张数占位与逐张回填（Bug #1 前端侧）
- [x] 4.1 `web/src/store/controller.ts`：`task_created{kind:"search", count:N}` 经 `ensureTaskPlaceholder` 带 count 建占位并订阅 SSE；`SearchBatchBody` 渲染"已到资产 + (count−已到) 个 shimmer 占位槽"。
- [x] 4.2 `task_progress{asset_id}` 沿用 `refreshWorkspace` 全量刷新逐张回填；`refreshWorkspace` 改为合并保留 `count`/`note`，避免刷新清空客户端占位计数。

## 5. 验证
- [x] 5.1 `go test ./...` 与 `go vet ./...` 全绿；新增 `internal/websearch/service_test.go`（断言 `Kind="search"`、announce `kind/count`、资产 `ParentID=taskID`）、`toolresult_test.go` 增 `search_images→search`。
- [~] 5.2 前端 `tsc -b && vite build` 通过。说明：当前 `web/` 无前端测试框架（无 vitest/jest），未新增时间轴快照单测；以类型检查 + 构建作为门禁。
- [ ] 5.3 手测（非 8080 诊断实例）：找 1/3 张图——占位数=请求数、逐张回填、确认仅一句、时间轴显示【搜图】批次。**未在本环境执行**（找图依赖外网 Bing/搜狗抓取，沙箱内不稳定），留待联网环境人工验收。
- [x] 5.4 `openspec validate fix-image-search-feedback --strict` 通过。
