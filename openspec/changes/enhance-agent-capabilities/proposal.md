# Proposal: enhance-agent-capabilities

## Summary
本次变更全面强化 Agent 能力，覆盖 9 个方向：联网搜索与图片检索、多任务流水线、任务后主动反馈、Context 显示 bug、推理流式打字机、聊天框扩展性预留、项目优化审查、意图识别增强，以及移除无效爬虫逻辑。

## Motivation
当前系统存在以下痛点：
1. 无联网搜索能力，爬虫供应商缺失，用户无法搜索参考图
2. Agent 每轮只能执行单个意图，复合指令需多次交互
3. 任务完成后 agent 沉默，缺少后续跟进引导
4. 清理 context 后 UI 仍显示 ~19%，用户困惑
5. 推理内容（thinking）整段突现，打字机体验缺失
6. 聊天框缺乏扩展预留，无法支持后续富交互
7. tools=0 高频出现，意图识别率偏低

## Scope

### 新增能力
- **web-search**：内置联网搜索工具（文字搜索 + 图片搜索），将搜到的图片送入工作区；替代无效爬虫
- **multi-task-pipeline**：单指令触发多工具串联执行（如 搜图→下载→生视频→生 icon）

### 修改能力
- **conversation-orchestration**：意图识别增强（解决 tools=0）、任务后主动反馈、开启推理打字机
- **frontend-experience**：Context bar 显示 bug 修复、聊天框富交互扩展性预留
- **material-crawling**：标记为 REMOVED（无供应商，由 web-search 替代）

## Out of Scope
- 用户可选模型（已有 usermodel 机制）
- 鉴权/多用户隔离
- 移动端适配

## Decisions
1. **联网搜索供应商**：文字搜索用 DuckDuckGo Instant API（零配置、公开合法），图片搜索用 Bing 图片页轻量解析（仅提取图片 URL），均无需 API key
2. **多任务失败策略**：中途任意步骤失败则**整体中断**，已完成的产物保留在工作区，告知用户失败原因
3. **任务后反馈时机**：等整轮（所有工具调用）结束后，统一推送 follow-up capsule
