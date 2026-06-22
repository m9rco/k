# Tasks — fix-adapt-aspect-and-backfill

## 1. 修复预放大形变（Issue 1）
- [x] 1.1 将 `internal/generation/service.go` 的预放大由 `crop.ModeScale` 改为等比贴合：新增 `crop.ContainPadBytes`（等比 contain + 透明边距 + PNG 输出），保持源图比例、绝不非等比拉伸
- [x] 1.2 复用既有 `crop.ContainCrop`：源图比例 < 画布按高贴合、> 按宽贴合，主体不形变，四周透明边距交由模型补全
- [x] 1.3 单测 `TestContainPadBytesPreservesAspectWithTransparentMargins`：16:9 源 → 2.5:1 画布，断言内容比例保持 ≈1.78（非拉伸到 2.5）、侧边透明、中心不透明

## 2. 锚点比例直接回填（Issue 2）
- [x] 2.1 `internal/generation/adapt.go`：删除 `!multiRef` 门控，`aspectClose(anchor, target)` 判定不再区分参考图数量——凡锚点比例匹配的尺寸走确定性快路径
- [x] 2.2 快路径复用 `CropToSizes(ModeCover)`：完全一致 → 单位缩放即逐像素回填；同比例不同尺寸 → 无裁切等比缩放（两者落库为 `cropped` 资产、`Via=crop`）
- [x] 2.3 仅相对锚点需重构图（比例差/横竖翻转）的尺寸保留 AI 重绘；整组参考仍经 `refs` 喂入此类任务（未改动该路径）
- [x] 2.4 单测 `TestAdaptMultiRefRatioMatchTakesCropPath`（完全一致 + 同比例两子用例，断言 `Via=crop`、无 task）、`TestAdaptMultiRefReshapeTakesAIPath`（断言 `Via=ai`）
- [x] 2.5 决策表覆盖：单参考 `TestAdaptRatioMatchTakesCropPath` / `TestAdaptOrientationFlipTakesAIPath` + 多参考两测，覆盖单/多 × 完全一致/同比例/重构图

## 3. 右调生成预算降延时（Issue 3）
- [x] 3.1 `resolveGptImage2Size` 引入有效预算 `budget = min(gptImage2GenBudget, targetPixels)`，仅当目标像素 ≥ 合法下限（`gptImage2MinPixels`，即"小档位 floor"）时生效；大档位不再无谓上抬到 ~3MP，小档位保留放大锐化
- [x] 3.2 比例夹断（clamped）尺寸跳过预算上限：其生成比例与目标不同，目标像素非有效预算，且更小预算会让 16 倍数取整越过 3:1——极端档由 cover 收敛、本非延时痛点。保留最长边 ≤3840、16 倍数、像素 [655360,8294400] 与 `gen.adapt_above_2k` 日志
- [x] 3.3 单测 `TestResolveGptImage2SizeBudgetCap`：2080×828 / 1920×1080 生成像素 ≈ 目标自身（≤110%）；900×600 / 512×512 仍放大到预算之上。既有 `TestResolveGptImage2SizeCoversCatalog`（139 档全合法）仍通过

## 4. 边距连贯补全（Issue 4）
- [x] 4.1 `internal/generation/prompt.go` 适配 MODIFY 体新增一句：把等比预放大引入的透明边距明确表述为"需向外扩展补全的连贯场景"，并禁止留白/letterbox 带与拉伸主体
- [x] 4.2 既有像素级留白带预过滤兜底：`internal/vision/pixel.go` 已检测"纯色留白条带"并触发 `gen.pixel_failed` 重生（无需新管线，仅验证）
- [x] 4.3 单测 `TestBuildPromptAdaptMarginExtension`：16:9→2.5:1 适配提示含 `empty/transparent margins`、`do NOT stretch or distort`、`letterbox` 约束

## 5. 验收与回归
- [x] 5.1 `go test ./internal/generation/... ./internal/crop/...` 全绿
- [x] 5.2 `go vet ./...`、`gofmt -l` 通过（全 22 包测试通过）
- [~] 5.3 复现日志场景：确定性部分已由集成测试 `TestAdaptMultiRefBackfillProducesExactUndistortedAsset` 覆盖——双参考组适配到 `taptap.banner.welfare-1920x1080`（完全一致回填）与 `1280x720`（同比例缩放），经**真实** crop 服务产出 `cropped` 资产、尺寸精确、无 AI task、无形变。模型相关部分（真实重绘视觉保真、`app.log` 端到端核对）仍需图模型密钥，留待人工/集成环境
- [x] 5.4 `openspec validate fix-adapt-aspect-and-backfill --strict` 通过
