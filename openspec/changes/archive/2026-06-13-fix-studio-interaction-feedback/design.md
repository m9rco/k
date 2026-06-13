# Design: 即时占位反馈与思考流式

## Context
5 个 bug 中有 3 个（二次调整无反馈、生视频"遗漏"、占位骨架）共享同一个根因：**Agent 发起的长任务，其 `task_id` 没有可靠地、即时地到达前端**。当前 `tool_result` 事件只在 `summary` 字段塞了工具返回值的截断文本（含 `task_id`），前端 `applyToolResult` 仅用它来收尾卡片，不解析任务 id，因此工作区只能等本轮对话 `done` 后 `refreshWorkspace()` 才发现新任务。这一节先定下事件契约，其余两个 bug（思考、卡片观感）相对独立。

## Goals / Non-Goals
- Goals：发起长任务后 <1s 内工作区出现占位骨架并开始流式进度；思考逐字可见；工具卡片可读；多图参考有明确入口。
- Non-Goals：不改任务持久化模型；不引入新的实时通道；不改生图/裁剪算法；不做模型可选。

## Decision D1：服务在创建任务时即广播 task_created（确定性占位）
**问题**：前端要画占位并订阅某任务的 SSE，必须先拿到 `task_id`。最初尝试从 `tool_result` 事件的 `output.Response` 解析 task_id，但该链路依赖 Eino 工具回调的触发时机与内容，实测不可靠（占位迟迟不出现）。

**方案（已采用）**：不依赖 Agent 回调。让 `generation.Service` / `video.Service` 在 `Start()` 内、任务记录刚 InsertTask 之后，通过一个 `TaskAnnouncer` 钩子（由 main 用 WS Hub 适配）向该会话广播一个 `task_created` 会话事件，`data` 携带 `{task_id, kind}`。前端 WS 收到后立即 `ensureTaskPlaceholder(task_id, kind)`：插入 running 占位并 `subscribeTask`。

- 与对话流、Agent 回调完全解耦——任务一旦创建，占位即出现（亚秒级）。
- `task_created` 在 `task_queued`(SSE) 之前广播，确保前端先有占位再收进度。
- 幂等：前端按 `state.tasks` 的 id 去重，`task_done` 的 refreshWorkspace 不会产生重复卡片。
- `TaskAnnouncer` 为可选注入（nil-safe），测试与无 hub 场景不受影响。

**保留**：`tool_result` 仍可携带 task_id/kind（已实现，作为兜底），但占位不再依赖它。

**备选（已否决）**：仅靠 tool_result 解析 task_id——回调时机/内容不可控，实测占位不出现。

## Decision D2：Anthropic 开启 extended thinking
**问题**：`anthropicBody` 未发送 `thinking`，模型不会返回 `thinking_delta`，前端思考块永远空（被"无思考内容不展示"规则隐藏）。

**方案**：在 `anthropicBody` 中加入 `thinking: {type:"enabled", budget_tokens:N}`，并相应放宽 `max_tokens`（thinking 预算计入输出）。流式解析 `streamAnthropic` 已支持 `thinking_delta`→`reasoning`，无需改。OpenAI 兼容/DeepSeek 路径已通过 `reasoning_content` 工作，保持不变。

**约束**：extended thinking 要求 `max_tokens > budget_tokens`。取 `budget_tokens=1024`、`max_tokens` 提升至至少 `budget+2048`。开启 thinking 时部分供应商对 `temperature` 等参数有限制——若现状未设置 temperature 则无影响；如设置需在开启 thinking 时省略。

**降级**：若代理不接受 `thinking` 字段而报错，沿用既有"流式失败降级为整段补发"路径保证不空屏；思考块按"无内容不展示"自然隐藏，不回归。

## Decision D3：思考块打字机复用回答的节流器
前端已有成熟的回答打字机（`pumpTyper`，按 backlog 自适应速率）。思考块改为同样的"目标缓冲 + 定时吐字"模型，但维护独立的 target/shown 状态，避免与回答正文的打字机互相抢节流器。结论开始（首个回答增量）或工具调用时，先 flush 思考块剩余字符再折叠，保证不丢字。

## Decision D4：多图参考入口
- **可见性**：复用既有多选态。当 `state.selected.size >= 1` 时，在 composer 上方显示参考态条："参考 N 张 · [清除]"，点清除即 `state.selected.clear()`。≥2 张时文案明确为"作为参考改图"。
- **lightbox 二次调整**：`openLightbox` 的"调整"动作，当当前存在多选时，把多选集合作为 `refs` 传入 `sendMessage`（多图参考）；否则维持单图 `asset.id` 行为。即把"放大的这张 + 已选的其余"组合成参考集，第一张为主参考。
- 后端无需改动（`reference_asset_ids` 链路已全）。

## Decision D5：工具卡片人性化
`renderToolCall` 解析 arguments JSON，按 `name`+`intent` 生成「图标 + 短语」标题（映射表，前端硬编码），例如：
- `edit_image`/`change_background` → "🎨 换背景"，副标题取 `background_desc`。
- `image_to_video` → "🎬 生成视频"，副标题取 `motion`。
- `crop_to_sizes` → "✂️ 切尺寸"，副标题取尺寸数量。
原始 JSON 不再作为主体；保留为可选 `title` 悬浮或调试态。未知工具回退到原始 name。

## Decision D6：图生视频显式入口
**问题**：生视频能力存在但用户找不到入口，只能靠在对话里恰好说对话术触发。

**方案**：在放大/二次调整（lightbox）面板的"二次调整"区下方，新增独立的"生成视频"区：一个动作描述输入框 + 「🎬 根据这张图生成视频」按钮。点击时以当前图为源、动作描述组装一句明确意图的消息发给 Agent，命中 `image_to_video` 工具。同时在资产右键菜单加入"生成视频"项，点击打开 lightbox 并聚焦视频输入框。供应商未配置时，Agent 按既有规则回复"暂未配置"，前端不崩溃。

## Risks
