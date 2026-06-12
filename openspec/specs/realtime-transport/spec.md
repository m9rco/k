# realtime-transport Specification

## Purpose
TBD - created by archiving change add-asset-studio-mvp. Update Purpose after archive.
## Requirements
### Requirement: 对话使用 WebSocket
系统 SHALL 通过 WebSocket 承载交互式对话通道，支持双向、低延迟的消息与中间步骤推送。

#### Scenario: 对话双向通信
- **WHEN** 用户在对话区发送消息
- **THEN** 系统经 WebSocket 接收并将 Agent 的增量响应与工具调用步骤回推到同一连接

### Requirement: 任务进度使用 SSE
系统 SHALL 通过 HTTP streaming（SSE）承载生图/生视频等长耗时任务的进度推送。

#### Scenario: 任务进度流
- **WHEN** 一个长耗时生成任务启动
- **THEN** 系统通过 SSE 持续推送该任务的状态变更直至完成或失败

#### Scenario: 断线重连
- **WHEN** SSE 连接中断后重新建立
- **THEN** 客户端可恢复获取任务最新状态而不丢失最终结果

