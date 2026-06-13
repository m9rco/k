# frontend-experience — Delta

## MODIFIED Requirements

### Requirement: 交互流畅度
系统 SHALL 在关键交互路径上消除阻塞感：列表/资产更新使用过渡或骨架占位、流式内容追加时保持滚动锚定、操作反馈即时，避免出现长时间空白或界面跳动。用户发送对话消息后，前端 SHALL 立即进入 loading 态（如占位 assistant 气泡或思考指示），不等待模型首个增量到达；该 loading 态由后端"轮开始"信号或本地即时反馈驱动，并在"轮结束"信号到达时收束。

#### Scenario: 流式滚动锚定
- **WHEN** 对话区有流式内容持续追加
- **THEN** 视图保持锚定在最新内容，不产生抖动或错位

#### Scenario: 加载过渡
- **WHEN** 工作区资产或任务状态更新
- **THEN** 以过渡/骨架平滑呈现而非空白闪烁

#### Scenario: 按钮即时反馈
- **WHEN** 用户点击按钮或可交互元素
- **THEN** 界面以即时的视觉过渡（悬停/按下/加载态）反馈操作，不出现无反馈的卡顿

#### Scenario: 发送后立即 loading
- **WHEN** 用户发送一条对话消息
- **THEN** 前端立即渲染 loading 态（占位气泡或思考指示），不等待模型首个增量
- **AND** 该 loading 态在该轮结束信号到达时结束

## ADDED Requirements

### Requirement: Capsule 反问渲染与回传
前端 SHALL 渲染 agent 下发的结构化反问（capsule）：展示问题文案与一组选项，每个选项以可点击 chip 呈现并附带一个可编辑输入入口（预填该选项的可编辑文本）。用户点击 chip SHALL 直接以该选项的值回传；用户在可编辑输入中改写后提交 SHALL 以改写文本回传。回传 SHALL 经既有 WebSocket 入站协议发送，使会话得以续接。

#### Scenario: 渲染反问选项
- **WHEN** 前端收到 capsule 事件
- **THEN** 前端在对话区展示问题文案与一组可点击选项 chip
- **AND** 每个选项附带可编辑输入入口并预填其可编辑文本

#### Scenario: 点击选项回传
- **WHEN** 用户点击某个选项 chip
- **THEN** 前端以该选项的值经入站协议回传并续接会话

#### Scenario: 改写后提交回传
- **WHEN** 用户在某选项的可编辑输入中改写内容并提交
- **THEN** 前端以改写后的文本经入站协议回传并续接会话

#### Scenario: 回传后收束反问
- **WHEN** 用户完成一次选择或改写提交
- **THEN** 该 capsule 进入已回应态，不再接受重复提交

### Requirement: Context 状态事件驱动更新
前端 SHALL 依据后端在轮结束时下发的窗口状态（估算 token、预算、是否已压缩）即时更新 context 状态展示，而非仅依赖轮询。

#### Scenario: 轮结束后刷新 context 状态
- **WHEN** 前端收到携带窗口状态的轮结束信号
- **THEN** 前端即时更新"上下文使用"展示以反映最新估算
