# Change: 适配必备要素保真——判官保真维度 + Hints 按 placement 过滤 + 重生择优

## Why

`data/logs/app.log`（trace_199426902e60a886）暴露三起适配产物缺陷，现有质量门控全部漏过：

- `asset_c0fbd56bebbd3662`（900×600 封面）：判官 total=86、subject=100，**一次过**，但核心宣发主体丢失。
- `asset_caba3ad1b4805069`（900×600 封面）：首检 total=65 不及格触发重生，**重生 total=35 更差但仍落库**（重生不复检）。
- `asset_8825bdc7257eddba`（512×512 icon）：判官 total=93、subject=85 **一次过**，但主体文字全被改写/糊化。

**排查结论：参考图已正确传入重生**。首检与重生的 `ref_count` 均为 2、`refs:1`（primary 在 `srcBytes`，1 张 extra 在 `extraImages`），参考图不是问题。

**三个真实根因**：

1. **判官无「必备要素/文字保真」维度**（`quality.go:23-36`）：只评 `subject_consistency / character_appeal / overall_quality / canvas_fill`，LOGO/定档大字/标签是否保留、文字是否被改写——无任何维度直接评（asset_8825 文字全变仍 subject=85 过）。
2. **加权总分淹没单项硬伤**（`quality.go:368`）：asset_c0fbd56 overall=50 被高分拉高到 total=86 蒙混过。
3. **REVISE hints 与 placement 文案约定冲突，重生不复检**（最直接的 bug）：judge 从主题报告「必须保留」清单生成了「补全 LOGO/大字/标签」类 hints，但没有过滤 `SizeNote` 中的「无文案」约定。重生 prompt **同时出现**「补全 LOGO/大字」与「Respect constraint: 无文案」，模型在矛盾指令下更差（total 65 → 35），且重生不复检直接落库。

## What Changes

- **judge prompt 的 hints 生成按 placement 文案约定过滤**：在 `qualityPrompt` 中明确，若【目标规格】含「无文案」，hints SHALL NOT 建议补充文案类要素（LOGO 可保留在必保范围，纯文案大字/标签不纳入改进建议）。消除 REVISE 与 placement 约定的矛盾。**BREAKING**（修改 judge 行为）。
- **判官新增 `key_elements_fidelity`（必备要素保真，0-100）维度 + 硬红线**：核对核心主体/LOGO 是否在画面内、要求保留的文字是否存在且字符正确（非糊化/改写/乱码）。低于 `KEY_ELEMENTS_FIDELITY_MIN`（缺省 60）**一票否决，绕过加权总分**，不被其余高分维度掩盖。**BREAKING**（修改判定逻辑）。
- **必保清单按 placement 过滤**：核心主体/LOGO 任何 placement 必保；纯文案要素仅当 placement 未约定「无文案」时纳入必保。
- **重生产物复检 + 择优交付**：重生（`Attempt=1`）产物也过判官一次；首检版与重生版**交付总分更高的一版**，杜绝「重生更差却盲发」。封顶两轮生图、最多两次判官调用，无循环。**BREAKING**（修改「重生不复检」约定）。

## Impact

- Affected specs: `platform-adaptation`（`适配后质量打分门控与单次重生` MODIFIED）
- Affected code:
  - `internal/vision/quality.go`：`qualityPrompt` 增 hints 过滤规则 + `key_elements_fidelity` 维度、`rawVerdict`/`QualityVerdict` 增字段、`evaluate()` 增硬红线
  - `internal/generation/service.go`：`run()` 重生分支增复检与择优落库（`service.go:877-887`）、保留首检版字节供对比
  - `internal/config`：新增 `KEY_ELEMENTS_FIDELITY_MIN`
  - 测试：`internal/vision`、`internal/generation` 表驱动单测 + 日志三缺陷回归
