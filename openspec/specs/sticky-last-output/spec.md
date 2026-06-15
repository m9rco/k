# sticky-last-output Specification

## Purpose
TBD - created by archiving change anchor-context-continuity. Update Purpose after archive.
## Requirements
### Requirement: 服务端跟踪上次产出资产
系统 SHALL 在每个 session 中维护一个「上次成功产出的 asset_id」（lastProducedAssetID）。当异步生图/生视频任务完成并产出新资产时，系统 SHALL 将该 session 的 lastProducedAssetID 更新为新资产的 id。该状态为进程内存，服务重启后丢失（可接受：后续轮次有完整历史可供模型推断）。

#### Scenario: 生图任务完成时更新 lastProduced
- **GIVEN** 一个 session 正在处理一次 edit_image 或 generate_image_from_text 请求
- **WHEN** 后端异步任务完成并产出 asset_id
- **THEN** 系统将该 session 的 lastProducedAssetID 更新为该 asset_id

#### Scenario: 视频任务完成同样更新
- **GIVEN** 一个 session 正在处理 image_to_video 请求
- **WHEN** 视频任务完成并产出 asset_id
- **THEN** 系统将该 session 的 lastProducedAssetID 更新为该 asset_id

#### Scenario: 服务重启后 lastProduced 为空
- **GIVEN** 服务端重启，session 历史从 DB 恢复
- **WHEN** 该 session 尚未在本次进程中产出任何新资产
- **THEN** lastProducedAssetID 为空，系统按现有逻辑处理（不注入上次产物前缀）

### Requirement: 无显式选中时注入上次产物前缀
系统 SHALL 在构建「工作区编号」前缀时，当用户本轮未提供任何 ref 或 refs 且 lastProducedAssetID 非空时，在 `[工作区: …]` 前缀末尾附加 `[上次产物: 图N]` 注解（N 为该 asset 在当前显示顺序中的序号）。

#### Scenario: follow-up 轮次注入上次产物
- **GIVEN** session 的 lastProducedAssetID 为 asset_B，工作区有图1=asset_A、图2=asset_B
- **WHEN** 用户发送 "再换个角色" 且未显式选中任何图
- **THEN** 系统注入: `[工作区: 图1=asset_A(generated), 图2=asset_B(generated)] [上次产物: 图2] 再换个角色`

#### Scenario: 有显式选中时不注入上次产物
- **GIVEN** session 的 lastProducedAssetID 为 asset_B，但用户本轮显式选中了 asset_A
- **WHEN** 用户发送带 ref=asset_A 的消息
- **THEN** 系统注入 `[选中: 图1]` 而非 `[上次产物: 图2]`，显式选中优先

#### Scenario: lastProduced 不在当前工作区时不注入
- **GIVEN** lastProducedAssetID 为某个已从工作区移除的 asset id
- **WHEN** 用户发送消息
- **THEN** 系统不注入 `[上次产物]` 前缀（asset 不在当前编号列表中）

### Requirement: System Prompt 声明上次产物规则
系统 SHALL 在 Agent system prompt 的「工具使用规范」中新增一条规则：当消息携带 `[上次产物: 图N]` 时，若用户本轮未明确指定操作对象，**直接**将图N 作为操作底图（source_asset_id）或主要参考（reference_asset_ids 首位），**不得**触发 clarify_intent 反问"要操作哪张图"。只有在 context 中既无显式选中、又无 `[上次产物]` 标注、且工作区存在多张图时，才允许发起「操作哪张图」类询问。

#### Scenario: LLM 遵循上次产物规则，不触发澄清
- **GIVEN** 上下文前缀包含 `[上次产物: 图2]`，用户说"继续改一下"
- **THEN** Agent 直接以图2对应的 asset_id 作为 edit_image 的 source_asset_id
- **AND** 不触发 clarify_intent，不发出"请问要操作哪张图"

#### Scenario: 仅在真正不确定时才澄清
- **GIVEN** session 无 lastProducedAssetID（进程重启后首次操作），工作区有图1、图2两张图，用户未选中任何图
- **WHEN** 用户发送"换个背景"
- **THEN** 系统允许 Agent 通过 clarify_intent 询问"操作哪张图"

### Requirement: 前端选中状态随产物流转
为使「默认在上次产物上叠加」在端到端生效，前端 SHALL：(1) 在发送一条用户消息后清空当前选中（选中 id 已随该消息下发，不应跨轮粘连）；(2) 当一个单产物图像/视频任务（kind 为 generate/video）完成时，自动将选中切换为该新产物，使下一轮默认作用其上。多产物或批量任务（搜索/爬取）SHALL 不自动改写选中。

#### Scenario: 发送后清空选中
- **GIVEN** 用户选中图1 并发送"换背景"，该轮 ref=图1 已随消息下发
- **WHEN** 消息发出后
- **THEN** 前端清空选中集合，图1 不再作为后续轮次的显式 ref 持续下发

#### Scenario: 新单产物自动成为选中
- **GIVEN** 上一步换背景任务（kind=generate）完成并产出图2
- **WHEN** 前端收到该任务的 task_done（携带新 asset_id）
- **THEN** 前端将选中切换为图2
- **AND** 下一轮"再换个角色"默认作用于图2

#### Scenario: 批量任务不劫持选中
- **GIVEN** 一次图片搜索（kind=search）完成并下载多张图
- **WHEN** 前端收到其 task_done
- **THEN** 前端不自动改写选中集合

### Requirement: MissingKeyParam 在有 lastProduced 时不触发
`ClassifyIntent` 在判断 `MissingKeyParam`（缺少可操作图片）时，SHALL 将 `lastProducedAssetID` 存在且在工作区中视为"有图可操作"，不设置 `MissingKeyParam=true`。这样 remediationClarify 路径也不会误触发"操作哪张图"询问。

#### Scenario: lastProduced 在工作区时 MissingKeyParam 为 false
- **GIVEN** 用户消息未携带 ref/refs，但 context 前缀含 `[上次产物: 图2]`（即工作区前缀存在）
- **WHEN** ClassifyIntent 运行
- **THEN** 结果的 MissingKeyParam 为 false
- **AND** remediationAction 不选择 remediateClarify

