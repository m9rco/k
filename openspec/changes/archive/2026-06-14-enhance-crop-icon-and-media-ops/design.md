## Context
系统已有：纯图像裁剪（cover-fit 居中裁）走直连端点 + agent 工具双通道；图生图走 `generation` 服务（换角色/背景/文案，异步任务 + SSE）；图生视频走 `video` 服务（COS 上传源图 → happyhorse 异步）。本次在这些既有结构上做增量，不重写主链路。

约束（来自 project.md / 既有实现）：
- 后端单二进制、前端 `embed` 进 Go，倾向**不引入需运行时预装的外部二进制**。
- 生图供应商 `gpt-image-1` 仅支持固定尺寸枚举（见记忆 [[gpt-image-1-fixed-sizes]]），产物尺寸≠请求尺寸是已知约束。
- 裁剪是确定性纯图像处理，不依赖 LLM；对话工具仅用于自然语言入口。
- 异步生成任务靠 `task_created`/`task_*` 事件驱动前端占位与刷新；同步工具（裁剪）当前**无**事件，是 BUG 根因。

## Goals / Non-Goals
- Goals
  - 裁剪支持四种确定性模式，贯通直连端点与对话工具。
  - 图生 icon 作为独立意图，尺寸可指定、默认 150×150。
  - 视频裁剪/抽帧能力上线，后端零新增运行时依赖。
  - 修复对话内同步产物不回填工作区的 BUG。
  - 渠道栏可滚动、图/视频角标配色区分。
- Non-Goals
  - 不做视频转码/特效/多段拼接（仅裁剪片段 + 抽帧）。
  - 不改动生图供应商或固定尺寸枚举约束。
  - 不引入服务端 ffmpeg / cgo 视频库。

## Decisions

### D1: 裁剪模式抽象
在 `crop` 包引入 `Mode` 概念，`CropBytes` 增加模式参数；对外 API（直连端点 + agent 工具）新增可选 `mode` 字段，缺省为 `cover`（向后兼容现有调用）。
- `cover`：现状，铺满目标框后居中裁切。
- `contain`：等比缩放完整放入目标框，留白区域以背景色填充（默认透明，JPEG 退化为白）。不裁掉任何内容。
- `anchor`：铺满后按九宫格方位（top/center/bottom × left/center/right，组合为 9 个锚点）裁切，替代固定居中。请求带 `anchor` 字段。
- `rect`：用户在源图上框选的归一化裁剪区域（`x,y,w,h` ∈ [0,1]），先按该区域裁源图再缩放到目标尺寸。仅前端直连端点使用（对话工具不暴露 rect，因 LLM 无法精确给坐标）。
- Alternatives：把模式做成完全独立端点——否决，参数差异小，单端点加字段更内聚。

### D2: 图生 Icon 走 generation 服务的新意图
新增 `EditKind = "generate_icon"`（或等价意图标识），复用 `generation.Service` 的异步任务/SSE/回填管线，仅新增：
- 一个 icon 专用提示模板（强调：从源图提炼主体、极简、居中、留边、适配小尺寸辨识度、透明或纯色底）。
- 尺寸 slot：用户指定则用之，否则默认 150×150。受 [[gpt-image-1-fixed-sizes]] 约束，**生成阶段**用供应商最接近的合法尺寸出图，再由 `crop` 包以 `contain`/`cover` 收敛到目标 icon 尺寸（icon 默认 `contain` 保证不裁主体），保证最终产物尺寸=目标。
- Alternatives：纯裁剪生成 icon——否决，用户明确要"生成相关 icon"，是再创作而非裁切。

### D3: 视频裁剪/抽帧放在前端（ffmpeg.wasm）
按用户决策，视频裁剪（按起止时间截取片段）与抽帧（取某时间点为图片）在浏览器用 `@ffmpeg/ffmpeg`(wasm) 完成：
- 抽帧产物（图片）与裁剪产物（视频）作为**新资产**经现有上传/资产接口回填工作区，复用既有 kind（图片=`upload` 或新增 `frame`；视频片段=`video`/`upload`）。
- 后端只需保证有一个接收"前端已处理好的二进制 + 元数据"的上传落库路径（可复用现有上传端点）。
- 风险：大文件慢、首次加载 wasm 体积大、移动端内存。Mitigation：限制可处理时长/体积并提示；wasm 按需懒加载；超限时降级提示。
- Alternatives：系统 ffmpeg / 纯 Go 库——按用户选择否决（部署形态优先）。

### D4: 对话内同步产物即时回填（BUG 修复）
根因：`crop_to_sizes`/未来的同步工具直接 `InsertAsset` 但不创建 task，前端只在 `task_done` 时 `refreshWorkspace`，故对话切图后工作区不更新。
方案（二选一，倾向 A）：
- **A（前端为主）**：前端在收到 `tool_result` 且工具为产出资产的同步工具（如 `crop_to_sizes`）时，主动 `refreshWorkspace`。无需后端改协议，最小改动。
- **B（后端广播）**：后端在同步工具落库后通过 hub 广播 `workspace_changed` 事件，前端监听后刷新。更通用但需新增事件类型。
决定：先用 A 修复 BUG；在 `tool_result` 事件已知工具名的前提下足够。若后续有更多同步产出工具，再考虑 B。

### D5: 角标配色
`asset-card.tsx` 中编号角标当前统一 `bg-accent`。改为按媒体类型取色：图 = accent（紫），视频 = 另一中性强调色（如 sky/teal 类，遵循 CLAUDE.md 单主色克制原则，用 accent 的同族变体或一个固定第二色）。仅样式层改动，编号逻辑（`assetLabels`）不变。

## Risks / Trade-offs
- ffmpeg.wasm 体积/性能 → 懒加载 + 时长体积上限 + 明确进度与降级提示。
- icon 二次收敛尺寸引入"生成→裁剪"两步 → 在 generation 管线内串联，对用户表现为单次任务。
- rect 模式仅前端可用，对话工具不支持 → 文档与工具描述中说明，LLM 不暴露该参数。

## Migration Plan
- 裁剪 API 新增字段全部可选、默认 `cover`，旧前端/旧调用无感。
- icon 为新增意图，不影响既有生图意图。
- 视频处理为新增能力，不改既有图生视频。
- 角标配色与渠道栏滚动为纯前端样式增强。

## Open Questions
- icon 默认底色（透明 vs 白）是否需用户可选？暂定透明优先，jpg 退化为白。
- 视频可处理的最大时长/体积阈值取值？实现期按实际 wasm 表现定，先给保守上限（如 ≤60s / ≤100MB）并提示。
