# Change: 强化「参考图」概念——多图参考适配 / 重试 / 上传即分析

## Why
当前「切尺寸/适配」把多选资产当作 **M 张源图各自 × N 个尺寸（M×N 产物）** 的纯批量裁剪，未把这批选中图当成一个**统一的参考组**喂给生图与视觉模型；视觉分析仅在适配时对**单张源图**懒触发；已成功的产物没有「重试」入口。这让「多张参考图共同定调、一次产出各平台尺寸」这一高频宣发诉求无法表达，也无法复用已上传图的分析结论。

## What Changes
- **多参考图平台适配（参考组）**：选中 1~16 张图发起「尺寸适配」时，将其作为**有序参考组**（第一张为锚点/内容真相源，其余为风格元素辅助），对**每个目标尺寸产出恰好一张**适配图（产物数 = 尺寸数，而非 M×N）。整组图同时作为参考喂给 `gpt-image-2`（图生图）与 `grok-4-fast`（视觉分析）。
- **批量切尺寸语义变更（BREAKING）**：工作区多选 + 批量切尺寸由「每张源图各自切到每个尺寸（M×N）」改为「选中集作为参考组 → 每尺寸一张（N）」。**BREAKING**：同样的多选操作产物数量与含义改变。单张选择维持既有智能路由（比例一致裁剪 / 差异大 AI 重绘）。
- **已生成产物重试**：在任意**已成功**的生成/适配产物上提供「重试」入口，按原始生成流程与参数重跑，结果作为**新条目**回填工作区，原图保留。需为产物持久化其生成来源（流程类型 + 参数）；无来源记录的资产（如纯上传图）不显示重试。
- **上传即分析（按 md5 预热）**：每张**新上传**图片在上传后**异步**发布到 COS（md5 去重）→ 调 `grok-4-fast` 分析 → 按 md5 写入 `vision_reports` 缓存，使后续适配直接命中、无需再次分析。尽力而为、不阻塞上传响应；COS/vision 未配置时静默跳过。

## Impact
- Affected specs: `platform-adaptation`（MODIFIED 智能路由 + ADDED 多参考图适配）、`asset-workspace`（MODIFIED 批量切尺寸 BREAKING + ADDED 产物重试）、`marketing-analysis`（ADDED 上传即分析）、`frontend-experience`（ADDED 多参考图尺寸适配入口 + 已生成图重试入口）
- Affected code:
  - `internal/agent/tools.go`：`adaptArgs`/`newAdaptTool`/`visionThemeReport` 贯通参考组（多图发布 + 分析 + 注入）
  - `internal/generation/adapt.go`：`AdaptToPlatform` 增参考组入参，≥2 张一律 AI 重绘并把整组作为参考；`GenerateParams.ReferenceAssetIDs` 透传
  - `internal/workspace/workspace.go`：`handleUpload` 触发异步分析预热
  - `internal/store/store.go`：产物生成来源持久化（重试所需），复用 `vision_reports`
  - 前端 `web/static/`：多选切尺寸走参考组适配、参考张数标注、产物重试按钮
- 复用既有能力，不新增模型/供应商：参考图构图复用契约（image-generation 1~16 锚点）、`reference-publishing` 的 `UploadIfAbsent` md5 去重、`vision_reports` 全局缓存、异步任务/SSE/工作区回填管线。
