## ADDED Requirements

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
系统 SHALL 允许每个模型后端通过 `<PREFIX>_PROVIDER` 覆盖供应商类型(如 `openai`、`anthropic`),缺省回退 `COMMON_PROVIDER`,再回退该后端内置默认。生图与生视频配置结构 MUST 承载 `Provider` 字段以与会话模型配置对齐;本要求不改变各 provider 的协议实现。

#### Scenario: 单模型切换供应商类型
- **WHEN** 设置 `CHAT_PRIMARY_PROVIDER=anthropic`
- **THEN** 会话主模型的 `provider` 解析为 `anthropic`,其余后端不受影响

#### Scenario: 公共供应商类型回退
- **WHEN** 设置 `COMMON_PROVIDER=anthropic` 且某后端未设 `<PREFIX>_PROVIDER`
- **THEN** 该后端 `provider` 解析为 `anthropic`

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
