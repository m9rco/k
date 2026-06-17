# provider-configuration (delta)

## MODIFIED Requirements

### Requirement: 供应商类型可按模型覆盖
系统 SHALL 为视觉分析能力提供可按 `Provider` 切换的适配器：当 `VISION_PROVIDER=gemini`（默认）时，视觉分析 SHALL 经 Gemini 原生 `:generateContent` 接口以 **inline base64**（`inlineData`）传入图片，文本指令作为 text part，结果取 `candidates[].content.parts[].text`，且 SHALL NOT 要求图片为公网 URL；当 `VISION_PROVIDER=openai` 时，视觉分析 SHALL 发送 OpenAI 兼容 `/chat/completions` 请求，其中 `content` 为多模态数组（`text` part + `image_url` part），支持流式输出。两种适配器 SHALL 完全独立于现有会话 chat 模型（不共用序列化路径），确保多模态内容不污染会话上下文。

#### Scenario: Gemini 视觉适配器 inline 请求
- **WHEN** `VISION_PROVIDER=gemini`，系统调用视觉分析
- **THEN** 请求经 `:generateContent`，图片以 inlineData base64 传入
- **AND** 不依赖公网 URL，不向会话 chat 模型注入多模态内容

#### Scenario: OpenAI 兼容视觉适配器多模态请求
- **WHEN** `VISION_PROVIDER=openai`，系统调用视觉分析
- **THEN** 请求 body 的 user message content 为数组，含 text part 与 image_url part
- **AND** 支持 stream:true 流式增量输出

## ADDED Requirements

### Requirement: 视觉分析与质量门控后端配置
系统 SHALL 为视觉分析后端独立解析 `VISION_PROVIDER`/`VISION_BASE_URL`/`VISION_API_KEY`/`VISION_MODEL`，默认 `provider=gemini`、`model=gemini-2.5-flash-all`，凭证按既有三层回退（专属 → 公共 COMMON → 内置默认）解析。

系统 SHALL 为适配质量门控后端独立解析 `QUALITY_PROVIDER`/`QUALITY_BASE_URL`/`QUALITY_API_KEY`/`QUALITY_MODEL`，默认 `provider=openai`、`model=doubao-seed-1-6-vision-250815`，凭证按同一三层回退解析。系统 SHALL 另解析 `QUALITY_THRESHOLD`（加权总分及格阈值，缺省 75）。当 `QUALITY_API_KEY` 解析为空时，质量门控能力 SHALL 优雅降级为「未配置」（等价于全部及格），适配行为与未引入门控时一致。

#### Scenario: 视觉后端默认 Gemini
- **WHEN** 未设任何 `VISION_*` 专属变量
- **THEN** 视觉分析 provider 解析为 gemini、model 解析为 gemini-2.5-flash-all，凭证回退 COMMON

#### Scenario: 质量门控后端默认 doubao
- **WHEN** 设置 `QUALITY_API_KEY`，未设其余 `QUALITY_*`
- **THEN** 质量门控 model 解析为 doubao-seed-1-6-vision-250815，base/key 回退专属或 COMMON
- **AND** 及格阈值解析为缺省 75

#### Scenario: 质量门控未配置时禁用
- **WHEN** `QUALITY_API_KEY` 解析为空（无专属且 COMMON 未覆盖）
- **THEN** 质量门控能力报告「未配置」，所有适配产物视为 pass
- **AND** 适配流程与未引入门控时完全一致
