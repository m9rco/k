# frontend-experience Spec Delta

## ADDED Requirements

### Requirement: Follow-up 建议渲染
聊天消息结构 SHALL 支持 `follow_up` 类型消息，前端以与 clarify 相似的 chip 列表渲染建议操作，用户可点选快速触发下一步；消息渲染按类型派发，为后续富交互预留扩展点。

#### Scenario: follow_up 消息渲染
- **WHEN** 后端推送 follow-up capsule
- **THEN** 前端以 chip 列表渲染建议操作，用户可点选快速触发下一步
- **AND** 用户可关闭（dismiss）该建议气泡

#### Scenario: 消息类型扩展不破坏现有渲染
- **WHEN** 聊天区收到未知 type 的消息
- **THEN** 降级为纯文本渲染，不崩溃

### Requirement: Context 净占比显示
Context bar SHALL 从后端接收 `systemTokens` 字段，显示对话消息净占比（`(estimatedTokens - systemTokens) / budget`，扣除 system prompt 基线）；清理 context 后净占比为 0%。

#### Scenario: 清理后净占比为 0%
- **WHEN** 用户清理 context 且新 window 仅含 system prompt
- **THEN** Context bar 显示 0%（对话消息净占比）
- **AND** 不出现清理后仍显示 ~19% 的困惑
