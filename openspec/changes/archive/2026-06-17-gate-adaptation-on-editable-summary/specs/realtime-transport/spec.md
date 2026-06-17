# realtime-transport Specification

## ADDED Requirements

### Requirement: 宣发摘要确认入站协议
系统 SHALL 通过 WebSocket 接受一类**宣发摘要确认**入站消息，使前端在「可编辑分析面板」的 3 秒确认窗口结束（用户主动确认、编辑后提交、或倒计时到期默认采用）时，把最终摘要文本回传给后端，解除 `adapt_to_platform` 的重绘门控。该消息 SHALL 携带：本次适配的图片集 cache key（与后端 `vision_reports` 键一致）、最终摘要文本、以及一个标记本值是否被用户编辑过的布尔。该协议 SHALL 与既有 `user_message`、`capsule_select`、`cancel_turn` 入站消息并存，并以加法式、向后兼容方式引入：不识别该类型的旧客户端不发送它，后端在门控等待超时后按默认值续接，行为不退化。

#### Scenario: 确认消息解除门控
- **WHEN** 前端在确认窗口结束时发送宣发摘要确认入站消息（携带 cache key 与最终摘要）
- **THEN** 后端将该摘要交付给正在门控等待的 `adapt_to_platform`，适配随即开始 AI 重绘

#### Scenario: 倒计时到期默认采用
- **WHEN** 用户在 3 秒内未编辑，前端倒计时归零
- **THEN** 前端自动发送确认消息，摘要文本为 grok 原始报告、编辑标记为 false
- **AND** 后端据此续接适配，用户无需任何点击

#### Scenario: 编辑标记驱动缓存回写
- **WHEN** 确认消息的编辑标记为 true
- **THEN** 后端以该 cache key 将编辑后的摘要回写 `vision_reports`，覆盖原报告
- **AND** 编辑标记为 false 时不回写（沿用 grok 原版缓存）

#### Scenario: 旧客户端无确认消息
- **WHEN** 不识别该入站类型的客户端从不发送确认消息
- **THEN** 后端在门控的安全超时到期后以 grok 原始报告续接适配，不卡死、不退化
