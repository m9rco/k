# Tasks: 修复工作区交互反馈专项

## 1. 后端：工具完成事件携带任务标识（修复 bug 1/4）
- [x] 1.1 在 `internal/agent/agent.go` 的 `toolCallbackHandler.OnEnd` 中解析 `tool.CallbackOutput.Response`，提取 `task_id`；按工具名映射 `kind`（edit_image→generate，image_to_video→video，crawl_game_assets→crawl）
- [x] 1.2 `EventToolResult` 的 data 增加可选 `task_id`、`kind` 字段；仅长任务工具携带，纯即时工具（crop/list_sizes）不带
- [x] 1.3 为该映射与 JSON 解析补表驱动单测（含无 task_id 的工具不应携带字段）

## 2. 后端：开启 Anthropic 思考（修复 bug 3 后端侧）
- [x] 2.1 在 `internal/agent/chatmodel.go` 的 `anthropicBody` 加入 `thinking: {type:"enabled", budget_tokens:1024}`，并将 `max_tokens` 提升至 `budget+2048` 以上
- [x] 2.2 开启 thinking 时按供应商约束省略不兼容参数（如 temperature，如有）
- [x] 2.3 确认 `streamAnthropic` 的 `thinking_delta`→reasoning 路径无需改动；补一条解析单测覆盖 thinking_delta 帧
- [x] 2.4 验证流式失败降级路径仍生效（思考无内容时不空屏、不展示空块）

## 3. 后端：生视频契约核对（修复 bug 4）
- [x] 3.1 核对 `internal/video/provider.go` 的 R2V 请求/响应契约与实际供应商一致（multipart 字段、响应取数路径）
- [x] 3.2 用已配置供应商做一次真实图生视频联调，确认 task_done 携带 assetId 且产物可预览/下载
- [x] 3.3 仅当确有偏差时调整 provider；保持未配置时 `Configured()=false` 的礼貌降级不回归

## 4. 前端：长任务即时占位与订阅（修复 bug 1/4 核心）
- [x] 4.1 `applyToolResult` 解析 `task_id`/`kind`：成功且带 task_id 时插入占位（作为兜底链路保留）
- [x] 4.2 即时 `subscribeTask(task_id)`，复用既有 `applyTaskEvent` 进度链路
- [x] 4.3 占位幂等：按 id 去重，确保 `task_done` 的 refreshWorkspace 不产生重复卡片
- [x] 4.4 `taskCard` 针对 `kind==="video"` 给出贴合视频的占位文案（如"生成视频中"）

## 4b. 确定性占位：服务在创建任务时广播 task_created（修复 bug 2 根因）
- [x] 4b.1 新增 `transport.EventTaskCreated` 事件类型
- [x] 4b.2 `generation.Service`/`video.Service` 增加可选 `TaskAnnouncer` 钩子，在 `Start()` InsertTask 后即广播 `{task_id, kind}`
- [x] 4b.3 main 用 WS Hub 适配 `taskAnnouncer` 并注入两个服务
- [x] 4b.4 前端 `handleEvent` 处理 `task_created`→`ensureTaskPlaceholder`，占位不再依赖 Agent 回调时序

## 4c. 图生视频显式入口（修复 bug 1）
- [x] 4c.1 lightbox 新增动作描述输入框 + 「🎬 根据这张图生成视频」按钮，以该图为源发起 image_to_video
- [x] 4c.2 资产右键菜单新增「生成视频」项，打开 lightbox 并聚焦视频输入框
- [x] 4c.3 动作描述为空时提示；样式（lb-divider）

## 5. 前端：工具卡片人性化（修复 bug 2）
- [x] 5.1 `renderToolCall` 解析 arguments JSON，按 name+intent 生成「图标+中文短语」主标题与可读副信息（映射表）
- [x] 5.2 移除原始 JSON 作为卡片主体；保留为次要/调试态
- [x] 5.3 `web/static/styles.css` 补充图标态卡片样式
- [x] 5.4 未知工具回退到原始 name，不报错

## 6. 前端：思考打字机（修复 bug 3 前端侧）
- [x] 6.1 `appendReasoning` 改为目标缓冲 + 定时吐字（独立于回答打字机的 target/shown 状态）
- [x] 6.2 `collapseReasoning` 折叠前先补完剩余字符，避免丢字
- [x] 6.3 与回答打字机协调：结论开始/工具调用时先 flush 思考再折叠

## 7. 前端：多图参考入口（修复 bug 5）
- [x] 7.1 多选时在 composer 上方显示「参考 N 张 · 清除」状态条；≥2 张文案明确为"作为参考改图"
- [x] 7.2 清除入口调用 `state.selected.clear()` 并刷新
- [x] 7.3 `openLightbox` 的"调整"动作：存在多选时把多选集合作为 refs 传入 `sendMessage`（多图参考），否则维持单图行为
- [x] 7.4 `web/static/styles.css` 补充参考态标识样式

## 8. 验证与回归
- [x] 8.1 `go build ./...` 与 `go test ./...` 通过（含新增解析/映射单测）
- [x] 8.2 端到端手测：二次调整→工作区秒出占位；生视频→占位+进度+回填；多选→参考态可见并能改图；思考逐字+可折叠；工具卡片可读
- [x] 8.3 `openspec validate fix-studio-interaction-feedback --strict` 通过
