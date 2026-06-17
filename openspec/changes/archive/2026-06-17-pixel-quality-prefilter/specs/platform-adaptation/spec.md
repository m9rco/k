# platform-adaptation (delta)

## ADDED Requirements

### Requirement: 像素级质量预过滤（模糊检测 + 画面完整度）
系统 SHALL 在 AI 重绘适配产物进入 AI judge 审核**之前**，对其执行一次纯算法像素级质量检查（`PixelChecker`）。`PixelChecker` SHALL 检测两类客观缺陷：

1. **模糊**：计算图像灰度 Laplacian 方差；方差低于配置阈值（`PIXEL_BLUR_THRESHOLD`，默认 80）时判不及格，原因为「图像模糊，清晰度不足」。
2. **画面留白带**：扫描图像四边各约 8% 宽度，检测是否存在 ≥ `PIXEL_BORDER_MAX_RATIO`（默认 15%）的纯色/均匀色带；命中时判不及格，原因为「存在纯色留白条带」。

`PixelChecker` SHALL 为纯 Go 实现，不依赖 CGo 或外部进程，执行时间 SHALL < 50ms。

像素检查不及格时，系统 SHALL 直接推送 `review_failed` 事件（携带像素层原因与改进 hints），跳过 AI judge 调用，并按现有重生流程重走一次生图（封顶，同 taskID）。像素检查通过时，系统 SHALL 照常进入 AI judge 审核（现有 `platform-adaptation: 适配后质量打分门控与单次重生` 要求保持不变）。

`PixelChecker` 为 nil（`PIXEL_BLUR_THRESHOLD=0` 且 `PIXEL_BORDER_MAX_RATIO=0`，或未初始化）时，系统 SHALL 完全跳过像素检查，行为与像素检查引入前一致。

#### Scenario: 明显模糊图被像素层拦截
- **GIVEN** 一张 Laplacian 方差 < 阈值（默认 80）的适配产物
- **WHEN** 适配产物收敛后进入质检流程
- **THEN** 系统推送 `review_failed` 事件，reason 包含「图像模糊」
- **AND** 系统跳过 AI judge，直接以「提升清晰度，避免模糊」为 hints 触发重生
- **AND** 重生产物不再触发像素检查或 AI judge（封顶）

#### Scenario: 有纯色留白带的产物被像素层拦截
- **GIVEN** 一张四边存在 ≥15% 宽度纯色留白条带的适配产物
- **WHEN** 适配产物收敛后进入质检流程
- **THEN** 系统推送 `review_failed` 事件，reason 包含「纯色留白」
- **AND** 系统跳过 AI judge，直接以「画面应完整填充，无纯色边框」为 hints 触发重生

#### Scenario: 像素检查通过后继续 AI judge
- **GIVEN** 一张清晰且无明显留白的适配产物
- **WHEN** 像素检查完成
- **THEN** 系统照常调用 AI judge（不跳过）
- **AND** AI judge 的通过/不及格逻辑不受影响

#### Scenario: 像素检查器未配置时透明跳过
- **GIVEN** `PIXEL_BLUR_THRESHOLD=0` 且 `PIXEL_BORDER_MAX_RATIO=0`
- **WHEN** 适配产物进入质检流程
- **THEN** 系统不执行像素检查，直接进入 AI judge（行为与本 change 前一致）
