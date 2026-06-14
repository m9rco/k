# Change: 修复找图（search_images）的占位回传、重复话术与时间轴归类

## Why
找图功能存在三处与生图/生视频不一致的体验缺陷，均源于 `search_images` 没有复用既有的"长任务即时占位 + 任务类型贯通"机制：

1. **占位不实时**：找图建任务后不刷新看不到结果，需手动刷新；刷新后也只有一个笼统 loading。根因是 `websearch.Service` 未接 `TaskAnnouncer`（不广播 `task_created`），且工具 marshal 返回纯文本使 `tool_result` 不带 `task_id`，前端无从建占位/订阅进度。
2. **确认话术重复两遍**：`search_images` 属于 `ToolReturnDirectly`，其固定话术作为最终消息直送用户，而模型本轮又流式说了一句同样的确认，导致"好的，正在搜索并下载相关图片，结果会出现在左侧工作区。"出现两遍。
3. **时间轴归类错误**：找到的图在时间轴里标为【生成/编辑】且各自成节点，预期应作为同一【搜图】批次。根因是后端任务 `Kind="generate"`、`toolKind→"generate"`，前端缺 `searched/search` 分支且未对搜图产物做批次聚合。

## What Changes
- 引入贯通前后端的 **`search` 任务类型**：后端找图任务 `Kind` 与 `toolKind`、`AnnounceTask` 的 kind、前端 `TaskKind` 与时间轴节点 kind 统一为 `search`（展示标签【搜图】）。
- 为 `websearch.Service` 接入 **`TaskAnnouncer`**，在创建找图任务的瞬间经对话通道广播 `task_created`（携带 `kind=search` 与请求张数 `count`），前端据此**立即按张数占位**并订阅进度；每张图下载完成经 `task_progress(asset_id)` 即时回填，无需手动刷新。
- 修复**重复确认话术**：同一找图轮**有且仅有一句**确认话术。
- 时间轴中将**搜图产物聚合为单个【搜图】批次节点**（与切尺寸/爬取的聚合一致），不再散落为多个【生成/编辑】节点。

## Impact
- Affected specs: `web-search`、`frontend-experience`、`asset-workspace`
- Affected code:
  - 后端：`internal/websearch/service.go`（新增 announcer、任务 `Kind="search"`、announce 携带 count）、`cmd/server/main.go`（接线 announcer，扩展 `AnnounceTask` 携带 count）、`internal/agent/tools.go`（`search_images` marshal/ReturnDirectly 去重）、`internal/agent/agent.go`（`toolKind("search_images")→"search"`）、`internal/transport/event.go`（`task_created` 载荷扩展 count，按需）
  - 前端：`web/src/lib/types.ts`（`TaskKind` 增 `search`）、`web/src/lib/timeline.ts`（`kindFromAsset/"searched"`、`kindFromTask/"search"`、搜图聚合）、`web/src/components/workspace/timeline-node.tsx`（`KIND_META` 增【搜图】）、`web/src/store/controller.ts`（按 count 建占位、search 回填）
