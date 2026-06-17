# Tasks: gemini-vision-and-adapt-quality-gate

## 1. Gemini 视觉分析（inline，去 COS 硬依赖）

- [ ] 1.1 在 `internal/vision/` 抽象 `Analyzer` 接口（`Configured()` + `Analyze(ctx, images, onChunk)`），现有 grok 实现改为接口实现之一
- [ ] 1.2 新建 `internal/vision/gemini_analyzer.go`：用 Gemini `:generateContent` + `inlineData` 传 base64 图片字节，复用 `generation/gemini.go` 的 baseURL 规整与凭证逻辑，响应取 `candidates[].content.parts[].text`，支持流式 onChunk
- [ ] 1.3 `internal/config/config.go` 增加 `VisionConfig`（provider/base/key/model，三层回退；默认 provider=gemini、model=gemini-2.5-flash-all）；保留 `VisionCredential()` 兼容
- [ ] 1.4 `cmd/server/main.go` 据 `VISION_PROVIDER` 选型：gemini→inline 分析器，openai→现有 image_url 分析器；日志反映所选模型
- [ ] 1.5 `internal/agent/tools.go` 的 `visionThemeReport`：当分析器为 inline 模式时，直接传图片字节而非走 COS 上传；COS 仅用于预热与 reanalyze（缺省时不阻塞 inline 分析）
- [ ] 1.6 单测：gemini 分析器请求体含 inlineData、响应解析取 text、流式累积、错误降级返回 ("", err)

## 2. 适配后质量打分门控（doubao judge + 审核态可视化 + 重生1次封顶）

- [ ] 2.1 新建 `internal/vision/quality.go`：`QualityChecker`，OpenAI-compat `/chat/completions`，产物字节转 data URI 传 image_url，固定 judge prompt（携带渠道/尺寸/themeReport），非流式，解析 `{compliance{pass,violations}, scores{...}, total, hints}` JSON；服务端判定：合规 false→一票否决不及格，否则 total<阈值→不及格；解析失败/超时(30s)/未配置 → 及格
- [ ] 2.2 `internal/config/config.go` 增加 `Quality ImageProviderConfig`（QUALITY_* 三层回退，默认 model=doubao-seed-1-6-vision-250815）+ `QualityThreshold int`（QUALITY_THRESHOLD，默认 75）；APIKey 空 → 门控禁用
- [ ] 2.3 `internal/transport/event.go`：新增审核态事件类型 `review_started`/`review_passed`/`review_failed`/`review_skipped`（加法式，旧客户端忽略，非终止）
- [ ] 2.4 `internal/generation/service.go`：`GenerateParams` 增 `Attempt int`（0=首次,1=重生）与 `QualityHints string`
- [ ] 2.5 `service.go run()`：`EditAdaptPlatform` 收敛后、持久化前，若 judge 已配置且 `Attempt==0` → 推 review_started → 调 judge → 及格推 review_passed 持久化；不及格推 review_failed → 把 hints+红线原因注入 prompt、**复用同一 taskID** 带 `Attempt=1` 重走出图+收敛 → 重生产物不再审核直接持久化；judge 异常/未配置 → 推 review_skipped 按原产物持久化
- [ ] 2.6 `internal/generation/prompt.go`：`QualityHints` 非空时注入图生图提示（PRESERVE 段后，与 themeReport 同区作额外强约束），反馈喂回 gpt-image-2
- [ ] 2.7 wire `QualityChecker` 到 generation service（main.go `SetQualityChecker` + 阈值）
- [ ] 2.8 单测：合规红线一票否决、total<阈值触发重生、重生封顶仅一次、未配置/超时降级及格、hints 注入 prompt、审核态事件按序下发且非终止

## 3. 前端审核态可视化

- [ ] 3.1 `web/src/lib/types.ts`：`Task` 增审核子态字段（如 `review?: "checking"|"passed"|"failed"`、可选 `reviewReason?`）
- [ ] 3.2 `web/src/store/controller.ts`：`applyTaskEvent` 处理 4 个审核态事件，在同一 taskID 占位卡片上更新 review 子态，不新增/切换卡片；review_failed 后回生图中态等重生 task_done
- [ ] 3.3 工作区占位卡片组件：渲染 审核中 🔍 / 通过 ✓ / 按建议重绘中 ✗ 的轻量标记，不展示分数细节；事件缺失/降级回退既有「生图中」表现
- [ ] 3.4 SSE 订阅事件名列表补充 4 个审核态事件

## 4. 校验与文档

- [ ] 4.1 `go build ./...`、`go test ./...`、前端 `npm run build`/typecheck 通过
- [ ] 4.2 更新 `openspec/project.md` 模型清单（vision=gemini-2.5-flash-all、quality=doubao-seed-1-6-vision-250815）
- [ ] 4.3 `openspec validate gemini-vision-and-adapt-quality-gate --strict` 通过
