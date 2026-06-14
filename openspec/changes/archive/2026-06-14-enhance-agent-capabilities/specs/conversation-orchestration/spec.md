# conversation-orchestration Spec Delta

## MODIFIED Requirements

### Requirement: 意图识别与白名单分发
系统 SHALL 仅就预设意图（换背景/换角色/换文案/切尺寸/**生成 icon**/生视频/**联网搜索/图片搜索**/下载打包）调用对应工具，其余请求礼貌拒绝并列出能力清单；其中「物料爬取」已移除（由图片搜索替代）。工具调用的完成事件 SHALL 结构化携带其所产生长任务的标识，使前端能够即时定位并订阅该任务的进度，而不必等待本轮对话结束。系统 SHALL 减少 tools=0 出现频率：当意图明确且关键参数充足时必须直接调工具，非关键参数可合理推断而无需澄清。

#### Scenario: 命中白名单意图
- **WHEN** 用户请求命中预设意图且关键参数充足
- **THEN** 系统在本轮直接调用对应工具，不先发文字确认再调用

#### Scenario: 意图充足时不过度澄清
- **WHEN** 用户说"帮我换个背景"且工作区只有一张图
- **THEN** 系统直接推断使用该图作为 source，调用 edit_image，而非先问"请问要换哪张图"

#### Scenario: 搜索意图纳入白名单
- **WHEN** 用户请求"搜索/查找 XXX 图片"
- **THEN** Agent 识别为图片搜索意图并分发到 search_images 工具

#### Scenario: 命中生成 icon 意图
- **WHEN** 用户请求"为某张图生成相关 icon"
- **THEN** 系统识别为生成 icon 意图并分发到图生 icon 工具（图生图大模型）
- **AND** 工具调用以事件形式可见，并携带其异步任务标识

#### Scenario: 工具完成事件携带任务标识
- **WHEN** 某工具调用成功并产生了一个异步长任务（生图/二次调整/生成 icon/生视频/搜索图片）
- **THEN** 该工具的完成事件在结果数据中携带该任务的 id 及任务类型（如 generate/video）
- **AND** 前端据此即时插入占位并订阅该任务的进度流

#### Scenario: 非长任务工具不携带任务标识
- **WHEN** 工具调用不产生异步长任务（如列举尺寸、纯裁剪即时返回）
- **THEN** 其完成事件不携带长任务 id
- **AND** 前端不会为其插入占位骨架

#### Scenario: 白名单外请求被拒绝
- **WHEN** 用户请求不在预设意图内
- **THEN** 系统不调用任何工具并礼貌说明能力范围

## ADDED Requirements

### Requirement: 任务后主动反馈
系统 SHALL 在整轮所有工具调用结束后，统一向用户推送一条跟进建议（follow-up capsule），包含简短说明与 2-3 个操作选项（如"生成视频""换背景""下载"），引导用户继续交互。follow-up SHALL 在轮结束（turn_end）时机发出，而非每个任务 done 事件单独触发。

#### Scenario: 整轮结束后统一推送建议
- **WHEN** 一轮所有工具调用完成（turn_end 发出）且本轮产生了可操作产物
- **THEN** 系统在 turn_end 后推送 follow-up capsule，展示"接下来要？"类建议操作
- **AND** 若本轮多个任务均完成，follow-up 合并为一条而非多条

#### Scenario: 无产物的轮次不推送
- **WHEN** 本轮未产生任何新产物（如仅 clarify 或纯文字回复）
- **THEN** 系统不推送 follow-up capsule

### Requirement: Context 净使用量下发
系统 SHALL 在窗口状态中额外下发 `systemTokens`（system prompt 自身的 token 消耗），使前端能展示**对话消息**净占比（`(estimatedTokens - systemTokens) / budget`）；清理 context 后净占比 SHALL 为 0%，避免用户误以为清理无效。

#### Scenario: 下发 systemTokens 字段
- **WHEN** 一轮对话结束，系统下发窗口状态
- **THEN** 状态中包含 `systemTokens` 字段，表示 system prompt 的基线 token 成本

#### Scenario: 清理后净占比为 0%
- **WHEN** 用户点击"清上下文"，窗口仅剩 system prompt
- **THEN** 前端 Context bar 对话净占比显示 0%（system prompt token 不计入显示值）
