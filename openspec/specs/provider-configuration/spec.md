# provider-configuration Specification

## Purpose
TBD - created by archiving change refactor-per-model-provider-config. Update Purpose after archive.
## Requirements
### Requirement: 每模型端点配置三层回退
系统 SHALL 为每个模型后端(会话主模型、会话测试模型、生图主供应商、生图备供应商、生视频供应商)独立解析 `provider`、`base_url`、`api_key` 三个字段,每个字段按以下优先级取第一个非空值:专属环境变量 `<PREFIX>_<FIELD>` → 公共默认 → 内置默认。字段之间相互独立,部分覆盖 MUST 不影响其余字段的回退。

`<PREFIX>` 取值:`CHAT_PRIMARY`、`CHAT_TEST`、`IMAGE_PRIMARY`、`IMAGE_BACKUP`、`VIDEO`。

#### Scenario: 仅设公共凭证,所有后端继承
- **WHEN** 仅设置 `COMMON_API_KEY` 与 `COMMON_BASE_URL`,未设任何 `<PREFIX>_*`
- **THEN** 五个模型后端的 `api_key` 与 `base_url` 均解析为该公共值

#### Scenario: 单后端专属变量覆盖公共值
- **WHEN** 同时设置 `COMMON_API_KEY` 与 `IMAGE_PRIMARY_API_KEY`
- **THEN** 生图主供应商的 `api_key` 取 `IMAGE_PRIMARY_API_KEY`,其余后端仍取 `COMMON_API_KEY`

#### Scenario: 字段级部分覆盖
- **WHEN** 某后端只设置了 `<PREFIX>_BASE_URL`,未设 `<PREFIX>_API_KEY`
- **THEN** 该后端 `base_url` 取专属值,`api_key` 回退公共值,两者互不影响

#### Scenario: 全部缺省回退内置默认
- **WHEN** 既无专属变量也无公共变量
- **THEN** 各字段解析为其内置默认(`base_url` 为内置默认 URL,`provider` 为后端内置默认,`api_key` 为空)

### Requirement: 语义中立的公共回退凭证
系统 SHALL 以语义中立的 `COMMON_PROVIDER` / `COMMON_BASE_URL` / `COMMON_API_KEY` 作为所有后端的公共回退来源,使更换某个模型的供应商不要求改动公共配置的命名或语义。

#### Scenario: COMMON_* 作为公共来源
- **WHEN** 设置 `COMMON_BASE_URL=https://x/v1`、`COMMON_API_KEY=sk-c`
- **THEN** 未单独覆盖的后端 `base_url`/`api_key` 解析为该值

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

### Requirement: 向后兼容的旧变量别名
系统 SHALL 将既有变量识别为公共/默认之间的兼容别名,优先级低于 `<PREFIX>_*` 与 `COMMON_*`、高于内置默认,使仅配置旧变量的现有部署行为不回归。别名映射 MUST 为:`YUNWU_BASE_URL`→公共 `base_url`、`YUNWU_API_KEY`→公共 `api_key`、`DEEPSEEK_API_KEY`→会话测试模型 `api_key`、`HAPPYHORSE_BASE_URL`/`HAPPYHORSE_API_KEY`/`HAPPYHORSE_MODEL`→生视频对应字段、`CRAWL_ENDPOINT`→crawl `base_url`。

#### Scenario: 仅旧 YUNWU 变量的现有部署
- **WHEN** 仅设置 `YUNWU_API_KEY` 与 `YUNWU_BASE_URL`,无 `COMMON_*`/专属变量
- **THEN** 所有模型后端解析出与该变更前一致的 `api_key`/`base_url`

#### Scenario: COMMON 优先于 YUNWU 别名
- **WHEN** 同时设置 `COMMON_API_KEY` 与 `YUNWU_API_KEY`
- **THEN** 公共 `api_key` 取 `COMMON_API_KEY`

#### Scenario: 旧 HAPPYHORSE 变量映射到 video
- **WHEN** 仅设置 `HAPPYHORSE_API_KEY`/`HAPPYHORSE_MODEL`,无 `VIDEO_*`
- **THEN** 生视频后端的 `api_key`/`model` 取对应 `HAPPYHORSE_*` 值

### Requirement: 爬取后端纳入公共回退
系统 SHALL 让物料爬取后端复用同一回退机制:`CRAWL_API_KEY` 缺省回退 `COMMON_API_KEY`;`CRAWL_BASE_URL`(别名 `CRAWL_ENDPOINT`)无公共默认,缺省即视为未配置;`CRAWL_PROVIDER` 字段预留。爬取端点未配置时,爬取能力 MUST 优雅降级为"未配置"而非报错。

