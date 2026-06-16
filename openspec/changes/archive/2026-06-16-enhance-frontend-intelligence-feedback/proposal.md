# Proposal: 前端智能感与反馈优化

## Summary
针对四个用户反馈点强化前端的"智能感"表达：补全关键交互 loading 态、分析报告流式完成后自动折叠、参考图与产物之间的关联可视化、AI 直接参与修图时同步体现在对话流。

## Problem
1. **冷场**：上传文件、适配尺寸提交、重试生成等操作缺乏即时视觉反馈，用户不知道系统正在处理。
2. **分析块常驻**：宣发分析流式完成后 `collapsed` 保持 `false`，挤占对话区空间、干扰后续内容。
3. **参考关系不可见**：当产物由多张参考图生成时，卡片上没有任何线索说明其来源。
4. **重试无 chat 痕迹**：点击资产卡的"重试"后，工作区有占位、进度条可见，但对话区完全没有呼应，体验割裂。

## Solution

### 1. 分析报告流式完成自动折叠
`onAnalysisDelta` 在 `done=true` 时将 `collapsed` 置为 `true`，完成即收起，需要时手动展开。规则与 ReasoningBlock 保持一致。

### 2. 关键交互 loading 补全
- **上传**：`uploadFiles` 期间 overlay 骨架或图标旋转（拖入区 / Composer 上传按钮）。
- **尺寸选择器提交**：确认按钮已有 `running` 守门，但按钮本身缺 spinner；补 `<Loader2>` 内联图标。
- **重试生成**：`retryAsset` 调用期间按钮变 disabled + spinner，防连点。

### 3. 参考图来源标识
后端在 `/api/session/:id/assets` 响应中，对 AI 产物补充 `referenceIds: string[]`（从 `gen_origin` 的 `reference_asset_ids` 读取）。前端 `AssetCard` 在有 `referenceIds` 时渲染「参考 N 张」徽章；当 N ≤ 4 时，hover 展开小缩略图行。

### 4. AI 修图在对话区同步
`retryAsset` 被触发时，在 chat 中插入一个 `kind:"tool"` 卡片（工具名 `edit_image`，intent `retry`，status `running`）；任务终态（done/failed）时同步更新为对应状态。这让用户在对话视图中也能感知到 AI 正在重绘。

## Affected Specs
- `frontend-experience` — loading 补全 + 参考来源标识 + chat 同步（MODIFIED）
- `marketing-analysis` — 分析完成自动折叠（MODIFIED）
- `asset-workspace` — referenceIds 字段（MODIFIED）

## Out of Scope
- 参考来源的精确视觉重构（如连线/分组）留给后续 change
- adapt_to_platform 批量产物间的关联（already tracked by sizeId）
