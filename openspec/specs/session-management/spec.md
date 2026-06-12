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