#### Scenario: crawl api_key 继承公共值
- **WHEN** 设置 `COMMON_API_KEY`、`CRAWL_BASE_URL`,未设 `CRAWL_API_KEY`
- **THEN** 爬取后端 `api_key` 取 `COMMON_API_KEY`,端点取 `CRAWL_BASE_URL`

#### Scenario: 端点缺省不继承公共 URL
- **WHEN** 设置了 `COMMON_BASE_URL` 但未设 `CRAWL_BASE_URL`/`CRAWL_ENDPOINT`
- **THEN** 爬取端点解析为空,爬取能力报告"未配置"

#### Scenario: CRAWL_ENDPOINT 兼容别名
- **WHEN** 仅设置 `CRAWL_ENDPOINT`
- **THEN** 爬取端点取该值

### Requirement: 适配器选型键
系统 SHALL 以每个后端配置的 `Provider` 字段作为「具体厂商适配器」的选择键,使生图、文生图、生视频能力在多个厂商实现之间**纯通过配置**切换,无需改动调用方代码。当 `Provider` 取值未匹配任何已注册适配器时,系统 SHALL 回退到该能力的默认适配器,以保证既有部署行为不变。

适配器选择键的解析 MUST 复用既有的 provider/base_url/api_key 三层回退(专属 → 公共 → 内置默认),使更换某能力的供应商只需设置该能力的 `<PREFIX>_PROVIDER`/`<PREFIX>_MODEL`(及按需的 base_url/api_key)。

#### Scenario: 按配置选择生图适配器
- **WHEN** 生图后端配置 `IMAGE_PRIMARY_PROVIDER=gemini`
- **THEN** 系统装配 Gemini 图像适配器处理该后端的生图请求
- **AND** 未设置该键的后端仍使用默认(OpenAI 兼容)适配器

#### Scenario: 未知 provider 回退默认
- **WHEN** 某后端配置了一个未注册的 `Provider` 取值
- **THEN** 系统回退到该能力的默认适配器而非启动失败

#### Scenario: 切换供应商仅改配置
- **WHEN** 运维把生视频从默认供应商切到 `veo`(设置 `VIDEO_PROVIDER=veo` 及其 model/base_url/api_key)
- **THEN** 系统经 Veo 适配器产出视频,调用方与工作区管线无需改动

### Requirement: 模型目录与可用性过滤
系统 SHALL 维护一份服务端权威的模型目录,每个条目至少包含:模型 id、显示名、所属场景(逻辑推理 / 图生图 / 文生图 / 图生视频)、厂商标识、厂商图标键。系统 SHALL 依据各场景凭证是否已配置(api_key/base_url 能解析出非空值)将目录过滤为「可用模型」集合,并仅向前端暴露可用集合。任何会话级模型选择的写入 MUST 校验目标模型 id 属于其场景的可用集合;不在集合内的 id SHALL 被拒绝(用户不能选择未配置或不存在的模型)。

#### Scenario: 仅暴露已配置可用模型
- **WHEN** 前端请求模型目录
- **THEN** 系统按场景分组返回**已配置可用**的模型(含显示名、厂商、图标键)
- **AND** 未配置凭证的模型不出现在可用集合中

#### Scenario: 拒绝不可用模型的选择
- **WHEN** 收到将某场景设为一个不在可用集合内的 model id 的请求
- **THEN** 系统拒绝该请求并保持原选择不变

#### Scenario: 厂商图标键
- **WHEN** 目录条目返回给前端
- **THEN** 每个条目携带一个厂商图标键,使前端能以对应厂商品牌图标渲染该模型

### Requirement: grok-4-fast 视觉分析模型目录项
系统模型目录 SHALL 包含 `grok-4-fast` 条目，归入新的**视觉分析**（`analysis`）场景，使用 yunwu 公共网关（`COMMON_BASE_URL`/`COMMON_API_KEY`），适配器选型键为 `vision`。当 yunwu 公共凭证未配置时，该模型对应的视觉分析能力 SHALL 不可用（不崩溃，调用方收到明确的「不可用」信号）。

#### Scenario: 经 yunwu 公共凭证使用 grok-4-fast
- **WHEN** 系统发起视觉分析调用，公共凭证已配置
- **THEN** 请求以 grok-4-fast 发送至 yunwu 公共网关
- **AND** 分析正常执行

#### Scenario: 公共凭证未配置时降级
- **WHEN** 公共凭证（COMMON_BASE_URL/COMMON_API_KEY）未配置
- **THEN** 视觉分析能力不可用，返回明确信号而非崩溃

