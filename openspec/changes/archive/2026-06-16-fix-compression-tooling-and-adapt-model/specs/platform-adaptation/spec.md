# platform-adaptation Spec Delta

## REMOVED Requirements

### Requirement: 会话级适配去重

**移除理由**：该去重（以 `(源图, 尺寸)` 命中已持久化旧产物即静默重用、不重新生成）与用户的
真实预期冲突——用户对已适配过的图再次发起同尺寸适配，期望得到一份**新产物**，而非被静默重用。
实践中它表现为"工作区什么也没出"，是空工作区故障的直接根因。移除后，每次适配请求都真正发起
裁剪/AI 重绘；轮内重复调用防护（`adapt_to_platform` 工具的 `dedup.firstSeen`）保留，仍可挡住
模型在同一轮并行发出的重复调用。

## ADDED Requirements

### Requirement: 平台适配 AI 重绘的请求级模型路由
系统 SHALL 在执行 `adapt_to_platform` 的 AI 重绘路径时，固定使用 `gemini-3-pro-image` 作为本次重绘的图生图模型，作用域 SHALL 仅限本次适配请求。会话已选的 image 场景模型 SHALL NOT 受影响；其他图生图操作（如 `edit_image`）SHALL NOT 受影响；模型设置面板的会话 image 选择 SHALL NOT 改变。当 `gemini-3-pro-image` 在 image 场景下不可用（其凭证未配置）时，系统 SHALL 优雅回退到会话当前 image 模型或服务默认 provider，不使适配失败。

#### Scenario: AI 重绘使用 Gemini 3 Pro Image
- **WHEN** 用户发起 AI 平台适配且目标尺寸需要 AI 重绘（比例或方向差异超容差）
- **THEN** 重绘任务使用 `gemini-3-pro-image` 生图，与会话 image 场景的用户选择无关
- **AND** 产物回填工作区

#### Scenario: 普通图生图不受适配路由影响
- **WHEN** 用户发起非适配的图生图操作（换角色/换背景/换文案等 `edit_image` 路径）
- **THEN** 使用会话已选的 image 模型（或服务默认），不使用 Gemini 适配路由

#### Scenario: Gemini 不可用时优雅回退
- **WHEN** `gemini-3-pro-image` 在当前部署的 image 场景下凭证未配置（不可用）
- **THEN** 适配 AI 重绘回退到会话 image override 或服务默认 provider 继续执行
- **AND** 适配任务正常完成，不向用户报错"模型不可用"
