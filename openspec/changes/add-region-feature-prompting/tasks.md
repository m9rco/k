## 1. 后端：选区特征描述能力
- [x] 1.1 在 `internal/vision` 新增固定的「局部特征精确描述」指令常量（只描述框内确有要素、不虚构、结构化字段：主体类别/外观材质颜色/可见文字/相对位置/必须保留），与既有 `analysisPrompt` 同源防注入策略
- [x] 1.2 在 `vision.Analyzer` 增加 `DescribeRegion` 入口（两实现共用 prompt 注入），走 inline base64 路径（`NeedsPublicURL()=false`），COS 未配置也可用
- [x] 1.3 在 `internal/crop` 新增 `RegionBytes`：按归一化 bbox 裁出选区像素（不缩放），输出选区 PNG/JPEG 字节
- [x] 1.4 新增 `POST /api/session/{id}/assets/{assetId}/describe-region` handler（挂 workspace 路由，main 注入 crop+vision）：取资产原图 → 按 bbox 裁选区 → 调 1.2 → 返回结构化特征文本；视觉不可用时返回 `{available:false}` 降级信号
- [x] 1.5 表驱动单测：RegionBytes 裁剪正确/越界拒绝、DescribeRegion 用固定 prompt、handler 503/400/200 三态

## 2. 后端：edit_image 接受可选 region 语境
- [x] 2.1 `internal/agent/tools.go` 的 `editArgs` 新增可选 `region_desc`（sanitized slot），jsonschema 描述其语义（仅作选区主体限定，不替换主体身份）
- [x] 2.2 `internal/generation/prompt.go` 在 MODIFY 段加入选区限定句（"Apply this change ONLY to the selected region subject … keep every other region unchanged"）；AVOID 补"Do NOT modify pixels outside the selected region or alter any other subject"
- [x] 2.3 不新增 provider 参数（未动 `http_provider.go` mask 形态），纯提示层；四段式骨架对 OpenAI 兼容与 Gemini 适配器一致表达
- [x] 2.4 prompt 单测：含 region_desc 时含选区限定句与保留约束 + 注入被剥离；无 region_desc 时与现状一致

## 3. 前端：预览 modal 智能选区
> 架构调整记录：实现阶段放弃了浏览器端 SAM（SlimSAM/transformers.js + 23MB onnxruntime wasm + 模型权重 embed）。改为**零模型依赖的「点选 → 后端视觉定位」**方案：前端只把归一化点击坐标发给后端，后端视觉模型（gemini inline）看全图 + 点坐标，一次返回该物体的包围盒 + 结构化特征描述（见 `internal/vision/region.go`）。比 SAM 方案更轻：无需引入前端依赖、无需 vendored 权重、无需 worker，客户端瞬时响应不卡 UI。3.1/3.2 的 SAM 路径不再适用，下列条目按真实实现修订。
- [x] 3.1 ~~引入 `@huggingface/transformers` + SAM 权重同源 embed~~ → **不引入任何前端模型依赖**；`web/package.json` 无 transformers，`web/static` 产物纯由 `vite build` 生成（无 wasm/权重/worker）
- [x] 3.2 ~~`use-smart-select.ts` 浏览器端 SlimSAM encoder/decoder~~ → **改为后端定位**：`POST describe-region` 传 `{px,py}`，`internal/vision/region.go` 的 `LocateAndDescribe` 用固定 `pointRegionPromptTmpl` 让视觉模型定位点中物体并产出 box + 特征（gemini inline / openai image_url 双实现，注入防护同 `analysisPrompt`）
- [x] 3.3 `region-selector.tsx`：点选物体（点击发坐标，后端定位后 `resultBox` 回填使框吸附到物体）+ 手动矩形框选（零依赖降级路径）+ bbox/点标记可视化
- [x] 3.4 `lightbox.tsx` 集成"圈定图层"按钮 → 进入选区模式 → 选定后调 `describe-region` → 结构化特征回填到可编辑 textarea
- [x] 3.5 组装并发送：以"只修改【选区主体：<特征>】，其余画面保持不变。修改要求：…"句式调用既有 `sendMessage`（透传 region 语境）
- [x] 3.6 视觉不可用（未配置/低置信）时 `{available:false}` 降级；用户始终可切手动框选；描述不可用退回纯文本二次调整，全程不阻塞

## 4. 集成与验收
- [x] 4.1 `go build ./...` 与 `go test gameasset/internal/{vision,crop,generation,workspace,agent}` 全绿（agent 包仅 1 个**预存在**失败 `TestToolsBuildWhitelist`，在干净树上同样失败，与本次无关）
- [x] 4.2 前端 `npm run build` 通过、产物落 `web/static` 供 embed（纯 `index.js`/`index.css`，无 SAM 权重/wasm/worker——后端定位方案不需要）。`web/static/*` 已 gitignore，仅保留 `.gitkeep` 占位（`//go:embed all:static` 在干净检出未跑前端时也能编译）；产物由 `make web` / `npm run build` 在构建时重新生成。点选→后端定位→描述→改图链路：后端 `TestDescribeRegionPointMode` 等单测覆盖，浏览器端交互待真人点验
- [x] 4.3 `openspec validate add-region-feature-prompting --strict` 通过
