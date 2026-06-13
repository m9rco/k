# Design — Timeline Workspace

## Context

现状(审计证据):
- `workspace-panel.tsx`:三段 `Stage`(进行中/已完成/失败),每段 `grid auto-fill minmax(150px,1fr)`。已完成资产经 `orderedAssets(map, state.order)` 排序,支持拖拽重排(`state.order` 前端态,刷新丢失,回退后端 `created_at DESC`)。
- `asset-card.tsx`:方形卡,左上"图N"角标(仅已完成段传 `index`)+ 类型标签,左下尺寸,右上 hover 操作,右下选中勾。无时间。
- `task-card.tsx`:进行中=骨架+shimmer+阶段进度;失败=错误+重试/移除。
- 数据:`AssetRecord.CreatedAt`(store)已落库且查询 `ORDER BY created_at DESC`;`AssetView` 未序列化 `CreatedAt`;前端 `Asset` 无时间字段;`Task` 无时间字段。
- `Asset.parentId` / `AssetView.ParentID` 已存在(派生关系)。
- 设计 token:深蓝沉静底、12px 圆角、降饱和 accent、弱 `border-line`、`framer-motion`、`shimmer`、`prefers-reduced-motion`。`brand-mark.tsx` 单色菱形 SVG。
- App 布局:`grid lg:grid-cols-[7fr_3fr]`,工作区在左占 ~30%(`order-1`),对话在右占 ~70%。窄屏堆叠。

用户已定:**事件串联 / 任务也入轴 / 后端下发真实时间**。

## Goals / Non-Goals

**Goals**
- 工作区呈现为一条按操作先后串联的时间轴(生产流水),传达"资产工坊"的加工过程感。
- 任务(进行中/失败)与产物在同一条轴上连续呈现,完成原地转产物,不跳段。
- 节点带真实创建时间(相对时间)与派生标注(由哪张图加工而来)。
- 编号(图N/视频N)保留为 LLM 沟通锚点;移除拖拽后顺序自然随时间,机制不变。

**Non-Goals**
- 不做跨会话的历史时间轴(仍是当前 session 工作区)。
- 不引入重型时间轴库;用现有 Tailwind + framer-motion 自绘。
- 不改对话区、capsule、SSE 进度协议、模型/provider。
- 不做时间轴的缩放/平移等复杂交互(保持克制)。

## Decision 1 — 时间轴轴心:事件节点 + 排序方向

**轴心**:每个节点 = 一次**创作事件**。事件类型与现有 kind/task 对应:
- 上传(upload asset,无 task)
- 生图/编辑(generate task → generated asset)
- 切尺寸(crop,一次操作可产多张 → 一个节点内含多张产物,见 Decision 4)
- 生视频(video task → video asset)
- 爬取(crawl task → 多张 crawled asset)

**排序**:按事件时间。对 asset 用 `createdAt`;对进行中/失败 task,它们是"正在发生/刚失败"的最新事件,排在轴的活跃端。

**方向**:最新在**顶部**(自上而下 = 新→旧),活跃任务节点在最顶。理由:工作区在左窄栏,用户最关心刚发生的;顶部活跃端也让进行中任务始终可见,无需滚动。(备选:最新在底=聊天式;否决,因为窄栏滚动成本高。)

**与对话区的关系**:不变(仍左工作区右对话)。时间轴是工作区**内部**的组织方式。

## Decision 2 — 任务入轴与"原地转产物"

**问题**:现在 task 完成后从"进行中段"消失、asset 出现在"已完成段",视觉上是跳段。

**方案**:时间轴节点以**稳定 key** 串联 task 与其产物。task 有 `assetId`(完成后指向产物 asset)。节点渲染逻辑:
- 一个进行中/失败 task → 一个活跃/错误节点(key = task.id)。
- task 完成(status=done)→ 后端产出 asset,前端该节点**原地**用 asset 内容替换(动画过渡),key 保持连续(task.id 与其 assetId 关联)。
- 无 task 的直接产物(如上传)→ 直接是产物节点(key = asset.id)。

**统一节点模型**(前端派生,不入库):
```
TimelineNode = {
  id            // 稳定 key(task.id 优先,否则 asset.id)
  at            // 排序时间(asset.createdAt 或 task 的活跃时间)
  state         // running | failed | done
  kind          // generate | crop | video | crawl | upload
  assets[]      // 已产出的资产(done 时非空;crop 可多张)
  task?         // 关联 task(进行中/失败时有,用于进度/重试)
  parentId?     // 派生来源(取自产物 asset.parentId)
}
```
前端从现有 `state.tasks` + `state.assets` 合成 `TimelineNode[]`,按 `at` 排序。**不新增后端结构**,只需 `AssetView.createdAt`。

## Decision 3 — 时间戳来源与相对时间

