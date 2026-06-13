# session-management Specification

## Purpose
TBD - created by archiving change add-asset-studio-mvp. Update Purpose after archive.
## Requirements
### Requirement: 无登录会话创建
系统 SHALL 在用户首次进入时，基于浏览器信息（user-agent、语言、屏幕等指纹特征）生成一个匿名 session，无需注册或登录流程。

#### Scenario: 首次进入生成 session
- **WHEN** 用户首次打开 web 应用且无既有 session 标识
- **THEN** 系统根据浏览器信息生成唯一 session id 并返回给前端
- **AND** 该 session id 写入 sessionStorage

#### Scenario: 重连复用 session
- **WHEN** 用户在同一浏览器标签会话内刷新或断线重连，且 sessionStorage 中存在 session id
- **THEN** 系统复用该 session，恢复其会话上下文，不创建新 session

### Requirement: 会话上下文展示
系统 SHALL 向前端提供当前 session 的上下文状态（当前会话标识、活跃状态、近期消息/任务概要），供前端在上下文面板展示。

#### Scenario: 展示当前会话状态
- **WHEN** 前端请求当前 session 的上下文状态
- **THEN** 系统返回 session 标识与当前会话的状态摘要（如进行中的任务数、最近意图）

### Requirement: 会话级状态隔离
系统 SHALL 按 session 隔离对话上下文、任务状态与生成产物引用，不同 session 之间不可互相读取。

#### Scenario: 跨会话隔离
- **WHEN** 两个不同 session 同时操作
- **THEN** 各自的消息窗口、任务与产物互不可见，互不干扰

### Requirement: 对话历史持久化与恢复
系统 SHALL 将会话的对话消息（用户消息、agent 回复、关键工具引用）按 session 维度持久化到长期存储（SQLite），并在会话重连或进程重启后从持久化历史重建对话 context，恢复对话连续性。持久化 SHALL 仅存文本与引用 id，不存大块工具产物二进制（与"大块结果不入 context"原则一致）。恢复后的 context SHALL 仍受 token 预算约束，超预算时按既有滑动窗口压缩。无历史记录时 SHALL 按空会话启动。

#### Scenario: 对话消息落库
- **WHEN** 一轮对话结束
- **THEN** 系统将本轮用户消息与 agent 回复持久化到长期存储，按 session 关联

#### Scenario: 重启后恢复对话
- **WHEN** 进程重启或会话重连后系统首次构建该 session 的对话 context
- **THEN** 系统从持久化历史读取消息重建 context 窗口
- **AND** 后续对话在该历史基础上续接

#### Scenario: 恢复后仍受预算约束
- **WHEN** 持久化历史重建后的窗口超出 token 预算
- **THEN** 系统按既有滑动窗口策略对其压缩

#### Scenario: 不持久化大块产物
- **WHEN** 某轮包含大体积工具产物（如图片二进制）
- **THEN** 系统仅持久化其引用 id 与文本摘要，不持久化原始二进制

#### Scenario: 无历史时空启动
- **WHEN** 某 session 无任何持久化对话历史
- **THEN** 系统按空会话窗口启动，行为与无持久化时一致

