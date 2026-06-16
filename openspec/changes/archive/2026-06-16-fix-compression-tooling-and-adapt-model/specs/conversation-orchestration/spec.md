# conversation-orchestration Spec Delta

## MODIFIED Requirements

### Requirement: Context 滑动窗口管理
系统 SHALL 对会话消息维护一个 token 预算受限的滑动窗口：超出预算时保留 system 提示与最近若干轮原文，对更早轮次做摘要压缩为单条 summary 消息，以防止 context 膨胀导致模型输出失训。token 估算 SHALL 对 CJK 字符与 ASCII 字节分别估算以降低中文场景的偏差。系统 SHALL 能在每轮结束时向前端下发当前窗口状态（估算 token、预算、是否已压缩）。

压缩 SHALL 不产生孤立 ToolMessage（role:tool 开头的 recent 切片），以避免 provider 拒绝或静默丢弃非法序列。当折叠边界落在 `assistant{tool_calls}` 与其对应 `role:tool` 之间时，系统 SHALL 向 recent 方向前移边界直到 recent 不以孤立 tool 消息开头；无法安全分割时 SHALL 跳过本轮压缩。

#### Scenario: 超预算触发压缩
- **WHEN** 会话消息累计超过配置的 token 预算
- **THEN** 系统将较早的历史轮次压缩为一条 summary 消息并保留最近轮次原文
- **AND** 后续模型调用使用压缩后的窗口

#### Scenario: 大块工具结果不入 context
- **WHEN** 工具返回大体积结果（如图片二进制/base64）
- **THEN** 系统仅以引用 id 形式将结果纳入对话上下文，不将原始大块数据送入 LLM context

#### Scenario: CJK 与 ASCII 分别估算 token
- **WHEN** 会话消息混合中文与英文/符号
- **THEN** 系统对 CJK 字符与 ASCII 字节使用不同的字符/ token 比率分别估算并合计
- **AND** 该估算用于窗口压缩决策

#### Scenario: 窗口状态下发
- **WHEN** 一轮对话结束
- **THEN** 系统向前端提供当前窗口的估算 token、预算与是否已压缩的状态

## ADDED Requirements

### Requirement: 压缩后工具调用连续性
当会话历史中曾经出现过工具调用，系统 SHALL 保证压缩后的 recent 窗口中至少保留一段完整的 `assistant{tool_calls}→role:tool` 交换，作为模型继续调用工具的 few-shot 锚点。当折叠操作无法在满足此约束的同时保持序列合法时，系统 SHALL 减少本轮折叠量（back-off）而非折叠掉最后一个工具交换对；若无论如何都无法保留则 SHALL 跳过本轮压缩。**上下文清理（ResetContext）重置窗口后不受此约束，因新窗口不存在历史工具调用**。

#### Scenario: 压缩后 recent 保留工具调用样例
- **WHEN** 会话历史里曾调用过工具且达到压缩阈值
- **THEN** 压缩后 recent 窗口仍包含至少一对完整的 `assistant{tool_calls}→role:tool`
- **AND** 模型在后续轮次中继续调用工具，不发生因压缩导致的工具调用退化

#### Scenario: 纯聊天会话压缩不受约束
- **WHEN** 会话历史里从未调用过任何工具
- **THEN** 压缩操作按原有逻辑折叠，不受"保留工具交换"约束
- **AND** 压缩行为与未引入此保护前一致

#### Scenario: 上下文清理后不继承旧工具锚点
- **WHEN** 用户触发上下文清理
- **THEN** 会话窗口重置为仅含 system 提示的干净状态，"历史曾调用工具"标记随之清除
- **AND** 清理后第一轮不因旧历史的工具保护约束而影响压缩行为

### Requirement: 压缩与模型切换的诊断可观测性
系统 SHALL 在每轮对话结束时于服务端日志输出结构化字段，包括：当前轮使用的 chat model id、窗口是否已压缩、模型本轮所见窗口中是否存在完整工具交换对（基于送入模型的消息判定）、本轮真实工具执行数量。该日志 SHALL 与现有 turn-end 日志合并为一行，不新增额外日志级别。

#### Scenario: 日志包含压缩与工具状态
- **WHEN** 一轮对话结束
- **THEN** 服务端日志包含 model、compressed 标志、has_tool_exchange 标志、工具执行数
- **AND** 该日志可用于判断"压缩/切换/清理"三种触发因子中哪个与工具调用退化相关

### Requirement: 再次请求即再次执行
系统的 System Prompt SHALL 明确约束 Agent：历史轮次中已经完成过的操作，不代表本轮无需再做。当用户本轮再次发起一个命中能力白名单的请求（哪怕与此前完全相同），Agent SHALL 再次调用对应工具重新生成产物，SHALL NOT 以「之前已经做过 / 产物已在工作区 / 你可以查看图N」为由跳过工具调用或仅以文字回复。该约束 SHALL 同时覆盖通用核心规则与平台适配（adapt_to_platform）专项规则。

#### Scenario: 再次发起同一适配要重新生成
- **WHEN** 用户对一张此前已适配过的图，再次发起到相同平台尺寸的适配
- **THEN** Agent 再次调用 adapt_to_platform 重新生成
- **AND** 不以「之前已做过 / 可查看图N」为由跳过工具调用

#### Scenario: 再次发起同一图生图要重新执行
- **WHEN** 用户再次发起一个与历史轮次相同的换背景/换角色/换文案请求
- **THEN** Agent 再次调用对应生图工具重新生成
- **AND** 工作区出现新的产物而非仅被告知历史产物
