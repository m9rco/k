# Change: 切尺寸升级为「平台 AI 适配」（图生图重绘 + 智能路由）

## Why
本工具定位是**游戏宣发助手**，不是图片裁剪器。把一张主视觉投放到 TapTap、4399、各手机厂商等不同平台的广告位时，真正的诉求是**为该平台重新适配出一张得体的素材**——保留原图主体与核心宣发意图，在目标尺寸/比例下重新组织构图、补全画面、放置安全区，而不是机械地把像素裁掉一块。当前的「切尺寸」是纯裁剪：横竖版翻转或比例差异大时会裁掉主体、出现黑边或主体被切半，宣发质量无法保证。

本变更将「切尺寸」意图的**默认路径**改为调用图生图模型做平台适配重绘，并要求服务端提示词模板把「保留主体与核心意图、适配该平台/尺寸、补全而非裁切」等语义显式表达出来，使所有图生图模型（gpt-image-2 / Gemini 系列）都能准确理解目标。纯裁剪能力作为**确定性快路径与手动兜底**保留。

## What Changes
- **新增 capability `platform-adaptation`**：把一张源图按目标平台尺寸适配为新素材，核心约束是「保留主体与核心宣发意图」。平台适配 **MUST 经由对话 Agent 发起**（让会话模型先理解源图与宣发意图，再驱动图生图重绘），不提供绕过对话模型的独立适配端点。
  - **智能路由**：源图与目标尺寸**宽高比一致**（在容差内、方向相同）时走确定性裁剪/缩放快路径（免费、快、零失真）；**比例差异大或横竖版翻转**时走图生图重绘（AI 适配）。判定对用户透明。
  - **会话级去重**：键 = `(源图资产 id, 目标尺寸 id)`。同一会话内同一张源图适配到同一平台尺寸，**图生图只请求一次**；后续命中直接复用已产出的资产，不重复起任务。**BREAKING**（行为）：去重从「单轮」升级为「会话级」。
  - **尺寸收敛**：图生图供应商只支持固定尺寸枚举时，出图后由纯图像处理收敛到精确平台尺寸（复用现有 icon 的 `contain` 收敛思路）。
  - 复用既有异步任务/SSE 进度/工作区回填/失败重试/多供应商失效切换管线；产物记录其渠道/尺寸归属与来源供应商，沿用既有打包下载分目录策略。
- **修改 `image-generation`**：新增 `EditKind = adapt_platform` 与其服务端提示词模板，模板必须显式覆盖「保留主体与核心宣发意图、适配目标平台/尺寸语义、补全画面而非裁切、尊重该尺寸的文案/留白/安全区约束、颜色与风格协调」，用户文本经结构化 slot + 注入防护承接。
- **修改 `image-cropping`**：纯裁剪从「切尺寸意图的唯一实现」降级为「`platform-adaptation` 的确定性快路径 + 前端手动兜底」。`crop_to_sizes` 工具、直连 crop 端点、前端 cover/contain/anchor/rect 模式**全部保留不变**，仅澄清其在新路由中的定位。
- **修改 `conversation-orchestration`**：能力白名单与「切尺寸」意图描述更新为「平台适配（AI 重绘 + 智能路由）」；新增 `adapt_to_platform` agent 工具承接该意图；意图关键词表保持对「切尺寸/裁剪/适配尺寸/各平台」的命中，但路由到新工具。

## Impact
- Affected specs: `platform-adaptation`(新增)、`image-generation`(修改)、`image-cropping`(修改)、`conversation-orchestration`(修改)
- Affected code:
  - `internal/generation/prompt.go`（新增 `adapt_platform` EditKind + 模板 + slot）
  - `internal/generation/service.go`（适配尺寸入参、尺寸收敛、渠道/尺寸 meta、去重复用）
  - `internal/agent/tools.go`（新增 `adapt_to_platform` 工具；会话级去重接入。平台适配仅经此工具链触达，不开放独立 HTTP 端点）
  - `internal/agent/intent.go` / `internal/agent/prompt.go`（意图路由与能力文案）
  - `internal/crop`（作为快路径被复用；比例一致判定）
  - `web/src/components/workspace/size-picker.tsx`（默认「AI 平台适配」改为向对话 Agent 发消息由 LLM 处理；保留纯裁剪为前端直连手动选项）
  - 配置 `configs/channels.json`（尺寸可携带平台语义提示，供模板使用）
