# Change: 裁剪体验升级 + 图生 Icon + 视频裁剪/抽帧

## Why
当前裁剪与媒体加工存在多处体验缺口：渠道栏在渠道多时溢出无法滚动；裁剪只有单一"铺满居中裁"策略，icon/logo 这类不能裁切的素材会被裁坏；用户想要"按图生成 icon"却只能走通用换角色生图、尺寸不可控；视频只能生成不能做基础加工（裁剪片段/抽取封面帧）；在对话里完成的切图产物不经任务通道，工作区需手动刷新才显现；图与视频的编号角标同色，用户难以一眼区分两类产物。

## What Changes
- **裁剪弹窗渠道栏滚动**：左侧渠道列表加 `max-height` 与独立纵向滚动，渠道再多也不撑破弹窗。
- **通用裁剪模式选择**：裁剪提供四种模式——智能铺满（cover，现状默认）、等比留白（contain，不裁切、补背景色）、九宫格锚点（按方位裁切）、手动框选区域（用户在源图上拖框）。模式贯通直连裁剪端点与 agent 裁剪工具。
- **图生 Icon 意图**：新增"为某张图生成相关 icon"能力，**调用图生图大模型**（非纯裁剪）。用户可指定尺寸；未指定时默认 150×150。纳入 agent 工具白名单。
- **视频裁剪与抽帧**：新增视频处理能力——按起止时间裁剪视频片段、从视频抽取指定帧为图片。采用**前端 ffmpeg.wasm** 处理（后端零新增依赖），产物经现有上传通道回填工作区。
- **对话切图产物即时回填（BUG）**：修复在 chat 内通过 `crop_to_sizes` 等同步工具产出的资产不触发工作区刷新、需手动刷新页面才显现的问题。
- **媒体编号角标配色区分**：图N 与 视频N 的角标使用不同配色，使两类产物在工作区一眼可辨。

## Impact
- Affected specs: `image-cropping`、`image-generation`、`video-processing`(新增)、`frontend-experience`、`conversation-orchestration`
- Affected code:
  - 后端：`internal/crop/crop.go`（裁剪模式）、`internal/crop/handler.go` 与 `service.go`（裁剪请求参数）、`internal/agent/tools.go`（crop 工具扩展模式参数、新增 generate_icon 工具）、`internal/generation/`（icon 提示模板与自定义尺寸支持）、`internal/transport`（同步工具产物变更广播）
  - 前端：`web/src/components/workspace/size-picker.tsx`（渠道栏滚动 + 模式选择 + 手动框选）、`web/src/components/workspace/asset-card.tsx` 与 `web/src/lib/timeline.ts`（角标配色）、新增视频处理弹窗组件（ffmpeg.wasm）、`web/src/store/controller.ts`（tool_result 触发工作区刷新）、`web/src/lib/api.ts`（裁剪/icon 请求字段）