- 后端 `AssetView` 增加 `createdAt`(RFC3339)。`AssetRecord.CreatedAt` 已有,只需在 workspace handler 序列化。
- 前端 `Asset` 增 `createdAt?: string`。
- task 的活跃时间:进行中/失败 task 无后端时间戳;前端用"首次见到该 task 的本地时刻"作为其排序时间(活跃节点本来就排最顶,精度不敏感)。完成后改用产物 `createdAt`。
- 相对时间展示("刚刚 / N 分钟前 / HH:mm"):纯前端格式化,随时间刷新(节点上小字)。

## Decision 4 — 一次操作多产物(crop / crawl)

切尺寸一次可产多张,爬取一次产多张。设计为**一个节点聚合多张产物**(节点内小网格/横向缩略图),而非每张一个节点——这才符合"一次操作"的事件语义。

**聚合依据**:同一来源 + 相近时间 + 同 kind。最稳妥用一个操作批次 id;但当前后端 crop 产物各自独立、无批次 id。为避免后端改动,前端启发式聚合:**同 parentId + 同 kind=cropped + createdAt 相近(同一秒级窗口)**归为一个节点。若聚合不准带来风险,降级为"每张产物一个节点"(仍按时间串联,只是更碎)。design 标注此为已知取舍。

## Decision 5 — 编号:保留为 LLM 锚点,顺序随时间

**澄清(按用户反馈)**:编号的**唯一目的**是作为 LLM 对话锚点——让 LLM 在"图2/图3/视频1"这类指代里识别到对应的 asset id,并在回复里用同样的称呼。它**不是**用户用来排序/整理的工具,也不需要为时间轴"重新定义语义"。

**方案**:
- 编号机制保留不变(派生展示属性,不入库,作为"图N→asset_id"映射注入 agent 上下文)。
- **图片用"图N"、视频用"视频N"**两套独立计数(图片归图片序、视频归视频序),与用户口头表达一致;在节点角标与发给 LLM 的映射里都用对应前缀。
- 移除拖拽后,显示顺序天然等于创建时间序,编号也就沿时间先后呈现——这是移除拖拽的**自然结果**,而非刻意改语义。时间序比可拖拽序更稳定,对 LLM 沟通反而更可靠。
- `controller.ts` 的 `orderedAssetIds`(构造编号映射发后端的)改为按 `createdAt` 升序,与时间轴展示一致;`buildNumbering`/`BuildAssetNumbering` 的前缀按 kind 区分图/视频。

**不做**:不把编号当作用户可调整的排序入口(拖拽已移除)。

## Decision 5b — 批次 id 留作后续(确认)

一次操作多产物的**稳定聚合**最干净的做法是后端给同一批产物打一个批次 id。本提案**不**在后端加批次 id(确认留作后续),先用 Decision 4 的前端启发式聚合(同 parentId + 同 kind + createdAt 同秒级窗口);聚合不确定时降级为每产物一节点。后续若需精确聚合,再补后端批次 id。

## Decision 6 — 移除自由拖拽,保留其余能力

时间轴是时间序,**移除已完成资产的拖拽重排**(`state.order` 及相关逻辑)。保留:多选、批量切尺寸、批量移除、单图右键/操作、放大预览、清空、打包下载。这些不依赖顺序。

spec 层:`frontend-experience` 的"工作区拖拽排序与多列分栏"requirement 被时间轴呈现取代(MODIFIED 为时间轴串联 + 节点内多产物网格)。

## Decision 7 — 工坊感视觉(克制)

沿用既有 token,不加花哨色:
- **时间轴主干**:一条细 `border-line` 竖线贯穿,节点是主干上的小圆点(accent 描边)。
- **节点卡**:沿用现有卡片样式(方图、角标),左侧贴轴的时间标记 + 相对时间小字。
- **活跃节点**:进度/shimmer(复用 task-card 现有动效),圆点用 accent 脉冲。
- **派生连线(可选增强)**:若产物有 parentId 且来源也在轴上,用极淡的引导线/文字"由 图N 加工"标注,不强画连线以免杂乱。
- **工坊语义文案**:节点头部用动作短语("换背景""切尺寸 ×3""生视频"),配既有 Lucide 图标,呼应"加工"。
- 遵守 `prefers-reduced-motion`。

## Risks / Open Questions

- crop/crawl 多产物的前端启发式聚合可能不准(无后端批次 id);降级为每产物一节点。是否值得后端补批次 id 留待后续。
- task 活跃时间用本地时刻,多端/重连场景下进行中 task 的相对时间可能不精确(可接受,活跃节点恒在顶)。
- 移除拖拽是能力回退;需确认用户不依赖手动排序(本提案按用户"时间轴"诉求判定时间序优先)。
- 编号机制不变(仍为 LLM 锚点),仅映射顺序随拖拽移除而变为时间序;需回归既有"图N→LLM"链路(时间序更稳,预期正向)。视频用"视频N"前缀需同步到节点角标与发往 LLM 的映射。

## Migration

- 后端 `createdAt` 为加法字段,旧前端忽略不受影响。
- 前端移除 `state.order` 拖拽逻辑为内部重构,无数据迁移。
- 无 DB 变更。
