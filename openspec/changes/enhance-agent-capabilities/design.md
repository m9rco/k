# Design: enhance-agent-capabilities

## D1 联网搜索架构

### 搜索供应商
**无需 API key，两种轻量方案：**
- **文字搜索**：DuckDuckGo Instant Answer API —— `GET https://api.duckduckgo.com/?q={query}&format=json&no_html=1`，公开合法，无鉴权，返回即时摘要与相关结果
- **图片搜索**：Bing 图片页轻量爬取 —— `GET https://www.bing.com/images/search?q={query}`，携带浏览器 User-Agent，解析页面提取图片 URL，仅限 URL 提取不做再分发
- 无任何配置项，开箱即用；网络不可达时降级提示
- 若后续有 Tavily API key，可通过 `Source` 接口热插拔替换

### 搜索工具接口设计
参考 crawl 包的 `Source` 接口模式，新建 `internal/websearch` 包：

```
Source interface {
    // Always available — no credentials required.
    SearchWeb(ctx, query, limit) ([]WebResult, error)
    SearchImages(ctx, query, limit) ([]ImageResult, error)
}
```

实现：`DDGSource`（文字搜索，DuckDuckGo Instant API）+ `BingImageSource`（图片搜索，Bing 页面解析）/ `StubSource`（测试）

Agent 工具层新增两个工具（tools.go）：
- `web_search`：文字搜索，返回摘要 + URL 列表
- `search_images`：图片搜索，下载并注入工作区（复用 crawl.Service 的下载逻辑）

**替代爬虫**：`material-crawling` spec 标记 REMOVED，`crawl` 包保留但不再注册到工具白名单；`web_search` + `search_images` 覆盖其功能并更可靠。

## D2 多任务流水线

### 现状分析
Eino ReAct 本身支持多轮 tool call（一轮内可调用多个工具），但当前 SystemPrompt 和意图设计隐含"一轮一意图"的约束。实际上只需：
1. 更新 SystemPrompt，明确告知 agent 可在一轮内串联多个工具
2. 更新能力白名单描述，加入"复合指令"示例
3. 无需架构改动，Eino ReAct loop 自然支持

**风险**：多工具串联时，前序工具产物（asset_id）需传入后序工具。Agent 需从工具返回的 task_id 中等待结果或读取工作区，当前工具均为异步（返回 task_id 不等待）。

**方案**：为生图/生视频工具增加可选 `await_result` 参数，当 agent 判断需要链式调用时设为 true，工具同步等待任务完成并返回产物 asset_id，供下一工具使用。

## D3 意图识别增强（tools=0 根因）

根因分析：
- SystemPrompt 的"澄清规范"过于保守，agent 倾向于直接文字回复而非调用工具
- 工具描述（description）与用户自然语言距离较远
- 缺少 few-shot 示例引导

方案：
1. 在 SystemPrompt 的"工具使用规范"第1条强化：**当意图匹配且信息充足时，必须先调用工具再说话，禁止只文字确认**
2. 为每个工具 description 补充中文触发短语示例
3. 减少 clarify_intent 的调用门槛（只有关键参数缺失才澄清，非关键参数可合理推断）

## D4 推理流式打字机

现状：stream.go 已支持 `reasoning_content` delta 推送（`reasoningFrame`），但：
- 降级路径（fallback 读完整 response）将 thinking 整段一次性写入，未分片
- 前端的 thinking block 渲染在降级路径下整段突现

方案：在 `fallback` 路径中，对 `ReasoningContent` 字符串按固定 chunk size（如32字节）分片 + 小延迟 emit，模拟流式效果。无需改变流式主路径。

## D5 Context Bar 19% Bug 分析

**根因**：`ResetContext()` 后 window 重建，但 system prompt 本身约占 ~1500 tokens（8000 budget 的19%）。清理后 UI 仍显示19% 是**预期行为**，但用户不理解。

**修复**：Context bar 改为显示"对话消息"占比（`EstimatedTokens - systemPromptTokens`），并将 system prompt 基线成本从 API 响应中分离。后端 `ContextState` 新增 `SystemTokens int` 字段，前端用 `(estimatedTokens - systemTokens) / budget` 计算实际对话占比，清理后应为0%。

## D6 聊天框扩展性预留

参考 CodeBuddy/Cursor 的富交互模式，聊天消息结构预留：
- `type` 字段区分：`text | tool_call | clarify | task_progress | markdown | code | image_inline`
- 消息渲染器注册表（MessageRenderer map），按 type 派发组件
- 当前只实现 text/tool_call/clarify，其他 type 只需注册即可扩展

## D7 任务后主动反馈

工具完成后（生图/生视频 task done 事件），agent 主动发一条跟进消息。
实现：在 `transport.TaskBroker` 的 done 事件中，可选回调注入 follow-up prompt；Orchestrator 判断任务类型后生成引导语（如"图片已生成，要继续生成视频吗？"），以 `assistant` 消息推送到前端，不走完整 ReAct 循环（避免额外 token 消耗），直接推一条预设的 follow-up capsule。
