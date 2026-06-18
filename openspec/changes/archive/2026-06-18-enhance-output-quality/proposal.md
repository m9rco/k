# Change Proposal: enhance-output-quality

## Why
在生图质量门控、低幻觉约束、生视频质检三个维度系统性提升输出质量。核心措施：①将视觉质检模型升级为 Gemini 2.5 Flash（更强语义理解）并新增「宣发吸引力」审美维度；②质检失败重试上限从 1 次提升到 2 次；③为视频产物引入源图视觉质检（当前生视频无任何质量门控）；④生视频 prompt 自动 LLM 扩写以减少动作描述不足导致的质量下降。

## What Changes
- 质检模型可配置升级为 `gemini-2.5-flash-all`（复用已有 Gemini 传输层）
- 质检新增 `ad_appeal`（宣发吸引力）维度，不改变 pass/fail 基线，仅附加 hints
- 质检重试上限 1→2（可配置 `QUALITY_MAX_RETRY`），三路 bestOf 取最优产物
- 普通生图（换角色/背景/文案/加角色）接入现有质量门控
- 生视频新增 `PromptEnricher`（LLM 扩写动作描述）与 `VideoQualityChecker`（源图代理质检）

## Summary
在生图质量门控、低幻觉约束、生视频质检三个维度系统性提升输出质量。核心措施：①将视觉质检模型升级为 Gemini 2.5 Flash（更强语义理解）并新增「宣发吸引力」审美维度；②质检失败重试上限从 1 次提升到 2 次；③为视频产物引入首末帧视觉质检（当前生视频无任何质量门控）；④生视频 prompt 自动 LLM 扩写以减少动作描述不足导致的质量下降。

## Motivation

### 现状 gap 分析

| 区域 | 当前状态 | 缺陷 |
|------|---------|------|
| 质检模型 | `doubao-seed-1-6-vision-250815` | 对英文 prompt 语义理解偏弱；同项目的 marketing analysis 已用 `gemini-2.5-flash-all`，两条链路模型割裂 |
| 质检维度 | 6 维（compliance/subject_consistency/character_appeal/overall_quality/canvas_fill/key_elements_fidelity） | 缺少「宣发吸引力」（视觉冲击力/CTR感）和「风格一致性」（与原游戏美术的协调度）两个直接影响投放效果的维度 |
| 质检重试 | 失败后最多重生成 1 次 | 一次 hint 注入有时不足以修复主体偏移或构图问题；2 次重试可显著提升最终合格率，成本可接受 |
| 质检覆盖 | 仅 `adapt_platform` 路径有质量门控 | `generate_image`（换角色/背景/文案）完全没有质量门控，幻觉/主体偏移无检测 |
| 视频质检 | **无** | 生视频产物没有任何内容一致性或质量验证，是最大质量盲区 |
| 视频 prompt | 用户原始动作描述 → 服务端模板 | 动作描述往往极短（"让角色走起来"），prompt 信息量不足是视频质量低的主因之一 |

### 目标
- 适配产物「主体一致性」分数均值提升（量化：平均分从估计 ~72 → 目标 ≥80）
- 生视频产物引入可量化质量信号（当前 0 维度 → 首末帧一致性分数）
- 宣发吸引力维度让系统产出真正「想点击」的素材，而不只是技术上过关

## Scope

### In Scope
1. **质检模型可配置升级**：`QualityChecker` / `SubjectDetector` 支持 `gemini-2.5-flash-all`（已有 Gemini 传输层，零新 HTTP 适配器）
2. **审美维度扩展**：质检 prompt 新增 `ad_appeal`（宣发吸引力 0-100）维度，更新 rawVerdict + qualityPrompt + 评分聚合
3. **质检重试上限 1→2**：service.go 中的 `Attempt` 上限从 1 提升到 2，第 2 次携带累积 hints
4. **普通生图质检**：为 `generate_image` 意图（换角色/背景/文案/加角色）接入现有质量门控
5. **视频首末帧质检**：video.Service 接入 `VideoQualityChecker`（提取首末帧 → 调用现有 QualityChecker）
6. **生视频 prompt LLM 扩写**：video.Service 在组装 motion prompt 前用 Claude 对用户动作描述做富化扩写

### Out of Scope
- 模型预训练 / 微调
- 角色身份卡全局持久化（复杂度高，下一个 change 考虑）
- 视频帧逐帧全量审核（计算成本过高）
- 修改前端 UI（质检结果当前已通过 SSE 上报，无需新 UI）

## Risks & Mitigations
| 风险 | 缓解措施 |
|------|---------|
| Gemini 质检在中文内容理解上低于 doubao | 质检模型可配置，不强制切换；doubao 仍为 fallback 选项 |
| 2 次重试使生图成本翻倍 | 仅在第 1 次 score < threshold 且 hints 非空时触发第 2 次；通过率高时不触发 |
| 视频质检增加 30-60s 延迟 | 质检在视频生成完成后异步触发，不阻塞 SSE 首帧反馈；质检失败时记录 warning，不阻断产物回填 |
| LLM 视频 prompt 扩写增加延迟 | 用 `claude-haiku-4-5` 做 prompt 扩写（现有 API，低延迟）；失败时降级回用原始 motion description |
