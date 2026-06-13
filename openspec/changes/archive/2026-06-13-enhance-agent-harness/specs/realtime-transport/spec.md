# realtime-transport — Delta

## ADDED Requirements

### Requirement: 轮生命周期事件
系统 SHALL 通过 WebSocket 下发对话"轮开始"与"轮结束"事件，使前端能在不依赖首个回答/思考增量的前提下管理 loading 态。轮开始事件 SHALL 在系统接收用户消息后、调用模型前发出；轮结束事件 SHALL 在该轮处理终止（完成、出错或产出反问）时发出，并携带本轮收尾元信息（是否调用工具、是否产出 capsule）。

#### Scenario: 下发轮开始事件
- **WHEN** 系统接收到用户消息并准备处理
- **THEN** 系统通过 WebSocket 下发轮开始事件，先于任何思考或回答增量

#### Scenario: 下发轮结束事件
- **WHEN** 一轮处理终止
- **THEN** 系统通过 WebSocket 下发轮结束事件并携带是否调用工具、是否产出 capsule 的元信息

### Requirement: Capsule 反问出站与回传入站协议
系统 SHALL 定义结构化反问（capsule）的双向 WebSocket 协议。出站 capsule 事件 SHALL 携带一句问题与一组选项，每个选项含展示文案、回传值与可选的可编辑预填文本。入站 SHALL 接受 capsule 选择消息，携带用户选中的值或改写后的文本，使系统据此续接同一会话的处理。

#### Scenario: 下发 capsule 反问
- **WHEN** agent 发起结构化反问
- **THEN** 系统下发 capsule 事件，含问题文案与一组（含展示文案、回传值、可选可编辑预填）的选项

#### Scenario: 接收 capsule 选择回传
- **WHEN** 用户点击某选项或改写预填文本后提交
- **THEN** 系统经入站 capsule 选择消息接收其值或文本
- **AND** 据此续接该会话的后续对话处理

#### Scenario: 未知事件类型不致错
- **WHEN** 客户端收到其不识别的事件类型
- **THEN** 客户端忽略该事件且不报错（向后兼容加法式协议演进）
