## ADDED Requirements

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
