## MODIFIED Requirements

### Requirement: 供应商类型可按模型覆盖
系统 SHALL 为视觉分析能力提供独立的 HTTP 适配器（`vision`），该适配器 SHALL 发送 OpenAI 兼容 `/chat/completions` 请求，其中 `content` 字段为多模态数组（混合 `text` part 和 `image_url` part），而非纯文本字符串。该适配器 SHALL 支持**流式**（`stream:true`）输出，逐 SSE chunk 转为对话事件增量推送。`vision` 适配器 SHALL 完全独立于现有会话 chat 模型（不共用序列化路径），确保多模态内容不污染会话上下文。

#### Scenario: 视觉适配器发送多模态请求
- **WHEN** 系统调用视觉分析
- **THEN** 请求 body 的 user message content 为数组，含 text part（分析指令）和每个图片 URL 的 image_url part
- **AND** 不向会话 chat 模型的序列化路径注入多模态内容

#### Scenario: 视觉适配器流式输出
- **WHEN** 视觉分析请求以 stream:true 发出
- **THEN** 响应逐 chunk 转为对话 EventMessage 增量推送到 web
- **AND** 完整报告文本在流结束后可供后续步骤使用

## ADDED Requirements

### Requirement: grok-4-fast 视觉分析模型目录项
系统模型目录 SHALL 包含 `grok-4-fast` 条目，归入新的**视觉分析**（`analysis`）场景，使用 yunwu 公共网关（`COMMON_BASE_URL`/`COMMON_API_KEY`），适配器选型键为 `vision`。当 yunwu 公共凭证未配置时，该模型对应的视觉分析能力 SHALL 不可用（不崩溃，调用方收到明确的「不可用」信号）。

#### Scenario: 经 yunwu 公共凭证使用 grok-4-fast
- **WHEN** 系统发起视觉分析调用，公共凭证已配置
- **THEN** 请求以 grok-4-fast 发送至 yunwu 公共网关
- **AND** 分析正常执行

#### Scenario: 公共凭证未配置时降级
- **WHEN** 公共凭证（COMMON_BASE_URL/COMMON_API_KEY）未配置
- **THEN** 视觉分析能力不可用，返回明确信号而非崩溃
