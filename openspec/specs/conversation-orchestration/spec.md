# conversation-orchestration Specification

## Purpose
TBD - created by archiving change add-asset-studio-mvp. Update Purpose after archive.
## Requirements
### Requirement: 意图识别与白名单分发
系统 SHALL 基于 Eino agent 识别用户意图，仅执行预设意图集合（换角色/换背景/换文案、切尺寸、下载/打包、**物料爬取、生视频**）。对集合外的请求，系统 SHALL 不执行任何工具，并礼貌返回说明及可执行能力清单。

#### Scenario: 命中预设意图
- **WHEN** 用户请求"把这张图的背景换成夜晚城市"
- **THEN** agent 识别为换背景意图并分发到生图工具
- **AND** 工具调用过程以事件形式可见于前端

#### Scenario: 命中爬取或生视频意图
- **WHEN** 用户请求"爬取某游戏素材"或"让这张图动起来"
- **THEN** agent 识别为物料爬取 / 生视频意图并分发到对应工具
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
系统 SHALL 以**真·流式**方式消费模型供应商的流式响应，并将 agent 的回复增量与思考增量分别实时推送给前端，而非先获取完整结果再切块模拟。当模型返回思考内容（reasoning/thinking）时，系统 SHALL 将其作为独立于回答正文的增量类型推送；当供应商流式解析失败时，系统 SHALL 降级为读取完整响应后补发，保证前端不空屏。

#### Scenario: 回答增量流式推送
- **WHEN** agent 生成回复
- **THEN** 系统按供应商返回的增量片段实时推送，前端可逐步渲染而非等待完整结果

#### Scenario: 思考增量独立推送
- **WHEN** 模型在回答前返回思考内容
- **THEN** 系统将思考增量以区别于回答正文的事件类型逐片推送
- **AND** 前端据此实时渲染思考块

#### Scenario: 流式失败降级
- **WHEN** 供应商流式响应解析失败
- **THEN** 系统降级为读取完整响应并补发增量
- **AND** 前端最终仍能完整呈现回答，不出现空屏

### Requirement: 裁剪工具按唯一 id 寻址
Agent 的裁剪工具（`crop_to_sizes`）SHALL 以尺寸的**全局唯一 id 列表**作为目标规格入参（而非尺寸名称），以便在 23+ 渠道、上百条尺寸、存在跨渠道同名/同尺寸的目录中精确解析每个目标规格。当请求的 id 不存在或对应尺寸不可由裁剪产出时，工具 SHALL 返回明确错误。

#### Scenario: 按 id 裁剪
- **WHEN** Agent 调用裁剪工具并传入一组尺寸 id（可跨渠道）
- **THEN** 系统按 id 精确解析各目标规格并产出对应裁剪图
- **AND** 各产物作为新的工作区资产回填

#### Scenario: 无效或不可裁剪 id 报错
- **WHEN** Agent 传入不存在的尺寸 id，或对应尺寸标记为不可裁剪产出
- **THEN** 工具不产出该尺寸的图片
- **AND** 返回可读错误，说明哪个 id 无效或不可裁剪

### Requirement: 尺寸目录列举工具
Agent 的尺寸列举工具（`list_platform_sizes`）SHALL 返回 **渠道 → 素材类型 → 尺寸（含唯一 id 与约束元数据）** 的三层结构，并 SHALL 支持可选的渠道过滤参数，使 Agent 能按需获取单个渠道的尺寸而不必将整个目录灌入模型 context。

#### Scenario: 列举全部渠道目录
- **WHEN** Agent 不带过滤参数调用列举工具
- **THEN** 系统返回三层目录结构，每个尺寸含 id、宽高、方向及可用的约束元数据

#### Scenario: 按渠道过滤列举
- **WHEN** Agent 带某个渠道标识调用列举工具
- **THEN** 系统仅返回该渠道下的素材类型与尺寸
- **AND** 避免将其余渠道的上百条尺寸纳入上下文

### Requirement: 上下文清理
系统 SHALL 允许用户主动重置当前会话的 context 滑动窗口：清除累积的对话历史（保留 system 提示），使用户可以从干净的上下文开始新话题。重置 SHALL 仅作用于当前会话，不影响其工作区资产。

#### Scenario: 重置上下文
- **WHEN** 用户触发上下文清理
- **THEN** 系统清除该会话累积的对话历史，仅保留 system 提示
- **AND** 后续对话从干净的上下文开始

#### Scenario: 清理不影响工作区
- **WHEN** 用户清理上下文
- **THEN** 工作区中已产出的资产保持不变

