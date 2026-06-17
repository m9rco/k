# Tasks: quality-gate-fault-attribution

## 1. 判官 fault_source 输出

- [x] 1.1 `internal/vision/quality.go`：`qualityPrompt` 新增 `fault_source` 字段说明（`"repaint"|"outpaint"|"both"`），并在 JSON 示例里体现；`rawVerdict` 新增 `FaultSource string`；`QualityVerdict` 新增 `FaultSource string`，在 `evaluate()` 中透传
- [x] 1.2 `internal/generation/service.go`：`QualityVerdict` 新增 `FaultSource string`；`qualityCheckerAdapter.Check()` 透传该字段

## 2. 服务端精确回退

- [x] 2.1 `GenerateParams` 新增 `PreOutpaintData []byte`（非空时 run() 跳过 gen.Generate()，直接进入 outpaint/converge 步骤）
- [x] 2.2 `run()`：outpaint 分支（`mode == crop.ModeOutpaint`）执行前快照 `preOutpaintData = out.Data`
- [x] 2.3 `run()` 质量失败重试分支：若 `verdict.FaultSource == "outpaint"` 且 `preOutpaintData != nil` → 设 `retry.PreOutpaintData = preOutpaintData`，hints 通过 `outpaintConverge` 新增的 `hints string` 参数注入 outpaint prompt；否则整条重跑（现有逻辑）
- [x] 2.4 `run()` 开头：若 `p.PreOutpaintData != nil` → 跳过 `gen.Generate()` 调用，直接 `out.Data = p.PreOutpaintData`，继续执行 outpaint/converge 及后续步骤

## 3. 测试

- [x] 3.1 单测：`fault_source=outpaint` + preOutpaintData 非空 → gen 不被调用，outpaint 重跑
- [x] 3.2 单测：`fault_source=repaint` → 整条重跑（gen 被调用，PreOutpaintData 为空）
- [x] 3.3 单测：outpaint 路径未触发（preOutpaintData 为空）即便 `fault_source=outpaint` → 整条重跑
- [x] 3.4 `go test ./internal/generation/... ./internal/vision/...` 通过

## 4. 验证

- [x] 4.1 `go build ./...` 无报错
- [x] 4.2 `openspec validate quality-gate-fault-attribution --strict` 通过
