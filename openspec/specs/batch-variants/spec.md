# batch-variants Specification

## Purpose
TBD - created by archiving change add-promo-content-suite. Update Purpose after archive.
## Requirements
### Requirement: 批量变体生成工具
系统 SHALL 新增 `generate_variants` 工具，对一个生图/改图意图按**变体策略**一次性产出 N 个 creative 变体（默认 N=4，SHALL 支持 2~8 范围）。变体维度 SHALL 至少支持：**构图/角度**、**配色基调**、**文案侧重**、**风格**。每个变体 SHALL 复用现有图生图管线与长任务机制，作为独立异步任务并行/串行推进，并各自回填工作区。该工具 SHALL 用于买量团队批量产 creative 测 CTR 的场景。

#### Scenario: 一次产出多个变体
- **WHEN** 用户请求「这张图多出 4 个不同风格的版本」
- **THEN** 系统调用 `generate_variants`，按风格维度生成 4 个变体任务
- **AND** 每个变体作为独立资产回填工作区，可分别下载

#### Scenario: 变体数量约束
- **WHEN** 用户请求生成超过上限的变体数（如 20 个）
- **THEN** 系统将数量收敛到上限（8）并提示用户
- **AND** 不发起超量任务

#### Scenario: 复用现有生图管线
- **WHEN** 任一变体发起生图
- **THEN** 其走与单图生成一致的供应商、质量与防注入约束
- **AND** 变体不绕过既有生图质量门控

### Requirement: 批量变体的实时占位与分组回填
系统 SHALL 在 `generate_variants` 触发后即时为 N 个变体插入占位骨架，并按任务进度逐个回填；同一批变体 SHALL 在工作区以**同组**呈现（共享批次标识），使前端可作为一组 creative 集中对比。任一变体失败 SHALL NOT 影响同批其他变体的产出。

#### Scenario: 逐个回填不互相阻塞
- **WHEN** 一批 4 个变体中第 2 个生成失败
- **THEN** 其余 3 个正常回填，失败项给出明确失败态
- **AND** 整批不因单个失败而整体回滚

#### Scenario: 同批变体分组呈现
- **WHEN** 一批变体陆续完成
- **THEN** 前端将其归入同一批次分组，便于横向对比
- **AND** 每个变体携带批次标识与变体维度标签

