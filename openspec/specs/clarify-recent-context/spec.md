# clarify-recent-context Specification

## Purpose
TBD - created by archiving change anchor-context-continuity. Update Purpose after archive.
## Requirements
### Requirement: 有上次产物时禁止触发「操作哪张图」澄清
系统 SHALL 在以下条件**全部**成立时才允许通过 remediationClarify 或 LLM 的 clarify_intent 发起「操作哪张图」类询问：
1. 用户本轮无显式选中（ref/refs 为空）
2. session 的 lastProducedAssetID 为空或不在当前工作区
3. 工作区存在多张图

只要条件2不成立（即有 lastProducedAssetID 且在工作区），系统 SHALL 直接使用上次产物，不得询问。

#### Scenario: 有上次产物时不询问
- **GIVEN** session 的 lastProducedAssetID 为 asset_B（图2）且在工作区
- **WHEN** 用户未选图发送"换个角色"
- **THEN** 系统使用图2作为操作对象，不触发任何"操作哪张图"澄清

#### Scenario: 只有一张图时不询问
- **GIVEN** 工作区只有一张图（图1=asset_A）
- **WHEN** 用户未选图发送"换个背景"
- **THEN** 系统直接使用图1，不触发澄清（现有逻辑，无需改动）

#### Scenario: 真正不确定时才询问
- **GIVEN** session 无 lastProducedAssetID，工作区有图1、图2两张图，用户未选图
- **WHEN** 用户发送"换个背景"
- **THEN** 系统（通过 LLM clarify_intent 或 remediationClarify）询问操作哪张图

### Requirement: 万不得已澄清时预填上次产物
仅在确实需要澄清的场景（条件2成立但条件3成立，如重启后session无lastProduced但有多图），且上次产物在工作区时，remediationClarify SHALL 将上次产物作为选项列表第一个预填选项。

#### Scenario: 降级澄清时预填上次产物
- **GIVEN** session 重启后 lastProducedAssetID 为空，但工作区有多图
- **WHEN** remediationClarify 生成"操作哪张图"的选项
- **THEN** 若能从恢复的对话历史中识别出最近编辑的图，将其作为第一选项预填

