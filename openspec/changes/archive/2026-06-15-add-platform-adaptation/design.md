# Design: 平台 AI 适配

## Context
现有「切尺寸」(`crop_to_sizes`) 是纯裁剪：确定性、免费、快，但只能裁/缩放，无法在比例变化时保留主体与宣发意图。图生图能力 (`internal/generation`) 已具备异步任务、SSE 进度、颜色适配、参考图复用、注入防护、多供应商失效切换、以及 icon 场景的「provider 出图 → `contain` 收敛到精确尺寸」范式。本变更在两者之上引入「平台适配」语义层，把「切尺寸」意图的默认实现切换到 AI 重绘，同时把纯裁剪降级为快路径与兜底。

非目标：不改动换角色/背景/文案/icon/视频等既有意图；不引入新供应商或厂商 SDK；不做鉴权（沿用项目内部约束）。

## Goals / Non-Goals
- Goals：保留主体与核心宣发意图地适配到各平台尺寸；提示词显式覆盖平台语义，让所有图生图模型准确理解；比例一致免费快路径；会话级 `(源图,尺寸)` 去重避免重复烧钱。
- Non-Goals：替换纯裁剪的底层实现；自动化 A/B 多版本；平台素材审核合规校验。

## Decision 1：智能路由（裁剪快路径 vs AI 重绘）
**入口约束**：平台适配只经对话 Agent 的 `adapt_to_platform` 工具触达——会话模型先理解源图与本次宣发意图，再决定调用——**不开放绕过模型的独立 HTTP 端点**。前端尺寸选择器的「AI 平台适配」按钮把所选源图与尺寸 id 作为一条对话消息发给 Agent，而非直连服务端；纯裁剪因是确定性、无需模型理解的操作，保留前端直连 crop 端点。

**判定**：比较源图与目标尺寸的宽高比。
- `|ar_src - ar_dst| / ar_dst <= ratioTolerance`（默认 0.04）且**方向相同**（横/竖/方一致）→ **裁剪快路径**：走现有 `crop.CropToSizes`（默认 `cover`），确定性、零模型成本。
- 否则（比例差异大、横竖翻转）→ **AI 重绘**：走 `generation.Start` 新 `adapt_platform` 意图。

**理由**：比例一致时裁剪等价于无损缩放，AI 重绘只会增加成本与失真风险；比例变化大时裁剪必然切主体或留黑边，必须靠模型补全画面。判定对用户透明（一个意图、一致的产物回填），仅在内部分流。

**容差取值**：4% 容忍轻微像素取整差（如 1280×720 vs 1920×1080 同为 16:9，比例差 0），但 1:1 → 9:16 这类必然走 AI。容差为常量，便于后续按实测调。

## Decision 2：会话级去重，键 = `(源图资产 id, 目标尺寸 id)`
**现状**：`turnCallGuard` 只在单轮内去重（防模型一轮内重复发同一 tool call）。
**变更**：平台适配额外做**会话级**去重。命中已存在的「同源图 + 同尺寸」适配产物时，直接复用该资产、不再起图生图任务。

**实现选型（持久化优先，而非内存 map）**：去重状态落在已持久化的产物资产上，而非进程内 map。
- 适配产物落库时，`Meta` 记录 `{channelId, sizeId, sourceAssetId, via: "ai"|"crop"}`（裁剪产物已有 `CropMeta` 的 channelId/sizeId，这里对齐并补 sourceAssetId/via）。
- 适配入口先查 store：`session 内存在 parentId==源图 且 meta.sizeId==目标尺寸 的成功产物` → 复用其 assetId，秒回。
- **理由**：进程重启/多 worker 下内存 map 会丢；产物本就持久化，查库判重最准确，且天然覆盖「跨轮复用」。键用 `sizeId` 而非宽高，尊重渠道语义差异（同为 1920×1080 的「社区头图(仅 logo)」与「投放素材」是不同适配目标）。

**与单轮 guard 的关系**：单轮 `turnCallGuard` 保留（防同一轮重复 tool call）；会话级查库判重叠加在其后，覆盖「跨轮再次请求同一适配」。

## Decision 3：提示词模板必须显式表达平台语义
新 `adapt_platform` 模板（服务端完全控制，用户文本仅作为 sanitized slot 片段）须覆盖：
1. **保留主体与核心宣发意图**：keep the main subject/characters and the core marketing intent intact。
2. **适配目标平台/尺寸**：recompose for a {orientation} {W}×{H} {channelName} {assetTypeName} placement。
3. **补全而非裁切**：extend/repaint background to fill the new aspect ratio; do NOT crop the subject out。
4. **尺寸语义约束透传**：把该尺寸的 `note`（无文案/仅 logo/圆角/透明底/安全区）作为约束注入。
5. **颜色与风格协调**：复用既有 `harmonyConstraint` + palette。

模板把这些写成模型无关的明确英文指令，确保 gpt-image-2 与 Gemini 系列都能解析；`channelName/assetTypeName/note` 来自 `channels.json`，经 `Sanitize` 后填入固定模板槽位，不参与控制语义。

## Decision 4：尺寸收敛
图生图供应商常只支持固定尺寸枚举，产物尺寸 ≈ 目标但不精确。沿用 icon 范式：provider 出图后用 `crop.CropBytesWithOptions(..., ModeContain)` 收敛到精确平台尺寸（保留完整主体、留白补齐）。用户视角为一次性产出精确平台尺寸。

## Decision 5：批量与分发
一次适配可针对多源图 × 多尺寸。沿用 `download-packaging` 既有策略：产物数 ≤ 6 直接回填工作区；> 6 前置提示后按 渠道/尺寸 分目录打包 zip。AI 适配产物与裁剪产物在 `Meta` 上结构一致（channelId/sizeId），打包逻辑无需区分两条路径。

## Risks / Trade-offs
- **成本**：AI 路径按次烧钱。缓解：比例一致走免费裁剪 + 会话级去重 + 供应商失效才切备用。
- **主体漂移**：模型重绘可能改动主体细节。缓解：模板强约束「保留主体」、注入 palette、把源图作为主参考；用户可二次调整或退回纯裁剪兜底。
- **延迟**：AI 比裁剪慢。缓解：异步任务 + SSE 进度 + 工作区占位，与既有生图体验一致。
- **横竖翻转的补全质量**：1:1 → 9:16 需大量补画，质量依赖模型。接受为已知限制，前端保留手动裁剪兜底。

## Migration
- `crop_to_sizes` 工具、直连 crop 端点、前端四种裁剪模式**保持兼容**，签名不变。
- 「切尺寸」意图默认路由到新 `adapt_to_platform` 工具；纯裁剪通过前端「手动裁剪」入口或显式裁剪措辞触达。
- 已落库的旧裁剪产物 `CropMeta` 向后兼容（缺 sourceAssetId/via 时按裁剪产物处理）。
