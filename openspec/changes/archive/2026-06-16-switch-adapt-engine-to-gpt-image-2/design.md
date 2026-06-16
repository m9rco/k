# Design: 平台适配 gpt-image-2 比例映射与分档收敛

## Context
适配 AI 重绘链路现状：

1. `agent.go:460` 解析 `gemini-3-pro-image` 注入 `deps.AdaptModelOverride`；`tools.go:adaptProvider` 优先用它，否则回退 `ImageOverride`。
2. `AdaptToPlatform`（`adapt.go`）按比例分流：比例一致走纯裁剪快路径；否则 `Start` 一个生成任务，`ProviderOverride` 即上面的 override。
3. 生成任务 `run`（`service.go`）按 `wantW/wantH` 调 `provider.Generate`，`http_provider.sizeParam` 把尺寸 snap 到 gpt-image 的 3 个枚举，出图后用 `crop.ModeContain` 收敛到精确 `adaptW×adaptH`。

问题集中在两点：默认引擎是 gemini 而非 gpt-image-2；`sizeParam` 只按方向三分类，且收敛一律 `contain`。

## Goals / Non-Goals
- Goals：默认 gpt-image-2；按比例就近选生成尺寸；按比例差异分档收敛；保留主体、减少无谓留白；可预配置。
- Non-Goals：变体（换人物/文字）与图转视频；改动纯裁剪快路径的比例容差；改动 OpenAI 适配器协议本身。

## Decision 1：默认引擎 gpt-image-2，gemini 降级为回退
`agent.go` 把请求级适配 override 的目标模型从 `gemini-3-pro-image` 改为 `gpt-image-2`。`ResolveImageModel(SceneImage, "gpt-image-2")` 仅在该模型可用时返回 override；不可用时 `AdaptModelOverride=nil`，`adaptProvider` 落到 `ImageOverride`（会话 image 选择或服务默认），从而**自然回退**——若部署把 gemini 配为 image 场景默认，gemini 即作为回退生效。

为满足「gpt-image-2 不可用时优先回退 gemini」的语义，回退顺序设计为：`gpt-image-2`（请求级）→ 会话 image override / 服务默认 provider。是否在 gpt-image-2 缺失时显式探测 gemini，取决于现有 `ResolveImageModel` 能力；若需显式 gemini 兜底，则在解析失败分支补一次 `ResolveImageModel(SceneImage, "gemini-3-pro-image")`。两种回退都保证适配不报「模型不可用」。

非适配的 `edit_image`、文生图、视频路径**完全不受影响**（仍用各自 scene override）。

## Decision 2：比例预设映射表（取代方向三分类，且不使用 auto）
gpt-image-2 合法生成尺寸：`1024×1024`(1:1)、`1536×1024`(3:2)、`1024×1536`(2:3)。

**不选 `auto`**：`auto` 让模型按 prompt 自选尺寸，产物比例不可控，后续收敛无法稳定预估留白/裁切量，违背「预先写好参数、最终直接可用」的诉求。改为**显式传入按目标宽高比就近匹配的合法枚举**：

```
ar = W/H
候选 = {1.0: "1024x1024", 1.5: "1536x1024", 0.667: "1024x1536"}
选 |log(ar) - log(候选ar)| 最小者   // 对数距离，横竖对称
```

按对数比例距离就近选取，使 3:2 命中 `1536×1024`、4:1 仍命中最接近的 `1536×1024`（已是最宽），1:1 命中方形。映射集中在一处（`sizeParam` 或新的 `nearestGenSize` 函数），数据驱动、便于预配置与单测。

## Decision 3：分档智能收敛（contain vs cover）
出图后 `genAR` = 生成尺寸比例，`dstAR` = 目标平台比例。定义收敛分档：

```
diff = |log(genAR) - log(dstAR)|
diff <= convergeTolerance(默认值待定，建议 ~0.18≈20%)  → ModeContain（等比留白，保全主体）
diff >  convergeTolerance                              → ModeCover（裁切铺满，避免大面积留白）
```

- 多数平台尺寸（横版 banner、竖版海报）比例接近某个合法枚举，走 `contain` 几乎无留白。
- 极端比例（4:1 长横幅 vs 3:2 生成）差异超阈值，走 `cover` 裁切铺满，牺牲边缘换取无空白成片——符合「按比例分档智能选择」决策。

**预设覆盖**：`config.Size` 增加可选字段 `convergeMode`（`"contain"`/`"cover"`，空=按上面分档自动判定）。尺寸目录可对已知的极端规格预先写死收敛模式，绕过自动判定，达到「预先写一部分预估参数、最终直接使用」。透传链路：`adapt.go` 从 `SizeSpec` 读 `convergeMode` → `GenerateParams`（新增字段）→ `service.go` 收敛分支据此选 `crop.Options{Mode}`。

## Risks / Trade-offs
- `cover` 裁切可能切掉极端比例下的部分主体——仅在比例差异过大时触发，且可由预设 `convergeMode=contain` 针对具体尺寸回退为留白，由配置方权衡。
- `convergeTolerance` 阈值需经少量真实尺寸验证微调；以常量+单测固定，后续可调。
- gpt-image-2 与 gemini 对同一 prompt 的构图风格不同，切换默认引擎会改变成片观感——属预期内的产品选择（艺术效果优先）。

## Migration
纯配置/路由/算法层改动，无数据迁移。既有产物归属（`crop.CropMeta`、`Via=ai`）结构不变。`Size.convergeMode` 为可选字段，旧目录 JSON 无该字段时按自动分档运行，向后兼容。
