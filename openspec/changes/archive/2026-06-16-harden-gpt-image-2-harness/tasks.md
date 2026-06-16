# Tasks

## 1. gpt-image-2 尺寸解析器（按适配器隔离）
- [x] 1.1 在 `internal/generation/http_provider.go` 新增 `resolveGptImage2Size(dstW, dstH int) string`：按目标比例（>3:1 夹 3:1）+ ~2K 像素预算反解，两边 round 到 16 倍数、clamp 最长边 ≤3840、夹总像素入 [655360, 8294400]
- [x] 1.2 用新解析器替换 gpt-image 路径的 `sizeParam`/`nearestGenSize` 调用；保留 `dashscope.go` 自有枚举映射不变
- [x] 1.3 表驱动单测覆盖代表性档位：16:9/9:16 同比例、512×512 与 900×600（<下限）放大、4:1/6:1 夹 3:1、2732×2048 夹 2K、零维度→空（`http_provider_test.go`）
- [x] 1.4 **覆盖断言**：遍历 `configs/channels.json` 全部 producible 档位，断言解析器输出对每个档位都满足 gpt-image-2 全部约束（16 倍数/最长边/像素范围/≤3:1），无遗漏
- [x] 1.5 确认 `gemini.go`（`aspectRatio`，不传 size）与 `dashscope.go` 不受改动影响（边界回归测试）

## 2. 参考图角色分层（1~16）
- [x] 2.1 `internal/generation/service.go`：`MaxReferenceImages` 6 → 16
- [x] 2.2 `primaryAndExtras`/透传保持「锚点 = refs[0]、辅助 = refs[1:]」语义，超 16 截断并保留提示
- [x] 2.3 单测：1/2/16/17 张参考图的截断与锚点选取（`service` 测试或新增）

## 3. 低发散 prompt harness（四段式 + 游戏宣发上下文）
- [x] 3.1 `internal/generation/prompt.go`：抽出固定 `[CONTEXT]`（游戏宣发声明）/`[PRESERVE]`/`[AVOID]` 文案常量，`BuildPrompt` 对所有图生图意图统一拼接四段式骨架
- [x] 3.2 `[CONTEXT]` 明确「现有游戏的宣发素材、参考图为真实游戏美术、停留在该游戏风格世界观、不虚构不存在的玩法/UI/场景/角色」；多参考图（≥2）时 `[PRESERVE]` 追加锚点角色声明句
- [x] 3.3 既有 `harmonyConstraint`/palette 并入骨架语义，去重；文生图仅加轻量 `[CONTEXT]`、不加 `[PRESERVE]`
- [x] 3.4 单测：各意图含四段、`[CONTEXT]`/`[PRESERVE]`/`[AVOID]` 不含用户文本、注入文本被 sanitized、文生图不含 `[PRESERVE]`（`prompt` 测试）

## 4. 保真与能力坑建模
- [x] 4.1 确认 gpt-image-2 请求不组装 `input_fidelity`；在适配器注释记录「自动高保真」事实
- [x] 4.2 `prompt.go`：透明底等不可满足约束按适配器能力改写（gpt-image-2 的 2 个透明底档位改写为「中性底便于抠图」；Gemini 等支持透明底者不改写）
- [x] 4.3 iOS `2732×2048` 等 >2K 档位夹 2K 出图路径打 trace 日志标注
- [x] 4.4 单测：透明底约束改写 / 可满足约束原样注入 / 供应商能力分支

## 4b. harness 决策可观测（trace 日志）
- [x] 4b.1 `service.go`：`BuildPrompt` 后发 `gen.harness` 事件，携带 kind/ref_count/multi_image_anchor/provider/supports_transparency/transparency_rewritten，适配任务额外带 target_size→gen_size 映射
- [x] 4b.2 新增 `providerKind` 助手（openai/gemini/dashscope，与 NewProvider 对齐）供日志使用
- [x] 4b.3 单测：捕获 buffer-backed logger，断言 `gen.harness` 字段与适配尺寸映射出现

## 5. 收敛协同与端到端
- [x] 5.1 确认 `adapt.go` 的 `convergeMode` 对「同比例放大→下采样」「夹 3:1→cover」「>2K→上采样」三类路径产物均精确等于目录档位（补断言，逻辑不改）
- [x] 5.2 `adapt_test.go` 增补：放大下采样、极端比例 cover、超 2K 上采样的协同用例
- [ ] 5.3 手测脚本/记录：1 张与多张（含 16 张）参考图，对一组代表性目标档位（icon 512 / cover 900×600 / 16:9 / 9:16 / 4:1 banner / iOS 2732×2048）跑通，核对主体与意图保留、构图和谐、不虚构、尺寸精确 — **待真实 gpt-image-2 凭证联调**（自动化测试已覆盖尺寸合法性、收敛路径、prompt 骨架；成片质量需真实 API 验收）

## 6. 验证
- [x] 6.1 `go build ./...` 与 `go test ./internal/generation/... ./internal/crop/...` 全绿
- [x] 6.2 `gofmt` 无 diff
- [x] 6.3 `openspec validate harden-gpt-image-2-harness --strict` 通过
