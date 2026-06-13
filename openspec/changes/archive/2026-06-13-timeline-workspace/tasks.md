# Tasks — Timeline Workspace

按"后端时间下发 → 前端数据模型 → 双视图(网格默认+时间轴可选)→ 编号/拖拽调整 → 验证"推进。

## 1. 后端下发创建时间
- [x] 1.1 `internal/workspace/workspace.go`:`AssetView` 增加 `CreatedAt`(JSON `createdAt`,RFC3339),在 list 与 upload handler 序列化已有的 `AssetRecord.CreatedAt`。
- [x] 1.2 `go test ./internal/workspace/` 通过(序列化不破坏既有行为)。

## 2. 前端数据模型与节点合成
- [x] 2.1 `web/src/lib/types.ts`:`Asset` 增 `createdAt?: string`;`Task` 增 `note?`(agent 对该操作的理解)。
- [x] 2.2 `web/src/lib/timeline.ts`:`buildTimeline` 从 tasks+assets 合成 `TimelineNode[]`(稳定 key:task.id 优先否则 asset.id;state running/failed/done;kind;assets[];task?;parentId;at);按 at 排序(最新在顶)。
- [x] 2.3 一次多产物聚合:同 parentId + kind=cropped/crawled + createdAt 同秒级窗口归一节点;不确定时降级为每产物一节点。
- [x] 2.4 `assetLabels`(图N/视频N 两类各自计数)、`relativeTime`(刚刚/N 分钟前/HH:mm)、`describeToolCall`(工具参数→中文操作理解)。

## 3. 时间轴视图渲染
- [x] 3.1 `web/src/components/workspace/timeline.tsx`:主干线 + 节点圆点 + 最新在顶串联;窄屏单列。
- [x] 3.2 `timeline-node.tsx`:节点头部动作短语(优先 task.note)+ Lucide 图标 + 相对时间;产物体复用 AssetCard;多产物紧凑网格。
- [x] 3.3 活跃节点:进行中 shimmer+进度+accent 脉冲圆点+取消;失败=错误+重试/移除;完成原地转产物(key 连续)。
- [x] 3.4 派生标注:产物有 parentId 且来源可识别时标注"由 图N 加工"。

## 4. 双视图与编号/拖拽调整
- [x] 4.1 视图切换:`workspace-panel.tsx` 加 `view`(grid 默认/timeline)状态 + header 切换按钮 + sessionStorage 记忆;两视图共享数据与操作。
- [x] 4.2 恢复网格视图:重建 `task-card.tsx`(网格态,含 note/进度/取消/重试);新建 `workspace-grid.tsx`(三段网格,无拖拽,图N/视频N 标签)。
- [x] 4.3 编号保留为 LLM 锚点:图片"图N"、视频"视频N"两类各自计数;两视图角标一致;移除拖拽后顺序按创建先后。
- [x] 4.4 移除 `state.order`、`orderedAssets` 拖拽分支、`reorderAsset`、asset-card 拖拽事件;`orderedAssetIds` 改按 createdAt 升序。
- [x] 4.5 `internal/agent/prompt.go` `BuildAssetNumbering` 前缀按 kind 区分"图N"/"视频N"(视频用视频序);prompt 规则 5 同步说明视频前缀。
- [x] 4.6 节点显示 LLM 对操作的理解:`describeToolCall` 经 tool_call→task.note 关联,网格/时间轴节点标题展示。
- [x] 4.7 保留多选/批量切尺寸/批量移除/单图操作/预览/清空/打包。
- [x] 4.8(留后续,不在本提案)一次操作多产物的精确聚合所需后端"批次 id"——本提案用前端启发式聚合,批次 id 作为后续后端工作记录在案。

## 5. 验证与回归
- [x] 5.1 `go build ./...` + `go vet` + `go test ./internal/...` 全绿(含 BuildAssetNumbering 图N/视频N 单测)。
- [x] 5.2 前端 `tsc -b` + `npm run build` 通过。
- [ ] 5.3 端到端(Playwright):默认网格视图;切换时间轴串联(上传→换背景);活跃节点;图N/视频N 角标两视图一致;相对时间;多选+批量仍可用;无拖拽;切换记忆。
- [x] 5.4 回归:既有 capsule、turn 生命周期、markdown 渲染、持久化恢复、"图N/视频N→LLM"沟通不退化。
- [x] 5.5 `openspec validate timeline-workspace --strict` 通过。
