# Tasks: fix-compression-tooling-and-adapt-model

## Part A：诊断 + 根因修复（压缩后工具调用退化）

- [x] A1. 在 `Window` 引入 `hasEverCalledTool bool`；`Append` 时若消息含 `ToolCalls` 则置 true；`ResetContext` 重建窗口后自然重置（新 `Window` 初始 false）。
- [x] A2. 实现 `recentHasToolExchange(msgs []*schema.Message) bool` + `Window.HasToolExchange()`。
- [x] A3. 在 `compressLocked` 折叠完孤立消息后加入"工具就绪 back-off"：若 `hasEverCalledTool && !recentHasToolExchange(recent[foldCount:])` 则向 recent 方向后退 foldCount；找不到可保留位置（foldCount 到 0）时**恢复原始值继续压缩**（best-effort，不阻塞）。
- [x] A4. 表驱动测试（`window_test.go`）：`TestCompressPreservesToolExchange`（有工具历史，压缩后 recent 仍含工具交换）、`TestCompressChatOnlyNoConstraint`（纯聊天不受约束）；原 `TestCompressNoOrphanToolMessage` 保留。
- [x] A5. 扩充 `Handle` 的 turn-end 诊断日志：加 `model`、`compressed`、`has_tool_exchange` 字段。

## Part B：再次适配不再静默跳过

- [x] B1. 移除 `generation.AdaptToPlatform` 中的 `findAdapted` 会话级去重调用——每次适配请求都真正发起裁剪/AI 重绘。连带删除死代码：`findAdapted` 函数、`AdaptViaReused` 常量、孤儿 `encoding/json` import。
- [x] B2. System Prompt 核心规则1 加通用约束「再次请求即再次执行」：历史完成过的操作不代表本轮无需再做，用户再次发起命中能力的请求就必须再次调用工具，禁止以"之前已做过/产物已在工作区/可查看图N"为由跳过。
- [x] B3. System Prompt 规则14（平台适配）加专门约束：同图同尺寸再次发起也要重新调 `adapt_to_platform`。
- [x] B4. 更新测试：`TestAdaptSessionLevelDedup` → `TestAdaptReRequestRegenerates`（再次请求生成新产物、新 task/asset id）；删除已失效的 `TestAdaptDifferentSizeNotDeduped`。
- [x] B5. `go build ./... && go test ./internal/agent/... ./internal/generation/...` 全绿。

## Part C：适配请求级 Gemini 路由

- [x] C1. `ToolDeps` 加 `AdaptModelOverride *config.ImageProviderConfig` + `adaptProvider(d)` helper（优先 AdaptModelOverride，否则 ImageOverride）。
- [x] C2. `Handle` 构建 ToolDeps 时注入：`o.cfg.ResolveImageModel(SceneImage, "gemini-3-pro-image")`，`ok` 为真（凭证就绪，IsModelAvailable 已校验 ImagePrimary.APIKey 非空）即设 `deps.AdaptModelOverride = &pc`；不可用则保持 nil（回退 ImageOverride / 服务默认）。
- [x] C3. `newAdaptTool` 的 AI 重绘路径用 `adaptProvider(d)` 取 provider。
- [x] C4. 关键认知修正：image 场景所有模型共用 `sceneCredential(SceneImage)`（ImagePrimary base/key），gemini-3-pro-image 解析为 `Provider=gemini`＋ImagePrimary 凭证；空工作区的唯一根因是 B1 的去重，非此路由。

## 验证

- [x] 单测全绿（`gameasset/internal/agent`、`gameasset/internal/generation` ok）。
- [ ] 人工验证：压缩阈值后再发图生图意图，日志 `has_tool_exchange=true` 且工具被调用。
- [ ] 人工验证：对已适配过的图再次发起同尺寸适配，工作区出现新产物（不再静默跳过）。
- [ ] 人工验证：AI 适配任务日志 provider 为 Gemini（`gen.run: ... DONE` 且产物 provider=google/gemini）。
