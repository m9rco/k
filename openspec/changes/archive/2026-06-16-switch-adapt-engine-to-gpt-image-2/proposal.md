# Change: 平台适配默认引擎切到 gpt-image-2（比例预设映射 + 智能分档收敛）

## Why
平台适配的第一步是**主体不变、宣发要点不变，和谐地生成各平台尺寸**。当前 AI 重绘固定走 `gemini-3-pro-image`，而 `gpt-image-2` 在保留主体/构图的艺术效果上更好，应作为这一步的默认引擎。

但 `gpt-image-2` 只支持固定尺寸枚举（`1024×1024` / `1536×1024` / `1024×1536`，外加让模型自选的 `auto`），无法直接指定任意平台尺寸——传任何尺寸都会被它匹配到最相近的默认尺寸。当前 `sizeParam` 仅按「方向」粗暴三分类（square/landscape/portrait），导致 3:2 与 4:1 这类差异巨大的目标都被压成同一个 `1536×1024`，再用 `contain` 一律留白收敛，极端比例下产生大面积空白、成片质量差。

需要把策略升级为：**默认 gpt-image-2 → 按目标比例预设映射到最接近的合法生成尺寸 → 出图后按比例差异分档智能收敛到精确平台尺寸**。

## What Changes
- **默认引擎切换**：`adapt_to_platform` 的 AI 重绘路径默认使用 `gpt-image-2`（取代原固定的 `gemini-3-pro-image`）；`gemini-3-pro-image` 降级为**失效回退**（gpt-image-2 凭证缺失/调用失败时回退到 gemini 或服务默认 provider，适配不失败）。
- **比例预设映射表**：把 `sizeParam` 的「方向三分类」升级为**目标宽高比 → 最接近合法生成尺寸**的预设映射（基于 gpt-image-2 的固定枚举，按宽高比就近匹配而非仅按方向）。**不使用 `auto`**（`auto` 让模型自选尺寸、比例不可控，无法稳定收敛）。映射规则集中、数据驱动、可预配置。
- **智能分档收敛**：出图后按「生成比例 vs 目标平台比例」的差异分档收敛——差异在阈值内用 `contain`（等比留白、保全主体）；差异过大（如 4:1 极端横幅）用 `cover`（裁切铺满、避免大面积留白）。每个尺寸可在尺寸目录中**预设收敛模式覆盖**默认分档判定。
- 适配的颜色适配、参考图复用、注入防护、异步管线、产物归属与打包策略**保持不变**。

本次范围仅限「第一步：和谐生成各尺寸」的生图引擎策略。第二步（基于指定尺寸的人物/文字元素变体）与第三步（图转视频）留作后续 change。

## Impact
- Affected specs: `platform-adaptation`（MODIFIED 2 条：请求级模型路由、适配尺寸精确收敛；ADDED 1 条：gpt-image-2 比例预设映射）
- Affected code:
  - `internal/agent/agent.go`（适配默认 override：`gemini-3-pro-image` → `gpt-image-2`，gemini 转为回退候选）
  - `internal/generation/http_provider.go`（`sizeParam` 升级为比例预设映射）
  - `internal/generation/service.go`（适配收敛由固定 `contain` 改为分档选择 `contain`/`cover`）
  - `internal/config/config.go`（`Size` 增加可选收敛模式预设字段）
  - `internal/generation/adapt.go`（透传尺寸的预设收敛模式到生成参数）
