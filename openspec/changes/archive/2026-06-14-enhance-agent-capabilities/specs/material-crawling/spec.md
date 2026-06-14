# material-crawling Spec Delta

## REMOVED Requirements

### Requirement: 按游戏名爬取素材
该需求由 `web-search` capability 的 `search_images` 工具替代。原 `crawl_game_assets` 工具从 Agent 白名单移除（`internal/crawl` 代码保留，不注册）。图片搜索能力更可靠、可配置性更强，覆盖原爬取场景。

## MODIFIED Requirements

### Requirement: 爬取意图纳入白名单
「物料爬取」意图 SHALL NOT 再纳入 Agent 工具白名单；`crawl_game_assets` 工具从白名单移除，由 `search_images`（图片搜索）/ `web_search`（联网搜索）替代其位置。当用户请求爬取某游戏素材时，Agent SHALL 将其识别为图片搜索意图并分发到 `search_images` 工具。

#### Scenario: 爬取意图改由图片搜索承接
- **WHEN** 用户请求"爬取某游戏的宣传素材"
- **THEN** Agent 识别为图片搜索意图并分发到 `search_images` 工具
- **AND** 行为与原爬取工具等效（图片下载注入工作区、标注来源）

#### Scenario: 爬取工具不再出现在白名单
- **WHEN** 构建 Agent 工具集
- **THEN** `crawl_game_assets` 不在工具白名单中
- **AND** 模型不会分发到该工具
