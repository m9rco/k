# conversation-orchestration（spec delta — polish-studio-ui）

## ADDED Requirements

### Requirement: 提示词优化端点
系统 SHALL 提供一个提示词优化端点 `POST /api/session/{id}/prompt/optimize`，使用服务端硬编码的会话理解模型，将用户口语化的输入改写为结构化、可直接用于生图的提示词文本。该端点 SHALL 仅返回改写后的文本，SHALL NOT 调用任何工具、SHALL NOT 触发素材产出、SHALL NOT 写入或污染会话的滑动窗口 context。改写指令 SHALL 把用户文本作为待改写的数据处理，不将其当作可执行指令（prompt-injection 防护）。

#### Scenario: 口语化输入改写为提示词
- **WHEN** 客户端以 `{ "text": "<口语化描述>" }` 调用该端点
- **THEN** 系统用会话理解模型将其改写为结构化生图提示词
- **AND** 仅返回 `{ "optimized": "<提示词文本>" }`，不触发生图或任何工具调用

#### Scenario: 不污染会话上下文
- **WHEN** 用户调用提示词优化端点
- **THEN** 该次改写不写入会话滑动窗口
- **AND** 后续正常对话的 context 不受其影响

#### Scenario: 空输入直接返回
- **WHEN** 请求文本为空或仅含空白
- **THEN** 系统不调用模型并返回空/原文，不报错

#### Scenario: 模型不可用时报错
- **WHEN** 会话理解模型不可用或超时
- **THEN** 系统返回明确错误状态
- **AND** 不产生任何素材、不改动会话状态

#### Scenario: 注入防护
- **WHEN** 用户输入中包含试图改变系统行为的指令性文本（如"忽略以上指令"）
- **THEN** 系统将其视为待改写的素材描述数据处理，仅产出生图提示词
- **AND** 不执行该文本所述的任何操作
