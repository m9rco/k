## ADDED Requirements

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
