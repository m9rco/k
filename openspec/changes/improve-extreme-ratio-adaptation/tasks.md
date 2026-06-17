## 1. 收敛路由（极端比例走 cover）

- [ ] 1.1 `internal/generation/adapt.go`：新增常量 `extremeConvergeRatio = 3.0`（与 `gptImage2MaxRatio` 同源对齐），加 doc comment 说明其与生成端 3:1 夹断的关系
- [ ] 1.2 `convergeMode` auto 分档新增极端比例档：计算 `dstRatio = max(dstW/dstH, dstH/dstW)`，`dstRatio >= extremeConvergeRatio` 时返回 `crop.ModeCover`，置于 outpaint 判定之前；目录 pin 优先级不变
- [ ] 1.3 `internal/generation/adapt_test.go`：表驱动覆盖 6:1/5:1/4:1 → `ModeCover`，临界 ~3:1、中等差 → `ModeOutpaint`，同比例 → `ModeScale`，目录 pin 仍优先

## 2. 极端比例安全区构图（生成提示）

- [ ] 2.1 `internal/generation/prompt.go`：新增 `safeBandFraction(genW, genH, dstW, dstH) float64`，返回 cover 后保留的中央带占比（`keepFrac = genRatio / dstRatio`，取整到 5% 网格，clamp 合理范围）
- [ ] 2.2 升级 `extremeRatioHint`：横幅→「主体/LOGO/核心文案置于中央约 N% 高度带内，上下仅放可裁背景延伸」；竖条对称（中央宽度带）；N 由 2.1 计算
- [ ] 2.3 确认 `extremeRatioHint` 在 `assembled low-divergence prompt`（gen.harness）路径正确注入，且与 THEME/PRESERVE 段不冲突
- [ ] 2.4 `internal/generation/prompt_test.go`：断言 6:1 目标提示含「中央」「约 50%」措辞、含「上下…背景」；4:1 含「约 75%」；普通比例不含安全区措辞

## 3. 收敛执行确认

- [ ] 3.1 `internal/generation/service.go`：确认 `EditAdaptPlatform` 收敛分支在 `mode == ModeCover` 时走 `crop.CropBytesWithOptions(..., ModeCover)`（非 outpaint 分支），日志 `gen.converge` 记录 `mode=cover`
- [ ] 3.2 确认极端比例不再调用 `outpaintConverge`（去掉极端档的第二段 AI 调用），中等差仍可走 outpaint

## 4. 校验与文档

- [ ] 4.1 `go build ./...`、`go test ./internal/generation/...` 通过
- [ ] 4.2 用 `data/assets` 中的两张参考图本机复跑 1008×168/202/252 适配（诊断实例用非 8080 端口），目检主体完整、构图自然、批次稳定
- [ ] 4.3 更新 `openspec/project.md` 若涉及（本 change 无模型清单变更，预计无需）
- [ ] 4.4 `openspec validate improve-extreme-ratio-adaptation --strict` 通过
