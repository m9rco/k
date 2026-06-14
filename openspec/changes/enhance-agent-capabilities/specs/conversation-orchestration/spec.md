# conversation-orchestration Spec Delta

## MODIFIED Requirements

### Requirement: 意图识别与白名单分发
系统 SHALL 仅就预设意图调用工具，其余请求礼貌拒绝。白名单 **MODIFIED** 为：换背景/换角色/换文案/切尺寸/生成icon/生视频/**联网搜索/图片搜索**/下载打包；移除"物料爬取"（由搜索工具替代）。系统 SHALL 减少 tools=0 出现频率：当意图明确且信息充足时必须直接调工具，非关键参数可合理推断而无需澄清。

#### Scenario: 命中白名单意图
- **WHEN** 用户请求命中预设意图且关键参数充足
- **THEN** 系统在本轮直接调用对应工具，不先发文字确认再调用

#### Scenario: 意图充足时不过度澄清
- **WHEN** 用户说"帮我换个背景"且工作区只有一张图
- **THEN** 系统直接推断使用该图作为 source，调用 edit_image，而非先问"请问要换哪张图"

#### Scenario: 搜索意图纳入白名单
- **WHEN** 用户请求"搜索/查找 XXX 图片"
- **THEN** Agent 识别为图片搜索意图并分发到 search_images 工具

### Requirement: 任务后主动反馈
系统 SHALL 在整轮所有工具调用结束后，统一向用户推送一条跟进建议（follow-up capsule），包含简短说明与2-3个操作选项（如"生成视频""换背景""下载"），引导用户继续交互。follow-up SHALL 在轮结束（turn_end）时机发出，而非每个任务 done 事件单独触发。

#### Scenario: 整轮结束后统一推送建议
- **WHEN** 一轮所有工具调用完成（turn_end 发出）且本轮产生了可操作产物
- **THEN** 系统在 turn_end 后推送 follow-up capsule，展示"接下来要？"类建议操作
- **AND** 若本轮多个任务均完成，follow-up 合并为一条而非多条

#### Scenario: 无产物的轮次不推送
- **WHEN** 本轮未产生任何新产物（如仅 clarify 或纯文字回复）
- **THEN** 系统不推送 follow-up capsule

### Requirement: 流式对话输出（MODIFIED）
新增：当流式主路径降级为 fallback（读完整响应）时，ReasoningContent SHALL 按固定分片（≤32字符/片）逐步 emit，而非整段一次性推送，保持打字机一致性。

#### Scenario: fallback 路径思考内容分片
- **WHEN** 供应商流式解析失败降级为完整响应
- **THEN** ReasoningContent 被切分为若干小段，每段作为独立 reasoning delta 推送
- **AND** 前端思考块呈逐字涌现效果，不出现整段突现

### Requirement: Context 窗口状态下发（MODIFIED）
后端 ContextState SHALL 新增 `systemTokens` 字段，表示 system prompt 自身的 token 消耗；前端 Context bar SHALL 显示**对话消息**净占比（`(estimatedTokens - systemTokens) / budget`），清理 context 后净占比 SHALL 为0%。

#### Scenario: 清理后 Context 显示0%
- **WHEN** 用户点击"清上下文"
- **THEN** Context bar 对话占比降为0%（system prompt token 不计入显示值）
- **AND** 不再让用户误以为清理无效
