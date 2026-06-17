# Design: stamp-album-workspace

## 视图架构

集邮册作为工作区的第三种视图模式（`stamp`），与 `grid` / `timeline` 并存于视图切换 toggle。切换逻辑和 sessionStorage 持久化策略沿用现有模式。

```
WorkspacePanel
├── 工具栏  [BrandMark] [工作区] [grid|timeline|stamp toggle] ... [打包下载]
│           ↑ 删除"批量切尺寸"按钮
└── 视图区
    ├── <WorkspaceGrid>    (view=grid)
    ├── <Timeline>         (view=timeline)
    └── <StampAlbum>       (view=stamp) ← 新增
```

## StampAlbum 布局

```
┌────────────────────────────────────────────────────────┐
│  参考图区（固定高度，可折叠）                             │
│  [ 参考图缩略图 ]  [ 更换 ]                              │
│  ↑ 若无参考图：「请上传或生成一张参考图后再使用集邮册」     │
├────────────────────────────────────────────────────────┤
│  渠道过滤 tab：全部 / 外渠 / 手机厂商 / 腾讯内渠 / PC    │
├────────────────────────────────────────────────────────┤
│  渠道组 1（TapTap）            [生成全部 →]             │
│  ┌──────┐ ┌──────┐ ┌──────┐ ┌──────┐ ...              │
│  │截图横│ │截图竖│ │ICON  │ │Banner│                    │
│  │1280×7│ │720×12│ │512×5 │ │1920×1│                   │
│  └──────┘ └──────┘ └──────┘ └──────┘                  │
│                                                         │
│  渠道组 2（4399）              [生成全部 →]             │
│  ...                                                    │
└────────────────────────────────────────────────────────┘
```

## 插槽状态机

```
empty ──[一键生成/单图生成]──▶ generating ──[asset_ready+sizeId匹配]──▶ filled
  ▲                                │ [failed]                               │
  └──────────────[重新生成]─────────▼                                       │
                                 error                                       │
  ◀─────────────────────────────────────────────────────[重新生成]──────────┘
```

- **empty**：虚线边框，展示 `sizeName + W×H`
- **generating**：骨架动画 + 进度（复用 TaskCard 的视觉样式）
- **filled**：缩略图；hover 展示操作栏（预览 / 下载 / 重新生成）
- **error**：红色边框 + 错误提示 + 重试按钮

## 参考图选取逻辑

1. `StampAlbum` 内部维护 `referenceAssetId: string | null`（组件 state，不持久化）
2. 初始化时自动选取 `state.assets` 中 `kind === "generated" || kind === "upload"` 的最新一张
3. 用户可点「更换」从工作区资产列表中手动选取
4. 无资产时显示引导态，不渲染集邮册网格

## 自动回填机制

利用现有 `asset_ready` 事件 + `Asset.sizeId` 字段：
- `StampAlbum` 订阅 `state.assets` 变化（已由 `useApp` context 驱动）
- 每次 assets 变化时，重新计算 `sizeId → Asset` 的映射表（取 `createdAt` 最新者）
- 插槽 render 时查此映射表决定状态

**无需新增 WebSocket 事件或 API**，完全在现有数据流上叠加。

## 一键生成逻辑

点击渠道的「生成全部」按钮：
1. 收集该渠道下所有 `producible: true` 且当前插槽为 `empty/error` 的 `sizeId` 列表
2. 调用现有 `app.sendMessage(text, referenceAssetId, sizeIds)` —— 与 SizePicker 的 adapt 路径完全一致
3. 触发后各插槽转入 `generating` 状态（由 task 占位 + asset_ready 回填驱动）

## 删除批量切尺寸入口

在 `WorkspacePanel` 中，删除以下 JSX 片段：
```tsx
{state.selected.size > 0 && (
  <Button variant="ghost" size="sm" onClick={() => setCropFor([...state.selected])}>
    <Crop className="size-3.5" /> 批量切尺寸
  </Button>
)}
```
`cropFor` state 和 `SizePicker` 组件本身**保留**（仍由单个资产卡片的「切尺寸」触发）。

## 渠道数据加载

沿用 `api.listPlatforms()` — 无需新增 API 端点。
