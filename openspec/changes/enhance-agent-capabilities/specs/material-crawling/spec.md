# material-crawling Spec Delta

## REMOVED Requirements

### Requirement: 按游戏名爬取素材 [REMOVED]
该需求由 `web-search` capability 的 `search_images` 工具替代。原 `crawl_game_assets` 工具从 Agent 白名单移除（`internal/crawl` 代码保留，不注册）。图片搜索能力更可靠、可配置性更强，覆盖原爬取场景。

#### Scenario: 功能迁移
- **WHEN** 用户请求"爬取某游戏的宣传素材"
- **THEN** Agent 识别为图片搜索意图，分发到 search_images 工具
- **AND** 行为与原爬取工具等效（图片下载注入工作区、标注来源）

### Requirement: 爬取意图纳入白名单 [REMOVED]
`crawl_game_assets` 从白名单移除；`search_images` / `web_search` 替代其位置。
