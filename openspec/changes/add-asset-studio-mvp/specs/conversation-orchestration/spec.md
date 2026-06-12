## ADDED Requirements

### Requirement: 意图识别与白名单分发
系统 SHALL 基于 Eino agent 识别用户意图，仅执行预设意图集合（换角色/换背景/换文案、切尺寸、下载/打包，以及预留的生视频、物料爬取）。对集合外的请求，系统 SHALL 不执行任何工具，并礼貌返回说明及可执行能力清单。

#### Scenario: 命中预设意图
- **WHEN** 用户请求"把这张图的背景换成夜晚城市"
- **THEN** agent 识别为换背景意图并分发到生图工具
- **AND** 工具调用过程以事件形式可见于前端

#### Scenario: 超出预设意图礼貌拒绝
- **WHEN** 用户请求与素材生成无关的任务（如"帮我写一封邮件"）
- **THEN** 系统不执行任何工具
- **AND** 返回礼貌说明，并列出当前支持的能力

### Requirement: Context 滑动窗口管理
系统 SHALL 对会话消息维护一个 token 预算受限的滑动窗口：超出预算时保留 system 提示与最近若干轮原文，对更早轮次做摘要压缩为单条 summary 消息，以防止 context 膨胀导致模型输出失真。

#### Scenario: 超预算触发压缩
- **WHEN** 会话消息累计超过配置的 token 预算
- **THEN** 系统将较早的历史轮次压缩为一条 summary 消息并保留最近轮次原文
- **AND** 后续模型调用使用压缩后的窗口

#### Scenario: 大块工具结果不入 context
- **WHEN** 工具返回大体积结果（如图片二进制/base64）
- **THEN** 系统仅以引用 id 形式将结果纳入对话上下文，不将原始大块数据送入 LLM context

### Requirement: 模型服务端硬编码
系统 SHALL 在服务端硬编码会话理解模型配置（主：claude-sonnet-4-6；测试：DeepSeek chat 经 OpenAI 兼容端点），用户不可选择或切换。

#### Scenario: 用户不可切换模型
- **WHEN** 用户尝试指定使用某个模型
- **THEN** 系统忽略该指定并使用服务端配置的模型

### Requirement: 流式对话输出
系统 SHALL 以流式方式将 agent 的回复增量推送给前端。

#### Scenario: 流式回复
- **WHEN** agent 生成回复
- **THEN** 系统按增量片段推送，前端可逐步渲染而非等待完整结果
