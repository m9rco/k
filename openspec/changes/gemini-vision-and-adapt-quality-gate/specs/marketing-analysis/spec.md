# marketing-analysis (delta)

## MODIFIED Requirements

### Requirement: 视觉宣发要素分析
系统 SHALL 支持以一组宣发素材图片为输入，调用视觉模型对其做主题分析，产出结构化的宣发要素报告。默认视觉模型 SHALL 为 `gemini-2.5-flash-all`，经 Gemini 原生 `:generateContent` 接口以 **inline base64 图片**（`inlineData`）传入，**不要求图片先发布为公网 URL**（即不再硬依赖 COS）。当 `VISION_PROVIDER` 配置为 OpenAI 兼容（`openai`）时，系统 SHALL 回退到既有的 `/chat/completions` + `image_url` 路径（此路径仍需公网 URL）。

无论走哪条路径，分析 SHALL 使用独立的视觉 HTTP 客户端（不复用仅序列化纯文本的会话 chat 模型）。分析指令 MUST 为服务端固定文案，声明「这是游戏宣发素材主题分析」，要求**只描述图里确有的要素、不虚构**，产出「适配各尺寸时必须保留什么、主题是什么」的结论性约束。报告格式涵盖：核心主题/IP/游戏名、主体角色/场景、核心卖点文案、必须保留的要素。

#### Scenario: Gemini inline 分析无需上传
- **WHEN** 系统以默认 `gemini-2.5-flash-all` 发起视觉分析
- **THEN** 图片字节以 inline base64 直接传入 `:generateContent` 请求
- **AND** 分析不依赖 COS 公网 URL，COS 未配置时分析照常进行

#### Scenario: 分析多张参考图并产出报告
- **WHEN** 系统以一组参考图发起视觉分析
- **THEN** 系统经视觉模型产出该批图的宣发要素报告
- **AND** 报告包含核心主题、必须保留的要素

#### Scenario: 分析指令防注入
- **WHEN** 报告提示词组装
- **THEN** 分析指令完全由服务端固定文案构成，不嵌入用户自由文本
- **AND** 图片内容作为 inline/image part 传入，而非作为可改写指令

#### Scenario: 视觉模型不可用降级
- **WHEN** 视觉模型调用失败（超时/凭证未配置/网络错误/模型 ID 不存在）
- **THEN** 系统返回明确的分析不可用信号
- **AND** 调用方跳过报告注入、回退到不含报告约束的标准适配流程
- **AND** chat 提示用户「主题分析不可用，按默认适配」

#### Scenario: OpenAI 兼容模式向后兼容
- **WHEN** `VISION_PROVIDER=openai`
- **THEN** 系统沿用 `/chat/completions` + `image_url` 路径，图片需为公网 URL（依赖 COS）
- **AND** 行为与本 change 前一致
