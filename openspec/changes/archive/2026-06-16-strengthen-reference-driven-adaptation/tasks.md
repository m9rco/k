# Tasks: 强化参考图概念

## 1. 后端：参考组贯通适配
- [x] 1.1 `AdaptToPlatform` 增 `referenceAssetIDs []string` 入参；锚点 = 参考组第一张（或 source）；校验/截断 ≤16 张
- [x] 1.2 路由收紧：参考组 ≥2 张时跳过 `aspectClose` 快路径，一律走 AI 重绘
- [x] 1.3 AI 重绘分支把整组透传到 `GenerateParams.ReferenceAssetIDs`，由既有参考图复用链路喂给 gpt-image-2
- [x] 1.4 `newAdaptTool`/`adaptArgs`：把 `reference_asset_ids` 作为参考组传入（不再仅取第一张当 source）；更新工具描述
- [x] 1.5 表驱动单测：N 尺寸 + M 参考 → N 个 outcome；≥2 张强制 AI；>16 截断

## 2. 后端：参考组发布与分析
- [x] 2.1 `visionThemeReport` 扩为整组：逐张 `UploadIfAbsent` 收集 URL 列表 → `VisionAnalyzer.Analyze(urls,…)`
- [x] 2.2 组级报告按「有序 URL 列表指纹」进程内缓存复用；单图 md5 命中仍跳过
- [x] 2.3 降级：发布/分析任一不可用 → 空报告、标准适配；chat 提示不变
- [x] 2.4 单测：多图 URL 收集、组指纹缓存命中、降级路径

## 3. 后端：上传即分析预热
- [x] 3.1 `handleUpload` 落库成功后 fire-and-forget goroutine：publish→analyze→`InsertVisionReport(md5)`
- [x] 3.2 上传响应不被阻塞；md5 已缓存则跳过；COS/vision 未配置静默跳过；失败仅日志
- [x] 3.3 单测：预热触发、md5 命中跳过、未配置降级、失败不影响上传成功

## 4. 后端：产物生成来源持久化（重试）
- [x] 4.1 assets 表新增 nullable `gen_origin`（JSON：流程类型 + 重跑关键参数，无图像数据）
- [x] 4.2 各生成流程落库时写入 `gen_origin`；裁剪快路径/上传不写
- [x] 4.3 重试入口/端点：按 `gen_origin` 重组同一工具调用 → 异步执行 → 新产物回填、原图保留
- [x] 4.4 单测：来源写入/读取、按来源重跑、无来源不可重试

## 5. 前端：多参考图尺寸适配
- [x] 5.1 多选 ≥2 发起切尺寸 → 走参考组适配（有序，第一张主参考）
- [x] 5.2 尺寸选择/确认界面标注「以 N 张参考图适配，每尺寸一张」
- [x] 5.3 单张选择维持既有单图适配语义

## 6. 前端：已生成图重试入口
- [x] 6.1 成功的 AI 产物卡展示「重试」入口；无 `gen_origin` 的资产不展示
- [x] 6.2 点击重试 → 调重试端点 → 占位/进度 → 新产物回填、原图保留
- [x] 6.3 与失败重试一致的视觉/交互（design token + 200ms 过渡）

## 7. 验证
- [x] 7.1 `go build ./...` 与 `go test ./...` 全绿
- [ ] 7.2 端到端：多选参考组适配产物数 = 尺寸数；上传后适配命中预热；成功产物重试出新图
- [x] 7.3 `openspec validate strengthen-reference-driven-adaptation --strict` 通过
