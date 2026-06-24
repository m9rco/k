# quality-gate-enhancement Specification

## Purpose
TBD - created by archiving change enhance-output-quality. Update Purpose after archive.
## Requirements
### Requirement: 质检模型可配置升级
系统 SHALL 支持通过 `QUALITY_MODEL` 环境变量选择视觉质检模型（SubjectDetector 同理）。当模型名包含 `gemini` 时，系统 SHALL 自动经 Gemini 原生 `generateContent` 路径传图（`inlineData`，`responseMimeType=application/json`），其余走现有 OpenAI-compat 路径。两条路径均**不依赖 COS 公网 URL**。默认值保持向后兼容（`doubao-seed-1-6-vision-250815`）。

#### Scenario: 配置 Gemini 质检模型
- **WHEN** `QUALITY_MODEL=gemini-2.5-flash-all`
- **THEN** 系统经 Gemini `generateContent` + `responseMimeType=application/json` 发起质检
- **AND** 产物图片以 `inlineData` base64 传入，不依赖 COS

#### Scenario: 未配置时使用默认模型
- **WHEN** `QUALITY_MODEL` 未设置
- **THEN** 系统使用 `doubao-seed-1-6-vision-250815` 走 OpenAI-compat 路径
- **AND** 行为与当前一致

### Requirement: 宣发吸引力维度（ad_appeal）
质检 SHALL 新增 `ad_appeal`（宣发吸引力 0-100）维度，评估素材在信息流中的视觉冲击力：主体是否醒目、色彩层次、构图是否符合广告投放惯例、整体是否达到「投放级」。该维度 **SHALL NOT** 纳入总分聚合（不改变现有 pass/fail 阈值基线）；当 total ≥ threshold 但 `ad_appeal` < 50 时，系统 SHALL 在产物 hint 追加吸引力改善建议（不触发重生成）。`ad_appeal` 分数 SHALL 记录到资产元数据供日志/统计分析。

#### Scenario: ad_appeal 不影响 pass/fail
- **WHEN** 质检 total ≥ threshold 但 ad_appeal = 35
- **THEN** 系统判定为通过（pass=true），产物正常持久化
- **AND** 产物 hints 追加一条吸引力建议文字
- **AND** ad_appeal 分数记录到资产元数据

#### Scenario: ad_appeal 与 total 均优秀
- **WHEN** 质检 total = 88，ad_appeal = 82
- **THEN** 产物正常通过，hints 不追加额外建议

### Requirement: 质检重试上限 2 次
系统 SHALL 将平台适配与普通生图的质检重生成上限从 1 次提升到 2 次（总共最多 3 次生成：首次 + 2 次 retry）。第 2 次 retry 的 hints SHALL 为前两次质检 hints 的拼接，以保留历次改进线索。最终产物 SHALL 取三次生成中 total 分最高的版本。当 `QUALITY_MAX_RETRY` 环境变量设置为正整数时，系统 SHALL 使用该值作为上限（默认 2）。

#### Scenario: 第 1 次重试仍失败触发第 2 次
- **WHEN** 首次生成 total=65（< threshold=75），第 1 次 retry total=72（仍 < threshold）
- **THEN** 系统发起第 2 次 retry，hints 为 hints_1 + " " + hints_2
- **AND** 第 2 次 retry 完成后取三次产物中 total 最高者持久化

#### Scenario: 第 1 次重试通过不触发第 2 次
- **WHEN** 首次 total=65 失败，第 1 次 retry total=80（≥ threshold）
- **THEN** 系统直接持久化第 1 次 retry 产物，不发起第 2 次 retry

#### Scenario: QUALITY_MAX_RETRY=1 恢复旧行为
- **WHEN** `QUALITY_MAX_RETRY=1`
- **THEN** 系统最多重生成 1 次，与本 change 前行为一致

### Requirement: 普通生图质检（换角色/背景/文案/加角色）
系统 SHALL 为 `change_character`、`change_background`、`change_text`、`add_character` 四种意图的产物接入现有质量门控。质检以空字符串作为 `themeReport`（无视觉分析前置阶段），`specLabel` 为意图名称（如 `"change_character"`）。质检失败后重生成逻辑与 `adapt_platform` 路径一致（最多 `QUALITY_MAX_RETRY` 次，hints 注入 REVISE 段）。`generate_icon` 与 `text_to_image` 意图 **SHALL NOT** 接入此质检。

#### Scenario: 换背景产物主体偏移被检出并重生成
- **WHEN** `change_background` 产物 subject_consistency=50（< threshold）
- **THEN** 系统以质检 hints 注入 REVISE 段发起重生成
- **AND** 最终产物为 subject_consistency 分更高的版本

#### Scenario: generate_icon 不走质检
- **WHEN** `generate_icon` 意图产出产物
- **THEN** 系统不调用质量门控，产物直接持久化

