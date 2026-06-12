# conversation-orchestration spec delta

## ADDED Requirements

### Requirement: 裁剪工具按唯一 id 寻址
Agent 的裁剪工具（`crop_to_sizes`）SHALL 以尺寸的**全局唯一 id 列表**作为目标规格入参（而非尺寸名称），以便在 23+ 渠道、上百条尺寸、存在跨渠道同名/同尺寸的目录中精确解析每个目标规格。当请求的 id 不存在或对应尺寸不可由裁剪产出时，工具 SHALL 返回明确错误。

#### Scenario: 按 id 裁剪
- **WHEN** Agent 调用裁剪工具并传入一组尺寸 id（可跨渠道）
- **THEN** 系统按 id 精确解析各目标规格并产出对应裁剪图
- **AND** 各产物作为新的工作区资产回填

#### Scenario: 无效或不可裁剪 id 报错
- **WHEN** Agent 传入不存在的尺寸 id，或对应尺寸标记为不可裁剪产出
- **THEN** 工具不产出该尺寸的图片
- **AND** 返回可读错误，说明哪个 id 无效或不可裁剪

### Requirement: 尺寸目录列举工具
Agent 的尺寸列举工具（`list_platform_sizes`）SHALL 返回 **渠道 → 素材类型 → 尺寸（含唯一 id 与约束元数据）** 的三层结构，并 SHALL 支持可选的渠道过滤参数，使 Agent 能按需获取单个渠道的尺寸而不必将整个目录灌入模型 context。

#### Scenario: 列举全部渠道目录
- **WHEN** Agent 不带过滤参数调用列举工具
- **THEN** 系统返回三层目录结构，每个尺寸含 id、宽高、方向及可用的约束元数据

#### Scenario: 按渠道过滤列举
- **WHEN** Agent 带某个渠道标识调用列举工具
- **THEN** 系统仅返回该渠道下的素材类型与尺寸
- **AND** 避免将其余渠道的上百条尺寸纳入上下文
