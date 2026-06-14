# Tasks: enhance-agent-capabilities

## Phase 1 — 基础能力（独立可交付）

- [ ] **T1** 新建 `internal/websearch` 包：`Source` 接口 + `SerpAPISource` 实现 + stub
- [ ] **T2** 在 `internal/agent/tools.go` 注册 `web_search` 工具（文字搜索，返回摘要列表）
- [ ] **T3** 在 `internal/agent/tools.go` 注册 `search_images` 工具（图片搜索+下载注入工作区，复用 crawl 下载逻辑）
- [ ] **T4** 从 `ToolDeps` 和 `Tools()` 白名单中移除 `crawl_game_assets` 工具（代码保留，不注册）
- [ ] **T5** 更新 `SystemPrompt()` 能力白名单：移除物料爬取，新增联网搜索、图片搜索
- [ ] **T6** 为 websearch 包编写单测（stub source）

## Phase 2 — 意图识别增强

- [ ] **T7** 更新 `SystemPrompt()` 工具使用规范第1条：强化"必须先调工具"约束，补充 few-shot 示例
- [ ] **T8** 精简 `clarify_intent` 调用条件描述：非关键参数可合理推断，减少不必要澄清
- [ ] **T9** 为 5 个核心工具补充中文触发短语到 description（edit_image / generate_image_from_text / crop / video / search_images）
- [ ] **T10** 多任务串联支持：为 `edit_image` / `generate_image_from_text` / `video` 工具增加可选 `await_result bool` 参数，当为 true 时同步等待异步任务完成并返回 `asset_id`

## Phase 3 — 推理流式 + Context 显示修复

- [ ] **T11** 后端 `ContextState` 新增 `SystemTokens int` 字段，`State()` 方法填充 `w.system` 的 token 估算
- [ ] **T12** 前端 `context-bar.tsx`：使用 `(estimatedTokens - systemTokens) / budget` 计算对话占比；清理后显示0%
- [ ] **T13** `stream.go` fallback 路径：对 `ReasoningContent` 按32字符分片 emit，模拟打字机效果
- [ ] **T14** 验证流式主路径的 thinking delta 已正确分片（对照 `reasoningFrame` 调用路径）

## Phase 4 — 任务后主动反馈 + 聊天框扩展预留

- [ ] **T15** 定义 follow-up capsule 结构（`FollowUpSuggestion { TaskType, Message, Options []ClarifyOption }`），在 `transport` 层发布
- [ ] **T16** `Orchestrator` 订阅 task done 事件，按任务类型生成预设 follow-up capsule 并推送 WS
- [ ] **T17** 前端聊天消息结构新增 `type` 字段枚举（`text | tool_call | clarify | follow_up`），消息渲染器按 type 派发
- [ ] **T18** 实现 `follow_up` 消息渲染组件（展示建议操作的 clarify-like chip 列表）

## Phase 5 — 验收

- [ ] **T19** E2E 验证：输入"帮我搜索王者荣耀图片并生成视频"，验证 search_images → video 工具串联
- [ ] **T20** 清理 context 后验证 UI 显示 0%
- [ ] **T21** 验证 tools=0 case 减少：重复10次"换背景"意图，统计工具调用率
- [ ] **T22** 更新 `openspec/specs/material-crawling/spec.md` 标注 REMOVED，新增 `web-search` spec
