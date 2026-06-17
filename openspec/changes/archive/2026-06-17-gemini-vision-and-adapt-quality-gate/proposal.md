# Change Proposal: gemini-vision-and-adapt-quality-gate

## Summary

两项聚焦的适配质量改进：

1. **视觉分析模型换 Gemini**：把视觉宣发要素分析从 `grok-4-fast`（OpenAI-compat，需 COS 上传公网 URL）切换到 `gemini-2.5-flash-all`，使用 Gemini 原生 `:generateContent` 接口，直接传 inline base64 图片 —— **彻底消除视觉分析对 COS 的依赖**。
2. **适配后质量门控（打分制 + 审核态可视化 + 反馈重走生图）**：AI 重绘产物收敛后、持久化前，调 `doubao-seed-1-6-vision-250815` 做一次**打分审核**——合规性为**硬红线一票否决**，主体一致性/人物卖相/整体质量加权成总分，低于阈值（默认 75）或命中红线即判不及格。不及格时把红线原因 + 低分维度 hints 反馈给最初的生图模型 `gpt-image-2`，**重走一遍完整生图流程**（审核1次 + 重生1次封顶，重生产物不再审核，杜绝循环）。全程通过 SSE 向前端下发审核态（审核中 🔍 → 通过 ✓ / 不及格 ✗「按建议重绘中」），**同一占位卡片演进、不留流程空白**；前端无需展示分数细节，保持「生图中」体感即可。

## Motivation

### 1. 为何换 Gemini 视觉分析

当前流程：上传参考图到 COS → 拿到公网 URL → 用 `image_url` 调 grok-4-fast。问题：

- COS 未配置时整个视觉分析阶段直接跳过，适配丢失主题约束。
- 多一次网络往返（上传），分析延迟增大。
- Gemini `:generateContent` 接口原生支持 `inlineData`（base64），分析不再需要公网 URL；与现有 `generation/gemini.go` 已有的 inline 模式完全对称。

> 是否还需要 COS 上传？  
> `gemini-2.5-flash-all` 经 Gemini 原生接口可接受 inline base64，**不需要上传**。但现有的「上传时预热 + 重新分析」功能仍需要 COS（需要 URL 来支持 `reanalyze` 路径）。**本 change 将 COS 降级为可选优化（有则预热、无则直接 inline 分析）**，而非硬依赖。

### 2. 为何加适配质量门控

当前适配只保证产物尺寸正确和风格与源图接近，但无法自动识别：
- 合规风险（图中出现违禁文字/符号）
- 宣发主体偷换（主角被换掉）
- 人物面部/身体被裁到角落或模糊

加一个「模型-as-judge」环节（doubao 视觉模型），在产物还处于 blurred/loading 状态时就做一次判断，不合格则用判官给出的 hints 重试一次，用户看到的最终产物质量显著提升。

## Scope

### 不在本 change 范围内

- 多次重试（本 change 固定最多 1 次）
- 质量门控用于非适配生图（文生图、图生图编辑）
- 前端显示质量评分详情
- 合规规则库维护

## Open Questions（已在 design.md 解答）

1. `gemini-2.5-flash-all` 经 yunwu 代理还是直连 Google？→ 见 design.md
2. doubao-seed-1-6-vision-250815 支持 data URI 还是只支持 https URL？→ 见 design.md
3. 质量门控判断超时如何处理？→ 见 design.md
