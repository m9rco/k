## ADDED Requirements

### Requirement: 多供应商图生图适配器
系统 SHALL 支持图生图能力在多个厂商模型间经配置切换,包括 OpenAI 兼容形态(`gpt-image-2`)与 Gemini 图像模型(`gemini-3-pro-image`、`gemini-2.5-flash-image`、`gemini-3.1-flash-image`、`gemini-3.1-flash-image-preview`)。各厂商以**统一的生图适配器接口**实现,由配置的 `Provider` 键选型;当厂商 API 形态与 OpenAI 兼容时 SHALL 复用既有适配器,否则 SHALL 以手写 HTTP 适配器对接其原生形态(不引入厂商 SDK)。无论选用哪个适配器,主/备供应商失效切换、产物来源记录、颜色适配与参考图复用语义 MUST 保持不变。

#### Scenario: 经配置使用 Gemini 图像模型
- **WHEN** 生图后端配置为某 Gemini 图像模型
- **THEN** 系统经 Gemini 适配器产出图片并回填工作区
- **AND** 产物记录其来源供应商

#### Scenario: 适配器对调用方透明
- **WHEN** 在 `gpt-image-2` 与 Gemini 模型之间切换配置
- **THEN** 换角色/背景/文案、颜色适配、参考图复用等上层能力无需改动即可工作

#### Scenario: 主备可为不同供应商
- **WHEN** 主供应商配置为 Gemini、备用配置为 `gpt-image-2`
- **THEN** 主供应商失败时系统切换到备用供应商重试并记录实际来源
