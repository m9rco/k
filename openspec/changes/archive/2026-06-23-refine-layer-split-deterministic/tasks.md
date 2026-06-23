# Tasks

## 1. 后端：检测两类化 + 上限收紧（vision）
- [x] 1.1 `subjectsPrompt` 改为只列两类——① 每个角色/人物 ② 每个宣发文案块；显式把 LOGO、核心物件/道具、场景/天空/地面/氛围/装饰底纹都排除到背景
- [x] 1.2 `maxDetectedSubjects` 8 → 5
- [x] 1.3 `subject_test.go`：断言 prompt 含两类与「道具/LOGO 留背景」措辞、上限解析到 5；`parseSubjects` 既有用例保持绿

## 2. 后端：确定性裁切 helper
- [x] 2.1 在 `internal/layering`（或 `internal/crop` 暴露 `ExtractRect`，不缩放）新增按归一化框裁出**原尺寸子图**的纯函数：`{x,y,w,h}` × 源图宽高 → `image.Rect` → 子图 NRGBA → PNG；含确定性 padding（各边 +N% 短边，clamp 进画布）
- [x] 2.2 返回裁切后的**实际归一化框**（含 padding）供前端原位摆放
- [x] 2.3 单测：裁切像素与源图对应区域一致、尺寸=框像素、padding 不越界、退化框（w/h≈0）安全跳过

## 3. 后端：layer-split 改为确定性裁切编排
- [x] 3.1 `layering.Service` 移除 `generator` 接口依赖与 `await`/`poll`/`awaitTimeout` 字段；改为读原图字节 → `DetectSubjects` → 逐主体 `cropRect` 落库 → 背景层引用源图 id
- [x] 3.2 裁切子图复用既有 asset 落库（`composite` kind，复用无损 PNG 优化）；返回有序图层（背景最底 + 主体按重要性），每层带归一化位置框
- [x] 3.3 无主体仍报「未检测到可分层主体」；检测失败报明确错误；不再有「背景失败回退」分支（背景恒为原图）
- [x] 3.4 `service_test.go` 改写：断言「背景层=源图 id + N 个裁切层带正确位置框 + canvas=源图尺寸」、无主体报错、未配置报错；删除 fill_background 回退用例

## 4. 后端：端点与装配
- [x] 4.1 `layer-split` handler 保持 `POST /api/session/{id}/layer-split`，返回结构新增每层 `box`
- [x] 4.2 `main.go`：`layering.NewService` 去掉 generation 依赖（仅需 detector + store）；确认编译

## 5. 前端：按原位摆放裁切层
- [x] 5.1 `api.ts` `SplitLayer` 增加 `box {x,y,w,h}`（归一化）类型
- [x] 5.2 `compositing-canvas.tsx`：主体层 `x = box.x*canvasW, y = box.y*canvasH`（背景层全画幅 x:0/y:0）；导出按现有 `naturalWidth*scale` + x/y 落位（已兼容子图）
- [x] 5.3 文案/交互无需改动其余部分；确认拖拽/缩放/层叠/移除对子图层正常

## 6. 保留停用项确认
- [x] 6.1 `extract_layer`/`fill_background` 意图、prompt、`chromaKeyToAlpha` 保留不删；确认 generation 既有单测仍绿（不被本次改动影响）

## 7. 验证
- [x] 7.1 `go build ./...` + `go test ./internal/...` 全绿 + `go vet`
- [x] 7.2 前端 `tsc -b` + `vite build` 通过
- [x] 7.3 `openspec validate refine-layer-split-deterministic --strict` 通过
- [~] 7.4 真机（playwright）：右键图层精修 → 秒级返回（无生图等待）→ 背景=原图、N 个主体层在原位与原图重合 → 拖动主体露出原图背景 → 导出合成图尺寸=源图。**部分阻塞**：经 yunwu.ai 网关时分割 mask 被阉割（见 memory `layer-split-yunwu-no-masks`），主体层降级为**不透明矩形裁切**而非透明底，故「透明底/无残留矩形」一项无法在当前网关下真机验证；代码层掩码抠图 + 降级路径已实现并通过单测，透明底需直连 Google key 或 rembg sidecar 方可真机验证。

## 8. 后端：矩形裁切 → 分割掩码抠图（修订）
- [x] 8.1 `vision/subject.go`：`Subject` 增 `Mask []byte`；新增 `subjectMasksPrompt`（Gemini 分割：`box_2d` 0..1000 + `mask` data URI）；`DetectSubjects` 按 `isGemini` 分流（Gemini 走掩码、OpenAI 走 box-only）；新增 `parseSubjectMasks` + `box2DToBox` + `decodeMaskDataURI`
- [x] 8.2 `layering/crop.go`：`cropSubject` 增 `mask` 参数——掩码缩放（`x/image/draw`）为 alpha 贴到原始 RGB（无 padding，框即边界）；掩码缺失/不可解码回退原 padding 不透明矩形
- [x] 8.3 `layering/service.go`：抠图循环传 `sub.Mask`；更新「不重绘/透明底」注释
- [x] 8.4 测试：`crop_test` 增「掩码 alpha 生效 + RGB 逐像素来自原图」「不可解码掩码回退不透明」；`subject_test` 增 `parseSubjectMasks`（box_2d 转换 / label 回退 / 空 desc 与退化框丢弃 / 上限 5 / 掩码解码）
- [x] 8.5 文案：`compositing-canvas.tsx` 顶部注释与 loading 文案、`main.go` 装配注释由「补全背景」改为「掩码抠图/背景=原图」
