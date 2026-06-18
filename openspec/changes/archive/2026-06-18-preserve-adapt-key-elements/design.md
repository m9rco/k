## Context

适配质量门控由 `internal/vision/QualityChecker` 打分、`internal/generation/Service.run` 编排。本次打破三条现有约定：

- 判官只评 5 项，**无必备要素/文字保真维度**。
- 「重生产物 SHALL NOT 再次进入审核门控」——防循环规则，副作用是重生更差也盲发。
- **`qualityPrompt` 的 hints 生成不感知 placement 文案约定**——已被日志直接证伪：`caba3ad` 重生 prompt 同时含「补全 LOGO/大字」与「Respect: 无文案」，模型在矛盾指令下 total 从 65 跌到 35。

## Goals / Non-Goals

- Goals:
  - REVISE hints 按 placement 文案约定过滤，消除「补文字」与「无文案」的矛盾指令。
  - 判官能识别「主体/LOGO 缺失」与「文字被改写/糊化」，作为硬红线。
  - 重生仍差时有安全网：复检后择优交付。
  - 全流程可降级——判官不可用时一律降级为及格，不阻塞出图。
- Non-Goals:
  - 不改生图模型、outpaint/converge 几何、像素预过滤。
  - 不引入第三轮生图。
  - 不做 OCR 级精确字符对比；判官视觉模型自身判断即可。

## Decisions

### Decision 1：hints 生成在 judge prompt 里按 placement 约定过滤

`Check()` 已接收 `specLabel`（含 `SizeNote`，经 Sanitize）。在 `qualityPrompt` 里新增一条规则：**若【目标规格】含「无文案」，生成 `hints` 时 SHALL NOT 建议补充纯文案类要素（定档大字、底部标签等）；LOGO 仍可纳入 hints。** 若含「仅 logo」，hints 也不建议补充纯文案，仅可提 LOGO。

这让 hints 天然与 placement 约定对齐，重生 prompt 不再出现矛盾。

- Alternatives considered:
  - *服务端用正则过滤 hints 文本*：hints 是自然语言，正则可靠性差，且滞后于 judge 生成。源头修复更准。
  - *调大重生 prompt 里的 placement 权重*：模型已看到约定，问题在 REVISE 和约定的相对权重不明，加强约定文案仍治标不治本。

### Decision 2：`key_elements_fidelity` 维度为判官硬红线

与 `canvasFillMin=60` 红线同构（`quality.go:359-366`）。低于 `KEY_ELEMENTS_FIDELITY_MIN`（缺省 60）一票否决。asset_c0fbd56 exactly 复现了这个漏洞：overall=50 被高分拉高到 total=86 蒙混过。

必保清单按 placement 过滤（与 Decision 1 一致）：核心主体/LOGO 任何 placement 必保；纯文案要素仅当未含「无文案」时纳入。

### Decision 3：重生复检 + 择优交付

保留首检版字节 + Total → 重生（`Attempt=1`）→ 重生版也调判官（≤35s + 降级为及格）→ 比 Total → 落库更高者。相等取重生版。全程封顶两轮生图、最多两次判官调用，无循环。

## Risks / Trade-offs

| 风险 | 缓解 |
|---|---|
| 多一次判官调用（重生复检）延迟 +~8-12s | 仅首检不及格时触发；35s 超时 + 降级为及格，不阻塞管线 |
| judge 把可读文字误判为糊化 | 下限 60 给出容差；parse 失败仍整体降级为及格 |
| hints 过滤不完整（judge 仍建议文案） | hints 是建议性文案，最坏情况退化到旧行为；不引入新失败模式 |

## Migration Plan

纯增强，无数据迁移。`KEY_ELEMENTS_FIDELITY_MIN=0` 关闭硬红线。回滚还原 `quality.go` + `service.go` 即可，无残留状态。
