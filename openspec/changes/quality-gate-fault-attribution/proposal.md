# Change Proposal: quality-gate-fault-attribution

## 摘要

现有质量门控失败时**始终重跑整条流水线**（gpt-image-2 重绘 + 可能的 Gemini outpaint），即便缺陷仅来自其中一步。本 change 让判官在 JSON 输出中携带 `fault_source`（`"repaint"` | `"outpaint"` | `"both"`），服务端据此**精确回退**：

| fault_source | 缺陷来源 | 重试策略 |
|---|---|---|
| `repaint` | gpt-image-2 重绘内容有问题 | 整条流水线重跑（现有行为，hints 注入 gpt-image-2 prompt） |
| `outpaint` | Gemini outpaint 填充有问题（边界感、风格割裂、留白） | **跳过 gpt-image-2**，只重跑 outpaint 步骤（复用上次重绘结果） |
| `both` | 两步均有问题 | 整条流水线重跑（同 repaint） |

实现核心：`run()` 在 outpaint 步骤**之前**快照 gpt-image-2 产物（`preOutpaintData`），质量失败时若 `fault_source==outpaint` 则把快照作为 `PreOutpaintData` 传给重试参数，重试路径直接跳到 outpaint 步骤，**不再调用 gpt-image-2**。

## Motivation

流水线由两个语义完全不同的步骤组成：

- **gpt-image-2**：理解宣发意图、重绘主体、构图 → 缺陷表现为主体错误、内容偷换、整体质量差
- **Gemini outpaint**：只在极端比例（ratio diff > 0.18）下触发，纯粹"往外延伸场景"填补空带 → 缺陷表现为边界割裂、填充内容与主体风格不一致、仍有明显色块/留白

当 Gemini outpaint 产出了糟糕的边缘填充，但重绘本体完全正确时，重跑整条流水线：
1. 浪费一次 gpt-image-2 API 调用（~5–20s + 配额）。
2. 即便重绘结果依然好，outpaint 再次失败的概率也不低（相同 prompt + 相同 source）。
3. hints 注入的改进点是给重绘的，对 outpaint 失败没有帮助。

精确回退 → outpaint-only 重试：把已经验证过的重绘结果直接喂给 outpaint，节省一次重绘调用，提示词也可针对 outpaint 优化。

## Scope

### 本 change 范围

- `qualityPrompt`（`internal/vision/quality.go`）：增加 `fault_source` 字段说明与选项。
- `rawVerdict` / `QualityVerdict`（vision + generation 两处）：增加 `FaultSource string`。
- `GenerateParams.PreOutpaintData []byte`：非空时 `run()` 跳过 gen.Generate()，直接用此数据进入 outpaint/converge 步骤。
- `run()`（`internal/generation/service.go`）：
  - outpaint 步骤前快照 `preOutpaintData`。
  - 质量失败时依 `FaultSource` 决定 retry 策略：`outpaint` → 带 `PreOutpaintData` 重试；其他 → 现有整条重跑。
- 单测：`fault_source=outpaint` 时跳过 gen 调用只重跑 outpaint；`fault_source=repaint/both` 时整条重跑。

### 不在本 change 范围内

- 非 outpaint 路径（ratio 差 ≤ 0.18，纯 scale 收敛）不受影响：这些路径没有"两步"，`FaultSource` 即便是 `outpaint` 也退化为整条重跑。
- 多次重试（封顶仍为 1 次，本 change 只是让那一次更精准）。
- 前端无需感知 `fault_source`（已有 `review_failed` + 「按建议重绘中」UI 足够）。