#### Scenario: 质检器未配置时降级
- **WHEN** `QualityChecker` 未配置（QUALITY_BASE_URL 未设置）
- **THEN** 所有意图的产物均不经质检直接持久化，行为与当前 adapt 路径降级一致

### Requirement: 角色融合专属质检维度与红线
系统 SHALL 为 `change_character` 与 `add_character` 两个融合意图的产物，在现有质量门控之上叠加**融合专属**判定，使「把参照图角色放进底图」既完整保留底图、又自然不突兀、且不凭空多生角色。该判定 SHALL 复用现有质检管线（同一判官模型与调用路径、`QUALITY_MAX_RETRY` 重试上限、hints 注入 REVISE 段重生成、取多次产物中最优版本、质检器未配置时降级直出）。判官 prompt MUST 为服务端固定文案。

融合的真相源契约：**底图（`source_asset_id`，即图1）是产物风格、宣发意图、文案/标题、构图与配色的唯一真相源**。融合 MUST 完整保留底图的这些要素，**只**把参照图（图2、图3…）中的**角色/主体**按底图的风格重绘式融入（本地化到底图的光照/色温/笔触/比例），**MUST NOT** 把参照图的风格、文案、背景或配色带入并覆盖底图。

四个判定为：
- **`base_fidelity`（底图保真 0-100，硬红线）**：底图的整体风格、宣发意图、文案/标题（字符正确不糊化/改写）、构图与配色 SHALL 在产物中完整保留；参照图的风格/文案/背景 **MUST NOT** 覆盖底图。低于其最小阈值 SHALL 直接判失败并重绘，不受加权总分影响。
- **`fusion_harmony`（自然融入度 0-100）**：新角色与底图在光照方向、色温/色调、边缘过渡、透视与比例上的协调度（角色应被**重绘以匹配底图风格**，而非贴图）。当该维度 < 阈值时，系统 SHALL 视为失败并按现有重生成流程注入改进 hints 重绘（封顶 `QUALITY_MAX_RETRY`）。
- **`no_extra_subjects`（硬红线）**：产物 **MUST NOT** 出现参考图/底图之外凭空多生的角色或主体。命中（即检出多余主体）SHALL 直接判失败并重绘，不受加权总分影响。
- **`identity_fidelity`（身份保真 0-100，硬红线）**：被融合角色的身份特征（外观、服饰、标志性特征）SHALL 忠于其参照图；底图原有应保留的主体 SHALL NOT 被替换或丢失。低于其最小阈值（硬红线）SHALL 直接判失败并重绘。

上述融合专属判定 **SHALL NOT** 改变 `adapt_platform` 及 `change_background`/`change_text` 的质检行为；非融合意图的维度集与红线保持现状。四个维度分数 SHALL 记录到产物元数据供日志/统计。

#### Scenario: 底图风格/文案被参照图覆盖命中硬红线
- **WHEN** `add_character` 产物把参照图（图2）的画面风格或背景带进来覆盖了底图（图1），或底图原有文案/标题被改写、糊化、丢失（`base_fidelity` < 最小阈值）
- **THEN** 系统判定失败（不受加权总分影响）并重绘
- **AND** hints 明确要求完整保留底图的风格/宣发意图/文案/构图/配色，只把参照图角色按底图风格重绘融入

#### Scenario: 融合突兀被检出并重绘
- **WHEN** `add_character` 产物 `fusion_harmony` = 40（< 阈值），无其他红线命中
- **THEN** 系统以质检 hints 注入 REVISE 段发起重生成（封顶 `QUALITY_MAX_RETRY`）
- **AND** 最终产物取多次生成中综合最优的版本
- **AND** `fusion_harmony` 分数记录到产物元数据

#### Scenario: 凭空多生角色命中硬红线
- **WHEN** `add_character` 产物出现参考图与底图之外的第三个角色，`no_extra_subjects` 命中
- **THEN** 系统判定失败（不受加权总分影响）并重绘，hints 明确要求只保留底图主体与指定融合角色

#### Scenario: 被融合角色身份走样命中硬红线
- **WHEN** `change_character` 产物中替换后的角色身份特征明显偏离参考图（`identity_fidelity` < 最小阈值），或底图原应保留主体被丢失
- **THEN** 系统判定失败并重绘

#### Scenario: 非融合意图不受影响
- **WHEN** `change_background` 或 `adapt_platform` 产物进入质检
- **THEN** 系统不评估 base_fidelity / fusion_harmony / no_extra_subjects / identity_fidelity，维度集与红线与本 change 前一致

#### Scenario: 质检器未配置时降级
- **WHEN** `QualityChecker` 未配置
- **THEN** 融合产物不经质检直接持久化，行为与现状一致

