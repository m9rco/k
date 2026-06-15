# Tasks: 平台 AI 适配

## 1. 生图侧：adapt_platform 意图与提示词模板
- [ ] 1.1 在 `internal/generation/prompt.go` 新增 `EditKind = adapt_platform` 常量
- [ ] 1.2 扩展 `Slots`，承接平台适配所需上下文：目标 `ChannelName`/`AssetTypeName`/`Orientation`/`Width`/`Height`/`SizeNote` 与可选用户描述（全部经 `Sanitize`）
- [ ] 1.3 在 `BuildPrompt` 新增 `adapt_platform` 分支，模板显式覆盖：保留主体与核心宣发意图、为目标平台尺寸重组构图、补全而非裁切、透传尺寸语义约束（无文案/仅 logo/圆角/透明底/安全区）、复用 palette + harmony 协调约束；用户文本仅作 sanitized slot 片段
- [ ] 1.4 表驱动单测：各槽位正确填入、注入串被剥离、空源图描述不阻断（适配以源图为主参考）、note 透传

## 2. 生图侧：尺寸入参、收敛与产物归属
- [ ] 2.1 `GenerateParams` 增加目标渠道/尺寸归属（channelId/sizeId）与目标平台宽高，供 `run` 收敛与落库 meta 使用
- [ ] 2.2 `service.run` 对 `adapt_platform` 走「provider 出图 → `crop.CropBytesWithOptions(ModeContain)` 收敛到精确平台尺寸」（复用 icon 范式）
- [ ] 2.3 适配产物落库 `Meta` 写入 `{channelId, sizeId, sizeName, sourceAssetId, via:"ai"}`，与 `crop.CropMeta` 结构对齐
- [ ] 2.4 单测：收敛后落库尺寸 == 目标平台尺寸；meta 字段完整

## 3. 智能路由与会话级去重
- [ ] 3.1 在 generation 服务新增「适配一个 (源图,尺寸) 」的入口：先按宽高比 + 方向判定走裁剪快路径还是 AI 重绘（容差常量 `ratioTolerance=0.04`）
- [ ] 3.2 快路径调用 `crop.CropToSizes`（cover），产物 meta 标 `via:"crop"`，与 AI 产物归属结构一致
- [ ] 3.3 会话级去重：入口先查 store —— session 内存在 `parentId==源图 且 meta.sizeId==目标尺寸` 的成功产物则复用其 assetId、不起新任务（覆盖跨轮 + 进程重启）
- [ ] 3.4 单测：比例一致→crop 路径、横竖翻转→AI 路径、同 (源图,尺寸) 二次请求复用不重复起任务、不同尺寸/源图各自独立

## 4. Agent 工具与意图路由
- [ ] 4.1 `internal/agent/tools.go` 新增 `adapt_to_platform` 工具：入参源图 id（或 reference_asset_ids）+ 一组目标尺寸 id；按尺寸分别走路由；保留单轮 `turnCallGuard`
- [ ] 4.2 工具 description 写明「平台适配（保留主体/意图，智能路由 AI 重绘或裁剪），切尺寸/适配尺寸的默认实现」，触发词覆盖 切尺寸/裁剪/适配尺寸/各平台/广告位
- [ ] 4.3 `internal/agent/intent.go`：「切尺寸」规则的 `tool` 改指 `adapt_to_platform`，关键词不变；`internal/agent/prompt.go` 能力文案与 `Capabilities` 的「切尺寸」描述更新为「平台适配（AI 重绘 + 智能路由）」
- [ ] 4.4 `crop_to_sizes` / 直连 crop 端点 / `list_platform_sizes` 保持不变（手动兜底与快路径复用）
- [ ] 4.5 单测：意图命中路由到新工具、缺尺寸时澄清、单轮重复 call 去重

## 5. 前端
- [ ] 5.1 `web/src/components/workspace/size-picker.tsx`：默认「开始适配」走平台适配（AI 智能路由）；保留「手动裁剪」入口走既有纯裁剪（cover/contain/anchor/rect 不变）
- [ ] 5.2 AI 路径走异步任务：插入占位骨架、订阅 SSE 进度、完成回填；快路径/手动裁剪即时回填（沿用现有 crop 调用）
- [ ] 5.3 产物数 > 6 的前置打包提示沿用既有逻辑，AI 与裁剪产物归属一致无需区分
- [ ] 5.4 文案与交互符合项目 UI/UX 规范（透明路由、不暴露内部分流细节）

## 6. 配置与文档
- [ ] 6.1 确认 `configs/channels.json` 各尺寸的 `note`/`orientation` 足以驱动模板平台语义（必要时补充语义备注，不改 schema）
- [ ] 6.2 `openspec validate add-platform-adaptation --strict` 通过
- [ ] 6.3 `go test ./...` 与 `go vet ./...` 通过；前端构建通过
