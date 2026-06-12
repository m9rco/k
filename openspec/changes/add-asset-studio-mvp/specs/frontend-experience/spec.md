## ADDED Requirements

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
