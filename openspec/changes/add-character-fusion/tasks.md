## 1. 融合意图请求级锁定 gpt-image-2（带兜底）
- [x] 1.1 在 `ToolDeps` 增加融合用的请求级 override 字段（如 `FusionModelOverride *config.ImageProviderConfig`），doc comment 说明仅 change_character/add_character 使用、nil 时回退 ImageOverride
- [x] 1.2 在 `internal/agent/agent.go` 解析该 override：复用 `adapt_platform` 的解析模式，`ResolveImageModel(SceneImage, "gpt-image-2")` → `"gemini-3-pro-image"` → nil（带兜底，缺凭据不注入破损 override）
- [x] 1.3 在 `internal/agent/tools.go` 的 `edit_image` handler：当 `kind` 为 `EditCharacter`/`EditCharacterAdd` 时 `ProviderOverride` 取融合 override（nil 则回退 ImageOverride），其余意图保持 `ImageOverride`
- [x] 1.4 单测：表驱动覆盖「融合意图选 gpt-image-2 / gpt-image-2 缺失降级 gemini / 两者缺失回退会话选型 / change_background 不受影响」

## 2. 融合生图 prompt 写死真相源契约
- [x] 2.1 在 `internal/generation/prompt.go` 的 `EditCharacter`/`EditCharacterAdd` 分支显式声明：底图=风格/文案/宣发意图/构图/配色真相源；只把参照图角色按底图风格重绘融入；不带入参照图风格/文案/背景；不凭空多生角色（复用并针对融合收紧 PRESERVE/AVOID clause）
- [x] 2.2 单测：断言融合 prompt 含上述约束，用户注入文本经 Sanitize 后不改写系统约束

## 3. 融合专属质检维度与红线
- [x] 3.1 在判官固定 prompt 中为融合意图追加 `base_fidelity`、`fusion_harmony`、`no_extra_subjects`、`identity_fidelity` 四项的评分/判定说明（服务端固定文案：底图风格/文案/意图全保留、不得带入参照图风格、不得凭空多生、角色身份忠于参照图、光照/色温/边缘/透视/比例协调）
- [x] 3.2 扩展质检结果结构体解析这四个字段（向后兼容：非融合意图不携带时按缺省）
- [x] 3.3 在 `internal/generation/service.go` 质检判定分支：仅当 `kind ∈ {EditCharacter, EditCharacterAdd}` 时启用四项判定——`base_fidelity < 最小阈值` 与 `no_extra_subjects` 命中与 `identity_fidelity < 最小阈值` 为硬红线失败，`fusion_harmony < 阈值` 触发重生
- [x] 3.4 四维度分数记录到 `quality.check` 诊断日志（与既有 `ad_appeal` 一致，供日志/统计）；失败 hints 拼接到 REVISE 段，复用 `QUALITY_MAX_RETRY` 与取最优版本逻辑
- [x] 3.5 确认 `adapt_platform`/`change_background`/`change_text` 质检维度集与红线不变（回归用例）
- [x] 3.6 单测：覆盖「底图被参照图覆盖/文案改写命中硬红线」「融合突兀触发重生」「凭空多生命中硬红线」「身份走样命中硬红线」「非融合意图不评估四维度」「质检器未配置降级直出」

## 4. 验证
- [x] 4.1 `gofmt` + `go vet ./...` + 相关包 `go test ./internal/generation/... ./internal/agent/...`
- [x] 4.2 `openspec validate add-character-fusion --strict` 通过
