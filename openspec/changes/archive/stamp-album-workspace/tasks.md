# Tasks: stamp-album-workspace

Ordered list of small, verifiable work items.

## Phase 1 — 删除批量切尺寸入口

- [ ] **T1** 从 `WorkspacePanel` 工具栏移除「批量切尺寸」按钮及相关 `cropFor` state 的工具栏触发逻辑（保留 `cropFor` state 和 `SizePicker`，单图入口不变）
  - 验收：工具栏不再出现「批量切尺寸」按钮；单个资产卡片的切尺寸入口正常工作

## Phase 2 — 集邮册视图骨架

- [ ] **T2** 在视图 toggle 中新增 `stamp` 选项（`BookImage` 图标），`ViewMode` 类型扩展为 `"grid" | "timeline" | "stamp"`，sessionStorage key 支持新值
  - 验收：三种视图图标可切换，切换后 sessionStorage 正确持久化

- [ ] **T3** 新建 `StampAlbum` 组件（`web/src/components/workspace/stamp-album.tsx`），接收 `onPreview`，内部调用 `api.listPlatforms()` 加载渠道数据
  - 验收：切换到集邮册视图时组件挂载，渠道数据加载完成后渲染渠道列表（不含图片，只显示名称和尺寸信息即可）

- [ ] **T4** 集邮册顶部参考图区：自动选取最新 generated/upload 资产；「更换」按钮打开工作区资产选择弹窗
  - 验收：进入集邮册时正确展示参考图；手动更换后刷新视图

## Phase 3 — 插槽渲染与状态

- [ ] **T5** 渠道组按 `group` 渲染，支持 tab 过滤（外渠 / 手机厂商 / 腾讯内渠 / PC / 全部）
  - 验收：tab 切换后只显示对应 group 的渠道组

- [ ] **T6** 渲染每个可生产尺寸的插槽（empty 态：虚线边框 + 尺寸名 + 宽×高）；`producible: false` 尺寸渲染为灰色说明占位
  - 验收：TapTap 渠道组展示全部 17 个尺寸插槽，视频类型为说明占位

- [ ] **T7** 插槽状态映射：基于 `state.assets`（按 sizeId 索引）和 `state.tasks`（按 sizeId 反查进行中任务）计算插槽 filled / generating / error 状态
  - 验收：生成一张 TapTap 截图后对应插槽自动转为 filled 态（无需刷新）

- [ ] **T8** filled 插槽 hover 操作栏：预览（调 `onPreview`）、下载（单张）、重新生成（重发 sendMessage）
  - 验收：hover 已填充插槽时操作栏出现；点击预览打开 Lightbox

## Phase 4 — 一键生成

- [ ] **T9** 渠道组右上角「生成全部」按钮：收集 empty/error 状态的 sizeIds → `app.sendMessage(text, referenceAssetId, sizeIds)`
  - 验收：点击 TapTap「生成全部」后，所有空插槽转为 generating 态，生成完成后逐个回填

- [ ] **T10** 无参考图时「生成全部」不可点击（tooltip 引导）；渠道全部已填充/进行中时按钮禁用并提示「全部已生成」
  - 验收：对应禁用态出现且操作被阻断

## Dependencies
- T2 → T3（视图骨架依赖 toggle 扩展）
- T3 → T4, T5（参考图区和渠道 tab 依赖组件骨架）
- T5 → T6（插槽依赖渠道组渲染）
- T6 → T7（状态映射依赖插槽 UI）
- T7 → T8, T9（操作依赖插槽状态）
- T9 → T10（按钮禁用态依赖生成逻辑）
- T1 可独立并行

## Parallelizable
- T1（删除按钮）可与 T2 并行
