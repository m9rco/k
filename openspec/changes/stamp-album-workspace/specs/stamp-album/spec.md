# stamp-album Specification

## Purpose
为宣发素材准备阶段提供「集邮册」视图——以参考图为锚点，按 channels.json 渲染全渠道尺寸插槽，生成产物自动回填，支持一键触发渠道级批量适配。

## ADDED Requirements

### Requirement: 集邮册视图模式
系统 SHALL 在工作区提供 `stamp` 视图模式（与 `grid` / `timeline` 并列），切换后视图区渲染集邮册面板；所选视图 SHALL 持久化至 sessionStorage，页面刷新后恢复。

#### Scenario: 切换到集邮册视图
- **WHEN** 用户点击工具栏中的集邮册视图图标
- **THEN** 工作区视图区切换为集邮册面板
- **AND** 视图选择持久化到 sessionStorage

#### Scenario: 刷新后恢复集邮册视图
- **WHEN** 用户在集邮册视图下刷新页面
- **THEN** 工作区初始化后恢复集邮册视图

### Requirement: 参考图区
集邮册面板顶部 SHALL 展示参考图区：初始化时自动选取工作区中最新的 `generated` 或 `upload` 类型资产作为参考图；用户 SHALL 可点击「更换」从工作区资产列表中手动选取参考图；若工作区无可用资产，SHALL 显示引导态提示用户先上传或生成参考图。

#### Scenario: 自动选取参考图
- **WHEN** 用户进入集邮册视图，工作区已有至少一张资产
- **THEN** 最新的 generated 或 upload 资产被自动设为参考图并展示在顶部参考图区

#### Scenario: 手动更换参考图
- **WHEN** 用户点击参考图区「更换」按钮并从弹出列表中选择另一张资产
- **THEN** 参考图更新为所选资产

#### Scenario: 无可用资产时的引导态
- **WHEN** 用户进入集邮册视图，工作区中无任何资产
- **THEN** 参考图区显示引导提示「请上传或生成一张参考图后再使用集邮册」
- **AND** 集邮册网格区不渲染可操作的生成按钮

### Requirement: 渠道网格渲染
集邮册网格区 SHALL 按 channels.json 渲染所有渠道，每个渠道为一组，组内展示该渠道下所有 `producible: true` 的尺寸插槽；渠道 SHALL 按 `group` 字段（外渠 / 手机厂商 / 腾讯内渠 / PC）可通过 tab 过滤；`producible: false` 的规格（如视频）SHALL 显示为不可操作的说明占位，不计入插槽可生成数。

#### Scenario: 渲染全渠道插槽
- **WHEN** 用户进入集邮册视图
- **THEN** 系统从 channels.json 读取全部渠道并渲染各渠道组
- **AND** 每组显示渠道名称、可生产尺寸数量、以及每个可生产尺寸的插槽

#### Scenario: 按 group 过滤渠道
- **WHEN** 用户点击渠道 group tab（如「腾讯内渠」）
- **THEN** 网格只展示属于该 group 的渠道组

#### Scenario: 不可生产规格说明占位
- **WHEN** 某渠道包含 producible: false 的尺寸（如视频规格）
- **THEN** 该尺寸显示为灰色说明占位（展示名称和格式），无生成按钮

### Requirement: 插槽状态与自动回填
每个插槽 SHALL 根据工作区资产状态呈现以下状态之一：`empty`（虚线框，展示尺寸名和宽高）、`generating`（骨架动画，与 TaskCard 视觉一致）、`filled`（生成图缩略图）、`error`（错误提示 + 重试）。当工作区中有 `sizeId` 与插槽匹配的资产到达时，对应插槽 SHALL 自动更新为 `filled` 状态，无需用户手动刷新；同一 sizeId 多次生成时，插槽展示 `createdAt` 最新的资产。

#### Scenario: 空插槽展示
- **WHEN** 集邮册中某 sizeId 在工作区无对应资产，且无进行中任务
- **THEN** 插槽呈 empty 态：虚线边框 + 尺寸名 + 宽×高

#### Scenario: 生成中插槽展示
- **WHEN** 工作区有针对该 sizeId 的进行中任务（queued 或 running）
- **THEN** 插槽呈 generating 态：骨架动画 + 进度标记

#### Scenario: 自动回填已生成插槽
- **WHEN** 工作区收到 asset_ready 事件，且该 asset 带有与某插槽匹配的 sizeId
- **THEN** 对应插槽自动切换为 filled 态并展示该资产缩略图

#### Scenario: 同 sizeId 多次生成展示最新
- **WHEN** 同一 sizeId 存在多张已生成资产
- **THEN** 插槽展示 createdAt 最新的那张

#### Scenario: filled 插槽操作
- **WHEN** 用户 hover 已填充插槽
- **THEN** 展示操作栏：预览（打开 Lightbox）、下载（单张）、重新生成

### Requirement: 一键生成渠道全部素材
每个渠道组 SHALL 提供「生成全部」按钮；点击后以当前参考图为输入，向 Agent 发送适配指令，为该渠道下所有处于 `empty` 或 `error` 状态的可生产尺寸各产出一张；发送路径与现有 SizePicker adapt 路径一致（`app.sendMessage(text, referenceAssetId, sizeIds)`）；若当前无参考图，按钮 SHALL 不可点击并显示 tooltip 引导。

#### Scenario: 一键生成渠道全部未填充尺寸
- **WHEN** 用户点击某渠道组的「生成全部」按钮，且已有参考图
- **THEN** 系统收集该渠道下所有 empty/error 状态的可生产 sizeId
- **AND** 调用 app.sendMessage，以参考图为 referenceAssetId，以收集的 sizeIds 为目标，触发平台适配
- **AND** 各对应插槽转入 generating 态

#### Scenario: 全部已填充时按钮提示
- **WHEN** 某渠道所有可生产插槽均已处于 filled 或 generating 状态
- **THEN** 「生成全部」按钮呈禁用态，提示「全部已生成」

#### Scenario: 无参考图时按钮不可用
- **WHEN** 工作区无参考图
- **THEN** 所有渠道组的「生成全部」按钮不可点击
- **AND** hover 展示 tooltip「请先选择参考图」
