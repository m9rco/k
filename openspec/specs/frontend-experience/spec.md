# frontend-experience Specification

## Purpose
TBD - created by archiving change add-asset-studio-mvp. Update Purpose after archive.
## Requirements
### Requirement: 品牌化科技感界面
系统 SHALL 提供精简、强科技感的品牌化界面，传达明确的产品与服务意识。

#### Scenario: 首屏品牌呈现
- **WHEN** 用户进入应用
- **THEN** 界面以精简科技感视觉呈现品牌与核心能力入口

### Requirement: 响应式与渐进式体验
系统 SHALL 采用响应式布局、骨架屏、CSS 过渡动效，并使用 sessionStorage 维持前端会话态。

#### Scenario: 加载骨架屏
- **WHEN** 数据或资产正在加载
- **THEN** 界面展示骨架屏占位而非空白
- **AND** 内容就绪后以过渡动效平滑替换

#### Scenario: 刷新保持会话态
- **WHEN** 用户刷新页面
- **THEN** 前端从 sessionStorage 恢复当前会话上下文展示

### Requirement: Agent 式对话与工具调用呈现
系统 SHALL 提供参考主流 Agent 产品（如 Cursor）的对话区，清晰展示工具调用过程、当前状态，并严格受控地呈现 context 状态与当前会话信息。

#### Scenario: 展示工具调用
- **WHEN** Agent 调用某个工具（生图/裁剪/下载等）
- **THEN** 对话区以结构化卡片展示该工具调用及其状态与结果

#### Scenario: Context 状态面板
- **WHEN** 会话进行中
- **THEN** 界面展示当前 context 使用状态与当前会话信息

### Requirement: 异常小弹窗通知
系统 SHALL 以小弹窗（toast）形式展示异常与提示信息。

#### Scenario: 异常通知
- **WHEN** 某操作发生异常
- **THEN** 界面以小弹窗展示简要错误信息，不阻断其余操作

### Requirement: 偏好角落占位
系统 SHALL 预留一个偏好展示角落；在没有任何可展示偏好/操作记录时该角落不展示。

#### Scenario: 无偏好时隐藏
- **WHEN** 用户尚无任何可分析的偏好记录
- **THEN** 偏好角落不展示

### Requirement: 分层尺寸选择器
前端 SHALL 以**分层选择器**呈现尺寸目录，替代原先的"平台分组一行平铺胶囊"方式，以承载 23+ 渠道、上百条尺寸的规模。选择器 SHALL 支持：先按渠道筛选/搜索 → 在选中渠道内按素材类型分组展示尺寸胶囊 → 跨渠道累加多选 → 一次性确认批量裁剪。

#### Scenario: 渠道筛选与搜索
- **WHEN** 用户打开尺寸选择器
- **THEN** 前端展示按速查分组（如外渠/手机厂商/腾讯内渠/PC）组织的渠道列表
- **AND** 提供渠道搜索框以快速定位渠道

#### Scenario: 渠道内按素材类型展示
- **WHEN** 用户选中某个渠道
- **THEN** 前端按素材类型（截图/ICON/视频封面/推广图/资源位/H5 等）分组展示该渠道的尺寸胶囊
- **AND** 每个胶囊展示尺寸名、宽×高

#### Scenario: 跨渠道多选与批量确认
- **WHEN** 用户在不同渠道下分别勾选若干尺寸
- **THEN** 前端将所选项聚合到"已选 N 项"区域并保持跨渠道累加
- **AND** 用户确认后以所选尺寸的唯一 id 列表发起一次批量裁剪

### Requirement: 尺寸约束的视觉提示
前端 SHALL 将尺寸的约束元数据（格式、文件大小上限、语义备注如"无文案/圆角/透明底"）作为胶囊上的角标或 tooltip 展示，帮助用户在选择前了解规格要求，但这些提示 SHALL NOT 阻止用户选择或裁剪。

#### Scenario: 展示约束提示
- **WHEN** 某尺寸带有 format / maxKB / note 元数据
- **THEN** 前端在对应胶囊上以小标注或 tooltip 呈现这些约束
- **AND** 用户仍可正常选择该尺寸

#### Scenario: 不可裁剪规格置灰
- **WHEN** 某尺寸标记为不可由裁剪产出（producible=false，如视频规格）
- **THEN** 前端将该胶囊置灰为不可选状态
- **AND** 通过提示说明该规格无法通过裁剪产出

