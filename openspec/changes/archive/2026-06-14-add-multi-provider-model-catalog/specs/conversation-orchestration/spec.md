## RENAMED Requirements
- FROM: `### Requirement: 模型服务端硬编码`
- TO: `### Requirement: 模型服务端配置驱动`

## MODIFIED Requirements

### Requirement: 模型服务端配置驱动
系统 SHALL 在服务端通过配置(环境变量)决定会话理解模型,用户不可选择或切换。会话理解模型 SHALL 支持经配置在多个供应商/模型间切换(如 `claude-sonnet-4-6`、`claude-haiku-4-5-20251001` 走 Anthropic 协议分支;`gpt-5.4`、`doubao-seed-2-0-mini-260428`、DeepSeek 走 OpenAI 兼容分支),由配置的 `provider`/`model`/`base_url`/`api_key` 解析,默认值保持向后兼容。系统 SHALL 在 OpenAI 兼容分支上兼容不同供应商的思考(reasoning)字段命名差异;当某模型不返回思考内容时,系统 SHALL 正常产出回答而不报错。无论选用哪个受支持的模型,用户 SHALL NOT 能在请求中指定或改写所用模型。

#### Scenario: 用户不可切换模型
- **WHEN** 用户尝试指定使用某个模型
- **THEN** 系统忽略该指定并使用服务端配置的模型

#### Scenario: 经配置切换会话模型
- **WHEN** 运维将主会话模型配置为另一受支持模型(如 `CHAT_PRIMARY_MODEL=gpt-5.4` 并设对应 provider)
- **THEN** 系统经对应协议分支调用该模型完成会话理解
- **AND** 流式回复与思考增量、工具调用循环行为保持一致

#### Scenario: 供应商思考字段差异兼容
- **WHEN** 选用的 OpenAI 兼容模型以不同字段名返回思考内容,或不返回思考内容
- **THEN** 系统正确解析其思考增量(若有)并下发,否则仅下发回答正文而不报错
