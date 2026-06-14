# web-search Specification

## Purpose
TBD - created by archiving change enhance-agent-capabilities. Update Purpose after archive.
## Requirements
### Requirement: 联网文字搜索
系统 SHALL 提供 `web_search` 工具，支持 Agent 在会话中发起联网文字搜索，返回摘要与来源 URL 列表供 Agent 用于后续回复或决策。搜索能力 SHALL 通过可插拔 `Source` 接口实现，默认实现模拟浏览器 HTTP 请求抓取 Bing/百度搜索页，无需 API key；网络不可达时礼貌降级。

#### Scenario: 命中搜索意图
- **WHEN** 用户请求"帮我搜索 XXX 的相关信息"
- **THEN** Agent 调用 `web_search` 工具并将搜索摘要融入回复
- **AND** 工具调用过程以事件形式可见于前端

#### Scenario: 搜索源未配置时降级
- **WHEN** 搜索供应商未配置（SEARCH_API_KEY 为空）
- **THEN** 工具返回错误，Agent 礼貌告知用户联网搜索暂未配置

### Requirement: 图片搜索并注入工作区
系统 SHALL 提供 `search_images` 工具，支持 Agent 按关键词搜索图片，将找到的图片下载并作为 `kind=searched` 资产注入工作区，供后续生图/生视频工具引用。

#### Scenario: 搜索图片并注入工作区
- **WHEN** 用户请求"帮我找一张《王者荣耀》的图"
- **THEN** Agent 调用 `search_images`，图片下载完成后以资产卡片出现在工作区
- **AND** 工作区卡片标注搜索来源

#### Scenario: 搜到图片后可直接用于生成任务
- **WHEN** `search_images` 完成且产物 asset_id 可用
- **THEN** Agent 可在同一轮或下一轮将该 asset_id 传入 `edit_image` / `video` 等工具继续处理

