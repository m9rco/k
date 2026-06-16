# Tasks

## 1. cos_uploads 持久缓存表 + md5 发布 helper
- [ ] 1.1 `internal/store/store.go`：新增 `cos_uploads` 表（`md5 TEXT PK, url TEXT, content_type TEXT, created_at DATETIME`），新增 `GetCOSUpload(md5)` 与 `InsertCOSUpload(md5, url, contentType)` 方法
- [ ] 1.2 `internal/cos/cos.go`：新增 `UploadIfAbsent(ctx, data, contentType, store)` 方法：算 md5 → 查表 → 命中则返回缓存 URL，未命中则 `Upload`（以 `refs/{md5}{ext}` 为键）后写表
- [ ] 1.3 单测：命中缓存不调 COS、未命中调 COS 并写缓存、跨会话复用

## 2. 视觉分析适配器（vision HTTP 客户端）
- [ ] 2.1 新增 `internal/vision/` package，实现 `Analyzer`：向 `{base}/chat/completions` 发 OpenAI 兼容多模态请求（`image_url` content parts），`stream:true`，chunk 通过回调/channel 逐片返回
- [ ] 2.2 分析指令常量：固定服务端文案，声明「游戏宣发素材主题分析」，只描述图里确有的要素，输出核心主题/主体/卖点/风格/配色/绝不可丢失要素/各尺寸适配注意点，指令中英文均可（关键约束英文短语更能被图生图模型利用）
- [ ] 2.3 单测：mock HTTP server 验证多模态请求结构（text + image_url parts 存在且正确）

## 3. grok-4-fast 模型目录项 + Orchestrator 接入
- [ ] 3.1 `internal/config/catalog.go`：新增 `SceneAnalysis ModelScene = "analysis"`，目录项 `{ID:"grok-4-fast", Scene:SceneAnalysis, Provider:"vision", 经 COMMON 凭证}`
- [ ] 3.2 `Orchestrator` 接入：参照 `optimize.go` 新增 `AnalyzeReferences(ctx, imageURLs) (report string, err error)`，内部调 `vision.Analyzer`，流式 chunk 经 hub 发 `EventMessage`
- [ ] 3.3 进程内分析报告缓存（按 URL 集指纹，simple `sync.Map` + 上限 LRU 或 `expvar`），命中返回缓存并通知 chat 「复用已有分析」
- [ ] 3.4 凭证未配置时 `Configured()` 返回 false，`AnalyzeReferences` 返回 `ErrNotConfigured`，调用方据此降级

## 4. AdaptToPlatform 编排：三阶段前置流程
- [ ] 4.1 `internal/generation/adapt.go`（或新 `adapt_pipeline.go`）：判断本次请求是否含 AI 重绘尺寸（已有 `aspectClose` 逻辑），含则编排：发布 → 分析 → 注入
- [ ] 4.2 **阶段 1**：对每个 source asset 字节调 `cos.UploadIfAbsent`；通过 orchestrator 事件出口发 `EventMessage`「正在发布 N 张参考图…」/ 「参考图已就绪」
- [ ] 4.3 **阶段 2**：以 URL 列表调 `Orchestrator.AnalyzeReferences`，流式 `EventMessage` 报告；完整报告字符串留存供阶段 3
- [ ] 4.4 任一阶段 `err == ErrNotConfigured` 或其他错误：跳过、发 `EventMessage`「主题分析不可用，按默认适配」，report = ""
- [ ] 4.5 **阶段 3**：`report != ""` 时调 `BuildPrompt` 前注入主题约束（`Slots.ThemeReport` 新字段），在 `prompt.go` 的 PRESERVE 段之后插入；`report == ""` 时行为与现有完全一致
- [ ] 4.6 `GenerateParams` 新增 `ThemeReport` 字段；`BuildPrompt` PRESERVE 段之后插入 `THEME:` 报告浓缩（限长，Sanitize）
- [ ] 4.7 `adapt_to_platform` 工具：ToolDeps 透传 cos uploader + vision analyzer，编排在 `Start` 前执行

## 5. 测试
- [ ] 5.1 `cos` 单测：UploadIfAbsent 命中/未命中路径
- [ ] 5.2 `vision` 单测：mock server 验多模态请求体格式；报告缓存命中不重调
- [ ] 5.3 `generation/prompt` 单测：ThemeReport 非空时 THEME 段出现；空时不出现
- [ ] 5.4 `adapt_pipeline` 单测：stub cos+vision，分析失败时仍适配（降级路径）；纯裁剪路径不触发发布/分析

## 6. 验证
- [ ] 6.1 `go build ./...` + `go test ./...` 全绿
- [ ] 6.2 `gofmt` 无 diff
- [ ] 6.3 `openspec validate add-vision-guided-adaptation --strict` 通过

## 依赖说明
实现需等 `harden-gpt-image-2-harness` 归档后进行（任务 4.5/4.6 基于四段式 harness 的 PRESERVE 注入点；若先实现需临时兼容旧提示格式，成本更高）。
