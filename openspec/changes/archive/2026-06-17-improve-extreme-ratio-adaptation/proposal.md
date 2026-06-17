# Change Proposal: improve-extreme-ratio-adaptation

## Why

极端宽高比尺寸（如好游快爆 `1008×168`(6:1)、`1008×202`(5:1)、`1008×252`(4:1)）的适配产物**效果差且不稳定**——主体被压扁/腰斩、头部与兔耳被裁出画面、LOGO 缺失，即使过了质量门控也明显不合格。

根因不是模型能力，而是**当前收敛管线对极端比例选错了策略**。基于 `data/logs/app.log` 中 `sess_fdcd08fe82f1d221` 的真实记录，三张图的完整链路如下（以 `1008×168` 为例）：

1. `resolveGptImage2Size(1008,168)` 把目标 6:1 **夹到 3:1 上限**，生成尺寸 `gen_size=3008×1008`（日志 line 12/45）。gpt-image-2 因此**从未见过真正的 6:1 目标比例**，它按 3:1 构图、主体居中铺满整幅。
2. 收敛阶段 `convergeMode` 的 auto 分档：gen 3:1 与 dst 6:1 的 log-ratio gap ≈ 0.69 ≫ `convergeTolerance(0.18)`，**选择了 `ModeOutpaint`（扩绘）**（`adapt.go:206-225`）。
3. `outpaintConverge` 把 3:1 的图交给 outpainter（nano banana），要求扩成 6:1。但 outpainter **snap 到自己的尺寸枚举**，实际输出 `fill_w=1584 fill_h=672`（≈2.36:1，比输入的 3:1 **还窄**！日志 line 42）。
4. 最后 `cover`-crop `1584×672 → 1008×168`，**纵向裁掉约 61%**（`service.go:974`），把 2.36:1 的图腰斩成 6:1，主体头部/兔耳/LOGO 全在被裁掉的上下区域。

也就是说：极端比例走的是**「生成(AI) → 扩绘(AI) → 破坏性裁切」三段式**。问题有三：

- **方向矛盾**：6:1 比 3:1 更宽，本应左右扩展；但 outpainter 输出反而更窄（2.36:1），cover 只能向纵向腰斩。outpainter 的尺寸枚举根本不覆盖 6:1，这条路注定失败。
- **双 AI 误差叠加**：两次独立 AI 调用各自带随机性，加上 outpainter「保中心像素不变 + 凭空发明 50% 新内容」的自相矛盾约束，产物在「保真」和「重绘」之间摇摆 = **不稳定**。
- **构图未对齐最终画布**：模型为 3:1 构图，但成片是 6:1。模型不知道上下会被裁掉，自然把主体放满全幅。

`spec.md` 的「适配尺寸精确收敛」其实已写明*极端比例应走 `cover` 裁切*（行 80-83），但实现的 auto 分档对这一档错误地选了 `outpaint`——**规范与实现存在偏离**。但仅把 outpaint 改成 cover 仍不够：3:1 的图 cover 到 6:1 照样腰斩。必须同时让生成阶段**为最终窄幅构图**。

## What Changes

核心思路是「降维打击」：对极端比例，**一次生成即面向最终窄幅画布构图，再走确定性 cover 裁切**，彻底去掉第二段 outpaint AI。

- **收敛路由分档新增「极端比例」档**：当目标长短比被 3:1 上限夹断（即真实目标比例 ≥ `extremeConvergeRatio`，缺省 3.0）时，收敛**强制走确定性 `cover`，不再走 `outpaint`**。修正规范与实现的偏离，消除 outpaint 段的漂移与尺寸 snap 双重损失。中等比例差（容差 ~ 极端阈值之间）维持现有 `outpaint` 行为不变。
- **极端比例生成阶段「安全区构图」引导**：当本次生成因 3:1 夹断而无法达到目标比例时，向图生图提示注入**构图安全带约束**——把主体、LOGO、核心文案安排进**中央水平带（横幅）/ 中央垂直带（竖条）**，上下（或左右）只放可牺牲的背景延伸。安全带占比按目标比例与生成比例的关系**动态计算**（如 6:1 目标 ≈ 中央 50% 高度，4:1 ≈ 75%），使后续 cover 裁切落在「只裁背景、不伤主体」的区域。
- **目录可选预设兜底**：保留 `configs/channels.json` 各尺寸 `convergeMode` 预设优先级不变；极端尺寸可显式 pin `cover` 作为兜底，但默认无需逐个标注——auto 分档已覆盖。

不改变：中等/同比例尺寸的现有 scale/outpaint 行为；产物精确等于目录档位的硬约束；多参考组、前置发布/分析、质量门控等管线。

## Impact

- **Affected specs**: `platform-adaptation`（MODIFIED：适配尺寸精确收敛、生图尺寸的比例就近映射；ADDED：极端比例安全区构图）
- **Affected code**:
  - `internal/generation/adapt.go`：`convergeMode` 新增极端比例档（`convergeTolerance` 旁增 `extremeConvergeRatio`）
  - `internal/generation/prompt.go`：`extremeRatioHint` 升级为带安全带占比的「安全区构图」指令；新增安全带高度/宽度计算
  - `internal/generation/service.go`：收敛分支保持，确认极端档走 `crop.ModeCover` 而非 outpaint
  - 测试：`adapt_test.go` / `prompt_test.go` 覆盖极端比例的路由与 prompt 断言
- **与在途 change 协同**：与 `gemini-vision-and-adapt-quality-gate` 互补——本 change 从根本上减少极端比例触发质量门控重绘的频率，二者无冲突。
