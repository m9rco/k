# Change: 多供应商模型接入(逻辑推理/图生图/文生图/图生视频可配置切换)

## Why
当前各能力的模型供应商单一、且部分形态硬编码:会话理解默认 deepseek/claude 两协议分支、生图固定 OpenAI `images/edits`(gpt-image)、生视频固定 happyhorse(DashScope 异步)。业务希望按厂商接入一批新模型,并能**纯通过配置切换**:

- 逻辑推理(主 agent):`doubao-seed-2-0-mini-260428`、`gpt-5.4`、`claude-haiku-4-5-20251001`、`claude-sonnet-4-6`
- 图生图:`gemini-3-pro-image`、`gemini-2.5-flash-image`、`gemini-3.1-flash-image`、`gemini-3.1-flash-image-preview`、`gpt-image-2`
- 文生图:`wan2.7-image-pro`、`qwen-image-2.0-2026-03-03`
- 图生视频:`veo_3_1_fast_components_vip`、`veo_3_1_components_vip`

全部经 yunwu 代理(https://yunwu.apifox.cn/)访问。已确认采用**手写轻量 HTTP 适配**(不引厂商 SDK,守 project.md「二进制轻量」约束),多数模型走 OpenAI 兼容形态、形态不同者各写一个适配器,并由配置选型。本变更建立在已合入的 `provider-configuration`(每模型 provider/base_url/api_key 三层回退)之上。

## What Changes
- **provider 适配器 dispatch(core 抽象)**:为生图、生视频引入「按配置 `Provider`/format 选具体适配器」的工厂层(会话模型已有 `provider` 分支,沿用)。新增适配器实现统一接口,经配置即可切换,默认行为不变。
- **逻辑推理(会话理解)**:确认 doubao/gpt-5.4 走现有 `openai` 分支、claude 走 `anthropic` 分支,经配置(`CHAT_PRIMARY_PROVIDER/_MODEL/_BASE_URL`)切换;补齐 doubao 等模型在 reasoning/thinking 字段上的解析差异。规格「模型服务端硬编码」MODIFIED 为「服务端配置驱动、用户不可选」。
- **图生图**:新增 Gemini 图像适配器(若 yunwu 暴露为 OpenAI `images/*` 兼容则复用现有 provider、仅配置;若为原生 `generateContent`+inline_data 则新写适配器)。`gpt-image-2` 走现有 provider。主/备失效切换语义不变。
- **文生图(新能力 `text-to-image`)**:新增「纯文本→图」适配器(wan/qwen,DashScope 异步 task 轮询),并新增对应 agent 工具与工作区入口;复用既有异步任务/进度/回填管线。**BREAKING**:无,纯新增。
- **图生视频**:新增 Veo 适配器(异步 task,请求/轮询/结果字段按 yunwu 文档),作为 `video-generation` 的可切换 provider;happyhorse 保留。
- **配置**:为生图/生视频/文生图各后端补 `*_PROVIDER`/`*_FORMAT`(适配器选择键)文档与默认值;`.env.example` 列出全部新模型样例。
- **测试**:每类适配器以 httptest 模拟厂商响应做表驱动单测;config 选型单测;失效切换/降级路径单测。

不在范围(Non-Goals):
- 不引入厂商官方 SDK;不改实时传输/工作区 UI 框架。
- 不做模型质量/成本路由(仅静态配置选型)。
- 文生视频(text-to-video)、流式生图不在本次范围。

## Impact
- Affected specs: `provider-configuration`(适配器选型键)、`conversation-orchestration`(模型配置驱动)、`image-generation`(Gemini 适配器/多形态)、`video-generation`(Veo 适配器)、`text-to-image`(新增)
- Affected code:
  - `internal/agent/chatmodel.go`(provider 分支补全:doubao reasoning 解析、gpt-5.4)
  - `internal/generation/`(适配器接口 + Gemini 适配器 + 工厂选型)
  - `internal/video/`(Veo 适配器 + 工厂选型)
  - 新增 `internal/texttoimage/`(或并入 generation)+ agent 工具 + 工作区入口
  - `internal/config/config.go`、`cmd/server/main.go`(装配选型)、`.env.example`
- 兼容:未改配置的现有部署保持原供应商与行为不变。
