# frontend-experience Spec Delta

## MODIFIED Requirements

### Requirement: Agent 式对话与工具调用呈现（MODIFIED）
聊天消息结构 SHALL 新增 `type` 字段，支持按类型派发渲染组件，为后续富交互预留扩展点。当前实现 `text | tool_call | clarify | follow_up`；新增 type 只需注册渲染器，无需改动消息框架。

#### Scenario: follow_up 消息渲染
- **WHEN** 后端推送 follow-up capsule
- **THEN** 前端以与 clarify 相似的 chip 列表渲染建议操作，用户可点选快速触发下一步

#### Scenario: 消息类型扩展不破坏现有渲染
- **WHEN** 聊天区收到未知 type 的消息
- **THEN** 降级为纯文本渲染，不崩溃

### Requirement: Context 状态面板（MODIFIED）
Context bar SHALL 从后端接收 `systemTokens` 字段，显示对话消息净占比（扣除 system prompt 基线）；清理 context 后净占比为0%。

#### Scenario: 清理后净占比为0%
- **WHEN** 用户清理 context 且新 window 仅含 system prompt
- **THEN** Context bar 显示 0%（对话消息净占比）
- **AND** 不出现清理后仍显示19%的困惑
