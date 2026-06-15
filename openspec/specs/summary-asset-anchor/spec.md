# summary-asset-anchor Specification

## Purpose
TBD - created by archiving change anchor-context-continuity. Update Purpose after archive.
## Requirements
### Requirement: 压缩时提取并保留资产编辑锚点
当 `Window.compressLocked` 将旧轮折叠进 summary 时，系统 SHALL 扫描被折叠消息，提取最近一次「source asset → output asset」编辑链，并以结构化行 `[最近编辑: source=<id> → output=<id>]` 附加在 summary 文本末尾。提取来源为窗口内实际存在的数据：

- **source** 取自 edit 类工具调用消息的 `source_asset_id` 参数（生产流的 tool result 仅为 `[edit_image 已执行]`，不含 asset id，故不能从 tool result 取）；
- **output** 由后续轮次的 `[上次产物: 图N]` 注解对照同批次 `[工作区: 图N=id]` 编号映射解析得到。

由于 source 与 output 常分属不同的压缩批次（编辑工具调用 vs 下一轮注入的「上次产物」注解），系统 SHALL 按字段 merge 到 `w.lastAssetOp`，而非整体覆盖，避免丢失先到达的一侧。当某一侧不可恢复时，SHALL 仅渲染存在的一侧（`source=` 或 `output=` 单边）。若被折叠轮次中两侧都无法识别，则不添加该行。

#### Scenario: 压缩包含编辑轮次时附加锚点
- **GIVEN** 被折叠消息含一轮 edit_image 调用（assistant 消息的 tool_call 参数含 `source_asset_id=a1`），以及后续携带 `[工作区: …图2=a2…] [上次产物: 图2]` 的用户消息
- **WHEN** compressLocked 触发压缩
- **THEN** 生成的 summary 消息末尾包含 `[最近编辑: source=a1 → output=a2]`

#### Scenario: source 与 output 分属不同压缩批次仍能合并
- **GIVEN** 第一次压缩只折叠了含 `source_asset_id` 的编辑轮次，第二次压缩才折叠了含 `[上次产物]` 的后续轮次
- **WHEN** 两次 compressLocked 先后触发
- **THEN** 最终 summary 的锚点同时包含 source 与 output，不因后批次覆盖而丢失 source

#### Scenario: 压缩不含编辑轮次时不添加锚点
- **GIVEN** 被折叠的历史消息全部为纯文本对话（无工具调用、无上次产物注解）
- **WHEN** compressLocked 触发压缩
- **THEN** 生成的 summary 消息不含 `[最近编辑]` 行，格式与现有一致

#### Scenario: 多次压缩只保留单个锚点
- **GIVEN** summary 已存在一个 `[最近编辑]` 锚点，本次压缩对旧 summary 再压缩
- **WHEN** compressLocked 再次触发
- **THEN** 系统先 strip 旧锚点再追加最新锚点，summary 中只有一个 `[最近编辑]` 行

