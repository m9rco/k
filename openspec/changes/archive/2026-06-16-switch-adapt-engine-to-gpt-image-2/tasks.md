## 1. 默认引擎切换（gpt-image-2，gemini 降级回退）
- [x] 1.1 `internal/agent/agent.go:460` 把请求级适配 override 目标模型由 `gemini-3-pro-image` 改为 `gpt-image-2`；保留「不可用则 override=nil → 回退会话/默认」的现有语义
- [x] 1.2 补 gemini 显式兜底：gpt-image-2 不可用时再尝试 `ResolveImageModel(SceneImage, "gemini-3-pro-image")`，仍不可用才落到会话 image override / 服务默认
- [x] 1.3 更新 `tools.go:577` 起 `adapt_to_platform` 工具描述与 `adaptProvider` 注释，去除「固定 gemini」表述
- [x] 1.4 单测：override 解析在 gpt-image-2 可用/不可用、gemini 可用/不可用四种组合下的落点正确（`internal/agent` 或 `internal/config`）

## 2. 比例预设映射（取代方向三分类，不用 auto）
- [x] 2.1 `internal/generation/http_provider.go` 把 `sizeParam` 升级为按目标宽高比对数距离就近匹配 gpt-image-2 三个合法枚举的映射（提取为可测函数，如 `nearestGenSize(w,h)`）
- [x] 2.2 表驱动单测覆盖 1:1 / 3:2 / 2:3 / 16:9 / 9:16 / 4:1 等比例的就近命中
- [x] 2.3 确认横竖与方形边界、零值（返回空=provider 决定）行为不回退既有调用

## 3. 分档智能收敛（contain vs cover + 预设覆盖）
- [x] 3.1 `internal/config/config.go` 的 `Size` 增加可选字段 `convergeMode`（`contain`/`cover`，空=自动）
- [x] 3.2 `internal/crop` 暴露 `SizeSpec` 携带 `ConvergeMode`；`adapt.go` 透传到 `GenerateParams`（新增字段，仅 `EditAdaptPlatform` 用）
- [x] 3.3 `internal/generation/service.go:389` 适配收敛分支：按 `genAR` vs `dstAR` 的对数差与 `convergeTolerance` 分档选 `ModeContain`/`ModeCover`；`convergeMode` 预设非空时直接采用预设
- [x] 3.4 定义 `convergeTolerance` 常量（建议 ~0.18）并加注释说明取值依据
- [x] 3.5 单测：比例接近→contain、极端比例→cover、预设 `convergeMode` 覆盖自动判定三类场景

## 4. 验证与文档
- [x] 4.1 `go build ./...` 与 `go test ./internal/generation/... ./internal/config/... ./internal/agent/...` 通过
- [ ] 4.2 用一张横版源图实际适配到「方形 / 竖版海报 / 极端长横幅」三类尺寸，确认成片无大面积留白且主体保留
- [x] 4.3 `openspec validate switch-adapt-engine-to-gpt-image-2 --strict` 通过
