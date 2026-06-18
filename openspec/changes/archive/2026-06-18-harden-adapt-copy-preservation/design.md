## Context

`preserve-adapt-key-elements`（已归档）给质量门控加了 `key_elements_fidelity` 红线 + 重生复检择优。本次发现它的择优只比 `total`，把红线作废了；同时暴露中等比例差无保文案约束。两者都让「文案丢失」的产物交付出去。

## Goals / Non-Goals

- Goals:
  - 择优不再让 `total` 掩盖 `key_elements_fidelity` 红线——优先交付保住文案/主体的版本。
  - 两版都丢文案时如实告知（降级信号），不伪报通过。
  - 中等比例差（如 16:9→3:2）重绘时，给模型明确的保文案/重排约束，让它「接近满足」而非硬丢。
- Non-Goals:
  - 不改生成比例策略（`resolveGptImage2Size` 仍贴目标比例）、不引入第三轮生图、不做确定性抠图合成（用户已否决）。
  - 不改极端档（≥3:1）既有 `extremeRatioHint` 行为。

## Decisions

### Decision 1：择优改为红线感知的 `bestOf`（Q1）

`service.run` 的 recheck 当前：`if FirstAttemptTotal > regenTotal { 用首检 }`——只比 total。改为按优先级比较两版：

1. **红线通过性**：`Pass=true` 优先于 `Pass=false`；
2. 两版红线状态相同时，比 `key_elements_fidelity`（高者优先）——直接对应「谁更保住文案/主体」；
3. keyelem 相同时比 `total`；
4. 仍相等取重生版（已含改进 hints）。

为此 `GenerateParams` 需携带首检版的 `Pass` 与 `KeyElementsFidelity`（当前只带 `FirstAttemptTotal`），`generation.QualityVerdict` 与 adapter 需补 `KeyElementsFidelity` 字段（vision 侧 `DimScores` 已有，只是没透传到 generation 包）。

- 数据验证：日志 `task_1aa95b64`（首检 keyelem=30 > 重生 25）→ 新逻辑保首检；`task_13dbcf62`（首检 keyelem=25 > 重生 20）→ 保首检。均纠正了「挑更差版本」。
- Alternatives：*两版都不过就 task_failed 不交付*——与上个 change 已确认的「不丢弃产物、择优交付」决策冲突，否决；改用「交付 + 降级信号」兼顾。

### Decision 2：两版均未过红线 → `review_failed{degraded:true}`（Q1）

当 `bestOf` 选出的最终版 `Pass=false`，下发 `review_failed`（带 `degraded:true` 与 `final:true`），而非现在无条件的 `review_passed`。前端据此可标「带瑕疵交付」，旧客户端忽略新字段仍按 task_done 完成（加法式兼容，沿用既有审核态契约）。产物照常持久化。

### Decision 3：中等比例差注入 `reproportionHint`（Q2）

新增 `reproportionHint(srcW, srcH, dstW, dstH, sizeNote)`，在 `BuildPrompt` 的 MODIFY 段（`extremeRatioHint` 之后）注入，触发条件：

- 源↔目标宽高比的 log 差 `|ln(srcAR) − ln(dstAR)|` **超过裁剪快路径容差**（即不会走确定性裁剪），**且** 目标未达极端档（`extremeRatioHint` 返回空）——正好覆盖 900×600 这类中间区间；
- 900×500（距源 1.2%）走快路径或差异极小 → 不触发，保持现状。

注入文案要点（按 placement 过滤）：
- 通用：「目标画幅与源图比例差异较大，需重构构图。重排时 MUST 保留主体、LOGO 完整可见，按新比例重新布局而非裁掉或省略。」
- placement 非「无文案」时追加：「同时保留主标题/核心文案，可重新排布位置，但不得丢弃或改写其文字。」
- placement 含「无文案」时不提文案（与既有 `SizeNote` 过滤一致）。

为此 `Slots` 增 `SourceWidth/SourceHeight`，`service.run` 在 BuildPrompt 前用已加载的源图尺寸（`srcW/srcH`）写入。

- 阈值取裁剪快路径容差（`ratioTolerance`）为下界、极端档为上界，二者之间即「需重构但非极端」区间，语义自洽，无新魔数。

## Risks / Trade-offs

| 风险 | 缓解 |
|---|---|
| `reproportionHint` 仍靠模型遵守，未必每次保住文案 | 与 Decision 1/2 协同：门控红线 + 择优 + 降级信号兜底；提示是「提高概率」，门控是「不放过」 |
| 降级信号可能让前端显示「带瑕疵」引困惑 | 加法式字段，前端可渐进采用；默认仍交付产物不阻塞 |
| `bestOf` 改变既有重生测试预期 | 同步更新 `quality_gate_test.go`，新增红线感知择优用例 |

## Migration Plan

纯增强，无数据迁移。`reproportionHint` 随二进制生效；recheck 比较逻辑替换即生效。回滚还原 `service.go` + `prompt.go` 即可。

## Open Questions

无（两个修复方向由日志直接推导，用户已认可「交给 AI 补全 + 门控兜底」的总方向）。
