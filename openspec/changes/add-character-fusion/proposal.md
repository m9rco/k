# Change: 角色融合默认锁定 gpt-image-2 + 融合专属质量门控

## Why
用户的高频诉求「把图2角色融合到图1」当前走 `edit_image` 的 `change_character`/`add_character` 意图（图1=被编辑底图 `source_asset_id`，图2=参照 `reference_asset_ids`）。当前两个缺口导致融合效果偏差：

1. **模型路由**：融合走 `ImageOverride`（会话选型/服务默认），不像 `adapt_platform` 那样请求级强制 `gpt-image-2`。而 gpt-image-2 对「主体身份与构图保真」最强，是融合的最佳模型，却没有被默认用上。
2. **审核针对性**：现有普通生图质检维度（compliance/subject_consistency/character_appeal/overall_quality）是为通用换/加角色与平台适配设计的，对融合两个核心失败模式没有专门红线——①新角色与底图**不和谐、贴图感/突兀**；②模型**凭空多生**了参考图/底图之外的角色或主体。

## What Changes
- **融合意图请求级锁定 gpt-image-2（带兜底）**：`change_character` 与 `add_character` 两个意图的 AI 生图，请求级优先使用 `gpt-image-2`；其凭据未配置时降级 `gemini-3-pro-image`，再降级到会话选型/服务默认（与 `adapt_platform` 现有降级链一致）。`change_background`/`change_text`/`generate_icon`/`text_to_image` 路由**不变**。
- **新增融合专属质检维度与红线**（仅作用于 `change_character`/`add_character`，在现有质量门控之上扩展，不改 adapt 路径行为）。融合的真相源契约：**底图（图1）是风格、宣发意图、文案、构图、配色的唯一真相源**——融合只把参照图（图2、图3…）的**角色**按底图风格重绘式融入，不得带入参照图的风格/文案/背景：
  - `base_fidelity`（底图保真，硬红线）：底图的风格/宣发意图/文案/构图/配色完整保留，未被参照图覆盖、文案未被改写或糊化。
  - `fusion_harmony`（自然融入度 0-100）：新角色与底图在光照方向、色温、边缘、透视、比例上的协调度（角色应被重绘以匹配底图风格，非贴图）；低于阈值触发现有重生成流程。
  - `no_extra_subjects`（硬红线）：产物**不得**出现参考图/底图之外凭空多生的角色或主体；命中即判失败并重绘。
  - `identity_fidelity`（身份保真 0-100，硬红线）：被融合角色的身份特征（外观/服饰/标志性特征）忠于参照图，且底图原有主体未被替换/丢失。
- **生图阶段同步写死真相源契约**：在 `change_character`/`add_character` 的生图 prompt 中显式声明「底图为风格/文案/宣发意图真相源，只把参照图角色按底图风格本地化融入」（复用现有 PRESERVE/AVOID clause 的锚点语义并针对融合收紧）。
- 复用现有质检重试上限（`QUALITY_MAX_RETRY`，默认 2）、hints 注入 REVISE 段、取最高分版本、质检器未配置时降级直出等机制。

## Impact
- 受影响 specs：`image-generation`（融合意图模型路由）、`quality-gate-enhancement`（融合专属质检维度与红线）
- 受影响代码：
  - `internal/agent/agent.go`（注入融合用的请求级 model override）
  - `internal/agent/tools.go`（`edit_image` 在 `change_character`/`add_character` 时使用融合 override 而非 `ImageOverride`）
  - `internal/generation/service.go`（融合意图的质检维度/红线分支、specLabel）
  - `internal/generation/prompt.go`（融合生图 prompt 写死底图真相源契约；新增四维度的固定判官文案）
- 非破坏性：未配置 gpt-image-2/质检器时行为优雅降级到现状。
