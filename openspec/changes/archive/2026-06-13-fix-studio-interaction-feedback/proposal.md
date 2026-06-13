# Change: 修复工作区二次调整反馈、工具卡片观感、思考流式、生视频与多图参考入口

## Why
近期联调暴露 5 个交互断点，均集中在"用户发起操作 → 是否立刻看到反馈"这条主链路上，导致体验上像"点了没反应"：

1. 在工作区点图二次调整后，对话区能看到交互，但工作区数秒内毫无变化（没有占位骨架、没有 loading），实际是产物任务已在后端排队、前端却要等本轮对话结束才刷新。
2. 工具调用卡片直接把 `{"intent":"change_background","source_asset_id":"asset_xxx",...}` 这类原始 JSON 摊给用户，过于程序化。
3. 思考过程没有逐字涌现：后端未向 Anthropic 请求思考内容，前端思考块也未走打字机，用户只能干等最终回答。
4. 图生视频"跑不通"：后端工具与异步任务其实已存在，但产物任务复用了与第 1 项相同的断点（前端不会即时订阅新任务），且对话结束才回填，表现为"遗漏"。
5. 多图作为参考改另一张图找不到入口：后端 `reference_asset_ids` 链路完整，但前端从未告诉用户"多选即参考"，放大/二次调整面板也只能传单图。

这些都属于恢复既定设计意图的修复，但牵涉前端交互、对话编排、生视频三块能力的可见行为，故走正式 change 提案。

## What Changes
- **即时占位反馈（核心）**：工具调用发起长任务（生图/二次调整/生视频）时，前端在收到工具事件后立即在工作区"进行中"分组插入占位骨架卡片并订阅其 SSE 进度，不再等本轮对话 `done`。
  - 后端 `tool_result` 事件结构化携带 `task_id` / `kind` / `intent`，供前端精确定位并即时订阅。
- **工具卡片人性化**：工具调用卡片以图标 + 简短中文意图短语呈现（如"🎨 换背景：淡紫色"），原始 JSON 参数收敛为可选的次要信息，不再作为主展示。
- **思考逐字流式**：
  - 后端对 Anthropic 会话模型开启 `thinking`（extended thinking）请求，使思考增量经既有 `reasoning` 事件实时下发；模型无思考内容时不展示空块（保持现状）。
  - 前端思考块改为打字机式逐字渲染，结论开始或工具调用时自动折叠为可展开态。
- **图生视频打通**：复用上面的即时占位链路，使"让图动起来"产生的视频任务也能即时出现占位、流式进度、完成回填；校验 R2V provider 请求/响应契约，未配置时礼貌降级（保持现状）。
- **多图参考入口可见**：
  - 工作区多选时显式提示"已选 N 张作为参考"，并在 composer 区域给出可见的参考态标识与一键清除。
  - 放大/二次调整（lightbox）面板支持把当前已选的多张资产一并作为参考发起改图，而非只传单图。

## Impact
- Affected specs:
  - `frontend-experience`（即时占位反馈、工具卡片观感、思考打字机、多图参考入口）
  - `conversation-orchestration`（`tool_result` 结构化携带任务标识、思考增量请求开启）
  - `video-generation`（生视频产物即时占位与流式反馈的验收补强）
- Affected code:
  - `web/static/app.js`（`applyToolResult`/`renderToolCall`/`appendReasoning`/`openLightbox`/多选提示/即时订阅）
  - `web/static/styles.css`（工具卡片图标态、参考态标识）
  - `internal/agent/agent.go`（`tool_result` 事件补充 `task_id`/`kind`/`intent`）
  - `internal/agent/chatmodel.go`（Anthropic `thinking` 请求体）
  - `internal/agent/tools.go`（工具结果回传结构，必要时让 callback 拿到 task 元数据）
  - `internal/video/provider.go`（R2V 契约核对，仅在确有偏差时调整）
