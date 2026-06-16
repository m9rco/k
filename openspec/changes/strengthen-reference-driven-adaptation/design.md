# Design: 强化参考图概念

## Context
现有链路：前端多选 → 消息以 `[adapt sizes: ...]` 前缀触发 → agent 调 `adapt_to_platform(source_asset_id, size_ids)` → `visionThemeReport(单张源图)` → `AdaptToPlatform(单源图, sizes)` → 每尺寸独立路由（裁剪快路径 / AI 重绘）。`adaptArgs` 已声明 `reference_asset_ids` 但被降级为「取第一张当 source」。本变更把「参考组」做成一等概念，贯通分析、生图、产物语义，并补齐重试与上传预热。

## Goals / Non-Goals
- Goals：多图参考组适配（每尺寸一张）、产物重试（新增）、上传即按 md5 分析预热。
- Non-Goals：不引入新模型/供应商；不改收敛分档与尺寸映射；不改单图适配的既有智能路由；不做重试的「原地替换」语义（已确认走新增）。

## Decisions

### D1. 参考组适配的产物基数与锚点
- 多选 M 张 + 选 N 尺寸 → **产物数 = N**（非 M×N）。每张产物以整组为参考。
- 参考组**有序**，**第一张为锚点**（内容/主体/核心宣发意图的唯一真相源，驱动 parent 链接、尺寸继承、调色板），其余为辅助（风格/配色/元素/构图灵感，MUST NOT 改写锚点主体）。复用 image-generation 既有「1~16 锚点契约」，>16 截断并提示。
- **路由收紧**：参考组 ≥2 张时**一律走 AI 重绘**——确定性裁剪快路径无法纳入辅助参考，跳过它才能让多图真正参与。单张选择维持既有 `aspectClose` 智能路由。

### D2. 多图贯通分析与生图
- `visionThemeReport` 由「单张源图」扩展为「锚点 + 辅助整组」：逐张 `UploadIfAbsent`（md5 去重）得到 URL 列表，`VisionAnalyzer.Analyze(urls, …)`（既有签名已收 `[]string`）。组级报告按既有「有序 URL 列表内容指纹」进程内缓存复用。
- `AdaptToPlatform` 新增 `referenceAssetIDs []string` 入参；AI 重绘分支把整组透传到 `GenerateParams.ReferenceAssetIDs`，由既有图生图参考图复用链路喂给 gpt-image-2。锚点仍为 parent/尺寸继承来源。

### D3. 上传即分析（预热）vs 适配时分析
- 二者**同一缓存**（`vision_reports` 按 md5），不同触发时机：
  - 上传：`handleUpload` 成功落库后 **fire-and-forget** 一个 goroutine，对该单图做 publish→analyze→`InsertVisionReport(md5)`。不阻塞上传 HTTP 响应；失败仅日志。
  - 适配：`visionThemeReport` 命中单图 md5 缓存即跳过 grok（已实现）；组级报告走进程内组指纹缓存。
- 单图预热与组级分析并存：预热保证「下次单图适配秒回」，组级分析捕捉多图关系。不做跨二者的合并，避免过度设计。
- 降级：COS/vision 未配置时预热静默跳过（与适配路径一致的优雅降级）。

### D4. 重试的来源持久化
- 重试需「按原流程重跑」，故每个**生成类产物**落库时持久化其生成来源：流程类型（edit_image / adapt_to_platform / icon / text2image …）+ 关键参数（source/reference ids、size_id、intent、sanitized slots）。
- 选型：在 assets 表新增一列 `gen_origin`（JSON 文本，nullable），随产物写入。纯上传/裁剪快路径产物无 AI 流程来源 → 该列为空 → 前端不显示重试。
- 重试动作：前端「重试」→ 后端按 `gen_origin` 重组同一工具调用 → 异步执行 → 新产物回填，原图不动（与「再次请求即再次执行」一致，结果天然非确定）。

### D5. 批量切尺寸语义变更（BREAKING）
- `asset-workspace` 的「工作区批量切尺寸」由 M×N 纯裁剪改为「参考组→每尺寸一张」。这是用户可见的产物数量/含义变更，标 BREAKING。
- 单张多选 = 单图适配（既有行为）；2 张及以上 = 参考组适配。

## Risks / Trade-offs
- **成本**：上传即分析对每张新图触发一次 grok + COS。缓解：md5 去重（重复内容零成本）、异步不阻塞、未配置则跳过。若团队上传量大可后续加开关，本期默认开启（内部小团队，可接受）。
- **BREAKING**：老用户对「多选切尺寸」的 M×N 预期改变。缓解：前端确认区明确标注「N 张参考 → 每尺寸一张」。
- **gen_origin 膨胀**：JSON 列存参数。缓解：仅存重跑所需最小字段，不存图像数据。

## Migration
- `gen_origin` 为新增 nullable 列，历史资产为空 → 不显示重试，无需回填。
- 进程内组级报告缓存重启失效可接受（既有约定）。
