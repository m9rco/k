# multi-task-pipeline Capability Spec

## ADDED Requirements

### Requirement: 单指令串联多工具执行
系统 SHALL 支持 Agent 在一轮对话内串联调用多个工具（如 search_images → edit_image → video），完成用户一句话描述的复合任务。Agent SHALL 按依赖顺序调用工具，前序工具的产物 asset_id 传入后序工具。

#### Scenario: 复合指令串联执行
- **WHEN** 用户发出"帮我找一张王者荣耀的图，生成一个相关视频，同时生成一个 app icon"
- **THEN** Agent 在同一轮内依次调用 search_images（找图）→ video（生视频）→ edit_image/icon（生icon）
- **AND** 每一步工具调用均在前端以独立卡片展示进度
- **AND** 最终工作区包含搜索图、视频、icon 三个产物

#### Scenario: 中途工具失败时整体中断
- **WHEN** 串联任务某步骤失败（如生视频供应商不可用）
- **THEN** 整个多任务流水线 SHALL 立即中断，不继续执行后续步骤
- **AND** Agent 告知已完成的步骤与失败原因
- **AND** 已成功的产物保留在工作区

### Requirement: 工具支持同步等待模式
生图 / 生视频工具 SHALL 支持可选的 `await_result` 参数；当置为 true 时工具同步等待任务完成并在结果中返回产物 `asset_id`，使 Agent 可在一轮内将产物传递给下一个工具。

#### Scenario: await_result 返回 asset_id
- **WHEN** Agent 调用 edit_image 时设置 await_result=true
- **THEN** 工具等待异步生图任务完成后返回产物 asset_id
- **AND** Agent 可在同一轮将该 asset_id 传入后续工具
