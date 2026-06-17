## Context

极端宽高比适配（6:1/5:1/4:1 横幅，及对称的竖条）当前走「生成(gpt-image-2, 夹 3:1) → 扩绘(outpainter) → cover 裁切」三段式。`data/logs/app.log` 实测显示该路径产出主体腰斩、LOGO 丢失、稳定性差。规范 `platform-adaptation` 已声明极端比例应 `cover` 收敛，但实现的 `convergeMode` auto 分档对该档错选 `outpaint`，且生成阶段未对齐最终窄幅画布。

约束：
- gpt-image-2 硬约束长短比 ≤ 3:1，**无法直接生成 6:1**；目标精确像素由收敛步保证（不可偏离目录档位）。
- outpainter（nano banana）有自己的尺寸枚举，不覆盖极端比例，强行扩绘 + 裁切 = 双重损失。
- 仅小团队内部使用，优先简单、稳定、可单测，避免引入新模型或外部依赖。

## Goals / Non-Goals

- **Goals**:
  - 极端比例产物主体完整、构图自然、批次间稳定。
  - 单次生成 + 确定性裁切，去掉极端档的第二段 AI（outpaint）。
  - 修正规范与实现的偏离，方案对用户透明（意图/产物形态/归属不变）。
- **Non-Goals**:
  - 不改中等/同比例尺寸的现有 scale/outpaint 行为。
  - 不追求在极端比例下「无损保留」纵向像素（物理上 6:1 画布纵向信息本就少，目标是主体完整而非分辨率极大）。
  - 不引入新的图生图/扩绘模型。

## Decisions

### D1：极端比例收敛改走确定性 `cover`，不走 `outpaint`

`convergeMode` 在 auto 分档中新增「极端比例」判定：计算**目标自身**长短比 `dstRatio = max(w/h, h/w)`，当 `dstRatio >= extremeConvergeRatio`（缺省 `3.0`，与生成端 3:1 夹断阈值对齐）时直接返回 `crop.ModeCover`，跳过 outpaint。

- 为何用 `cover` 而非 `outpaint`：outpainter 尺寸枚举不覆盖极端比例（实测 6:1 目标得到 2.36:1 输出，反而更窄），扩绘方向与裁切方向矛盾。`cover` 是确定性的、零额外 AI、零随机性。
- 为何与生成端阈值对齐：生成端一旦因 3:1 夹断而无法贴合目标比例，收敛就**必然**面临大比例差，此时 outpaint 已无意义。两个阈值同源（`gptImage2MaxRatio = 3.0`）保证路由自洽。
- 中等比例差（`convergeTolerance < gap` 且 `dstRatio < extremeConvergeRatio`）维持 `outpaint` 不变——这类目标比例本身合法，gpt-image-2 能贴合，outpaint 仅做小幅扩展，仍有价值。

分档优先级（auto，无目录 pin 时）：
1. `dstRatio >= extremeConvergeRatio` → `ModeCover`（新增）
2. `gap > convergeTolerance` → `ModeOutpaint`（原有，现仅覆盖中等差）
3. 否则 → `ModeScale`（原有）

目录 `convergeMode` 显式 pin 仍最高优先（不变）。

### D2：极端比例生成阶段「安全区构图」

仅 cover 还不够：3:1 的图 cover 到 6:1 仍纵向腰斩。必须让 gpt-image-2 **为最终窄幅画布构图**——把主体收进中央安全带，上下只放可牺牲背景。

`extremeRatioHint` 升级为带**安全带占比**的指令。安全带尺寸 = 生成画布中、cover 裁切到目标比例后会被保留的中央区域：

- 横幅（目标更宽）：生成约 3:1，cover 到 6:1 时保留中央高度 `keepFrac = genRatio / dstRatio`（如 3:1 生成 → 6:1 目标，保留中央 50% 高度）。提示模型把主体/LOGO/文案放在**中央 `keepFrac` 高度带内**，上下 `(1-keepFrac)/2` 各为可裁背景。
- 竖条对称处理（保留中央宽度带）。

`keepFrac` 由 `genRatio`(实际生成比例) 与 `dstRatio`(目标比例) 在收敛前即可算出，提示中以「中央约 N%」措辞传达（N 取整到 5% 网格，避免模型对精确百分比过敏）。

### D3：目录预设兜底

`channels.json` 各尺寸 `convergeMode` 预设优先级不变。极端尺寸**可**显式 pin `"cover"` 作兜底，但 D1 的 auto 分档已覆盖，默认不强制逐个标注（减少配置维护）。

## Risks / Trade-offs

- **纵向分辨率损失**：6:1 成片纵向仅保留生成图的 ~50%。→ 缓解：生成端已按 ~3MP 预算放大出图（现有 `gptImage2GenBudget`），保留带的绝对像素仍足够锐利；且 6:1 画布纵向信息密度本就低，主体完整 > 纵向像素极大。
- **`keepFrac` 估计偏差**：模型未必严格遵守安全带。→ 缓解：提示用「中央约 N%」+ 「上下仅放背景延伸」双重表述；质量门控（在途 change）作为二次兜底，但本 change 目标是把门控触发率降到最低。
- **阈值取值**：`extremeConvergeRatio` 缺省 3.0 与生成端对齐。若未来目录引入 3.2:1 等临界比例，可能在 outpaint/cover 边界抖动。→ 缓解：阈值集中为常量，可调；临界尺寸可用目录 `convergeMode` pin 显式锁定。

## Migration Plan

纯行为改进，无数据/接口变更。上线后极端尺寸的新产物自动走新路径；已落库的旧产物不受影响（用户可对不满意的旧产物重新触发适配）。回滚 = 还原 `convergeMode` 分档与 `extremeRatioHint`，无副作用。

## Open Questions

- `extremeConvergeRatio` 是否需要按横幅/竖条分别取值？缺省统一 3.0，实现中用对称的 `max(w/h, h/w)` 判定，暂不区分；若实测竖条表现不同再拆分。
