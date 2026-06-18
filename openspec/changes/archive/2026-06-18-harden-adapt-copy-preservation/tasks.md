## 1. 透传 key_elements_fidelity 到 generation 包

- [x] 1.1 `internal/generation/service.go` 的 `QualityVerdict` 增 `KeyElementsFidelity int` 字段
- [x] 1.2 `cmd/server/main.go` 的 `qualityCheckerAdapter.Check` 把 `v.DimScores.KeyElementsFidelity` 映射进去

## 2. 择优改为红线感知（internal/generation/service.go）

- [x] 2.1 `GenerateParams` 增 `FirstAttemptPass bool` 与 `FirstAttemptKeyElem int`
- [x] 2.2 首检不及格分支保存 `verdict.Pass`、`verdict.KeyElementsFidelity`
- [x] 2.3 新增 `bestOfVerdict` + `preferFirst`：红线通过性 → key_elements_fidelity → total → 取重生版
- [x] 2.4 最终交付版 `Pass=false` 时下发 `review_failed{degraded:true, final:true}`，否则 `review_passed`

## 3. 中等比例差保文案约束（internal/generation/prompt.go + service.go）

- [x] 3.1 `Slots` 增 `SourceWidth/SourceHeight int`
- [x] 3.2 `service.run` 在 `BuildPrompt` 前写入 `Slots.SourceWidth/Height = srcW/srcH`
- [x] 3.3 新增 `reproportionHint`：源↔目标 log 比例差 > `ratioTolerance` 且 `extremeRatioHint` 为空时返回约束文案；按「无文案」过滤
- [x] 3.4 `BuildPrompt` 的 `EditAdaptPlatform` 分支在 `extremeRatioHint` 之后注入 `reproportionHint`

## 4. 测试（internal/generation）

- [x] 4.1 `TestPreferFirst` 表驱动：重生 total 高但 keyelem 低→取首检（e64e/13db 回归）、重生过红线→取重生、全平→取重生
- [x] 4.2 `TestQualityGateDegradedSignalWhenBothFail`：两版都不过红线→交付且 task done
- [x] 4.3 `TestReproportionHint`：16:9→3:2 注入约束（含 copy）、16:9→3:2 无文案（无 copy mention）、16:9→1.8 不注入、极端比例不注入（caller else-if 保证）
- [x] 4.4 更新 `TestQualityGateRegenWorseRevertsToFirst` 已兼容新 bestOf 逻辑
- [x] 4.5 `go build ./...` 与 `go test ./internal/generation/... ./internal/vision/...` 全绿

## 5. 收尾

- [x] 5.1 `openspec validate harden-adapt-copy-preservation --strict` 通过
- [x] 5.2 全部 tasks 勾选
