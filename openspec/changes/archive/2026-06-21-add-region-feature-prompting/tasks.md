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
> 架构记录（两层选区，最终态）：**首选浏览器端 SAM 描轮廓**——点选物体即在浏览器内用 SlimSAM 分割出该物体掩码、描出轮廓（用户要的「图层」观感），取掩码紧致 bbox 走 rect 描述链路。**降级为后端定位**——SAM 权重缺失/wasm 初始化失败/掩码为空时，退回 `internal/vision/region.go` 的点选定位（视觉模型看全图+点坐标返回 box+描述），再退回手动矩形框选。早期曾短暂改为「纯后端定位、无前端模型」，但后端只能给矩形包围盒、给不出像素级轮廓，无法满足描边需求，故回到 SAM 方案并保留后端定位作为降级。
- [x] 3.1 `web/package.json` 引入 `@huggingface/transformers@4.2.0`；SAM fp16 权重 + onnxruntime-web wasm 由 `web/scripts/fetch-models.mjs` 同源 vendored 到 `web/public/{models,ort}/`（curl 优先尊重代理），随 vite 拷进 `web/static` embed；缺权重自动降级不阻塞
- [x] 3.2 `use-smart-select.ts` + `smart-select.worker.ts`：Web Worker 内懒加载 SlimSAM（`allowRemoteModels=false` + `localModelPath=/models/` + `wasmPaths=/ort/` 全同源、单线程免 COOP/COEP），图片载入跑一次 vision encoder 缓存 embedding，点击触发轻量 mask decoder，worker 内取最高 IoU 掩码、算紧致 bbox + 描边 RGBA 叠层（转移所有权零拷回主线程）；任何失败 → `unavailable` 降级
- [x] 3.3 `region-selector.tsx`：点选物体（SAM 就绪→描轮廓+用掩码 bbox 走 `onRect`；不可用→`onPoint` 后端定位）+ 手动矩形框选（零依赖降级）+ canvas 轮廓/矩形叠层（object-contain letterbox 对齐）
- [x] 3.4 `lightbox.tsx` 集成"圈定图层"按钮 → 进入选区模式 → 选定后调 `describe-region` → 结构化特征回填到可编辑 textarea
- [x] 3.5 组装并发送：以"只修改【选区主体：<特征>】，其余画面保持不变。修改要求：…"句式调用既有 `sendMessage`（透传 region 语境）
- [x] 3.6 SAM 加载中/失败有清晰状态提示；权重缺失/算力不足自动切后端定位或手动框选；描述不可用退回纯文本二次调整，全程不阻塞

## 4. 集成与验收
- [x] 4.1 `go build ./...` 与 `go test gameasset/internal/{vision,crop,generation,workspace,agent}` 全绿（agent 包仅 1 个**预存在**失败 `TestToolsBuildWhitelist`，在干净树上同样失败，与本次无关）
- [x] 4.2 前端 `npm run build` 通过、产物落 `web/static` 供 embed。SAM fp16 权重（vision_encoder 12.2MB + prompt_encoder_mask_decoder 8.55MB）+ onnxruntime-web wasm（asyncify 23.5MB + plain 12.9MB 两变体）由 `fetch-models.mjs`（prebuild 钩子 / `make models`）同源 vendored 到 `web/public/{models,ort}`，随 vite 拷进 `web/static`；vite 插件 `dropBundledOrt` 剔除 vite 自动打包的重复 ort wasm（省 23.5MB 死重量），`/ort/` 为唯一来源。`web/{static/*,public/models,public/ort}` 全 gitignore，仅保留 `static/.gitkeep` 占位满足 `//go:embed all:static` 干净检出编译约束；产物由 `make web` 构建时生成。已起诊断实例（ADDR=:8099，避开用户 8080/8097）验证 `/models/...onnx`、`/ort/...wasm`、worker chunk 均 200 同源可达且尺寸正确。浏览器内 点击→描轮廓→描述回填→改图 实际推理待真人点验
- [x] 4.3 `openspec validate add-region-feature-prompting --strict` 通过
