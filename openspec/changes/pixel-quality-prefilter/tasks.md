# Tasks: pixel-quality-prefilter

## 1. 像素级质量检查器

- [ ] 1.1 新建 `internal/vision/pixel.go`：`PixelChecker`，实现灰度 Laplacian 方差计算（3×3 kernel）和四边均匀色带扫描；`NewPixelChecker(blurThreshold int, borderMaxRatio float64) *PixelChecker`，两个参数均为 0 时返回 nil（透明禁用）；`Check(imgBytes []byte, mime string) (PixelVerdict, error)` 返回 `{Pass bool, Reasons []string, Hints string}`
- [ ] 1.2 `internal/config/config.go` 增加 `PixelBlurThreshold int`（`PIXEL_BLUR_THRESHOLD`，默认 80）与 `PixelBorderMaxRatio float64`（`PIXEL_BORDER_MAX_RATIO`，默认 0.15）
- [ ] 1.3 `cmd/server/main.go` 用上述配置初始化 `PixelChecker` 并注入 generation service（新增 `SetPixelChecker`）
- [ ] 1.4 `service.go run()`：`EditAdaptPlatform` 收敛后，先调 `PixelChecker.Check()`；不及格 → 推 `review_failed` + 重生（跳过 AI judge）；通过 → 继续走现有 AI judge 流程

## 2. 测试

- [ ] 2.1 单测 `pixel_test.go`：合成一张明确模糊图（Gaussian 预模糊）验证 `variance < threshold` → 不及格；合成清晰图验证通过；合成有纯色横带的图验证留白检测触发；边界：nil checker → 直接通过
- [ ] 2.2 `go test ./internal/vision/...` 通过

## 3. 验证

- [ ] 3.1 `go build ./...` 无报错
- [ ] 3.2 `openspec validate pixel-quality-prefilter --strict` 通过
