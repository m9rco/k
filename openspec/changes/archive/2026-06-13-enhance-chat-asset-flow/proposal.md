# Enhance Chat & Asset Flow

## Why

上一轮 harness 优化已上线后,实际使用中暴露出 7 个体验/正确性问题(基于对 `internal/agent/`、`internal/transport/`、`web/src/` 的审计,均附代码证据):

1. **思考降级时不流式**:reasoning 在正常 SSE 路径已逐 chunk 流式(`stream.go:106-108`、`stream.go:185-188`),但流式失败的降级路径 `fallbackStream`(`chatmodel.go:155-159`)把整段 reasoning **一次性**发出,失去打字机效果。
2. **资产无编号**:`AssetRecord`/`AssetView` 无序号字段(`store.go:159-171`、`workspace.go:52-62`),卡片只显示类型+分辨率(`asset-card.tsx:100-107`)。用户无法用"图2/图3"指代具体图,LLM 也只能收到不可读的 `asset_id`(`prompt.go:54`)。
3. **思考过程是英文**:system prompt 的语言约束只覆盖"最终回复"(`prompt.go` 语言层),未约束 reasoning/thinking 语言,Anthropic extended thinking 默认倾向英文,中英混杂。
4. **markdown 被剥离而非渲染**:当前 `MessageBubble` 对 assistant 文本做 `stripMarkdown`(`message-bubble.tsx:18`、`utils.ts:13-30`),把 markdown 标记**删掉**。但模型受控失败偶尔返回 markdown 时,用户看到的是被剥得不完整的纯文本,而非渲染后的富文本——需求要求**渲染**对应 markdown,而不是返回/暴露源码。
5. **多图意图理解弱**:`edit_image` 有 `source_asset_id`(被编辑对象)与 `reference_asset_ids`(参照物)两类语义(`tools.go:51-66`),但 prompt 未教 LLM 用编号区分"根据图2图3生成新图(纯参照)"vs"把图2图3放进图4(图4 是被编辑对象)"这类组合意图。
6. **无消息队列/插队**:会话进行中再次 `sendMessage` 直接并发发送(`controller.ts:350-368`,无 thinking 拦截),后端 `Handle` 串行但无队列语义,用户无法排队/调整顺序/打断,与 Cursor 等 agent 不一致。
7. **思考完不回消息**:当模型只产出 reasoning、无最终 content 也无工具调用时(`agent.go` turn done `replyLen=0 toolCalls=0`),后端仍发一条空 `message{done:true}`,前端因此渲染出一个**空白 assistant 气泡**(`controller.ts` onAssistantDelta 对空 text 仍建气泡),表现为"思考完了却没有回复",对话出现断裂。用户给的例子正是模型把"该回复用户的话"写进了 reasoning 而没写进 content。

## What Changes

- **思考全程流式 + 中文**:`fallbackStream` 对 reasoning 也做 re-chunk(与正文一致的逐片下发);在 system prompt 显式要求**思考过程也用简体中文**(对 Anthropic extended thinking 同样生效)。
- **工作区资产编号**:按**显示顺序**为资产分配编号(图1、图2…),编号随移除/重排实时更新。前端卡片显示编号角标;后端把"图N → asset_id"的映射作为上下文提供给 LLM,使 LLM 既能理解用户口中的"图2图3",也能在反问/说明里用"图N"称呼。
- **多图意图理解**:扩充 prompt 与 edit_image 描述,教 LLM 用编号区分"参照物(reference_asset_ids)"与"被编辑目标(source_asset_id)"。覆盖两类组合意图:(a) 根据图2图3生成新图=两者都作参照,无 source;(b) 把图2图3放进图4=图2图3 作参照、图4 作 source。
- **markdown 渲染替代剥离**:前端引入轻量安全 markdown 渲染(粗体/斜体/标题/列表/链接/行内代码,代码块按纯文本块展示,禁原始 HTML 防注入),替换 `stripMarkdown`。prompt 仍倡导简洁纯文本,但模型一旦输出 markdown,UI **渲染**而不是暴露源码或残留标记。
- **消息队列 + 插队**:前端在会话进行中把新输入放入**待发队列**,展示队列;支持**手动提前**(把某条移到队首)与**打断当前轮**(取消进行中的 agent turn 并立即处理)。后端新增 turn 中断(取消正在进行的 `Handle`/模型流)与按序消费队列的协议。
- **空回复连贯性**:turn 结束若 `reply` 为空且无工具/无 capsule,前端**不渲染空气泡**;并在 prompt 强化"面向用户要说的话必须写进正式回复(content),不要只写在思考里",从根因减少空回复;若仍为空,给出一条保底说明或直接收束,保证对话不断裂。

本提案**不改动**:模型硬编码策略、生图/裁剪/视频 provider 逻辑、既有任务 SSE 进度协议、capsule 澄清机制。

## Capabilities & Specs

- `conversation-orchestration`(MODIFIED):流式输出(降级也流式 reasoning + 空回复保底)、输出格式(markdown 渲染语义反转)、分层 prompt(思考中文 + 资产编号沟通 + 多图意图 + content 优先);(ADDED)turn 中断。
- `asset-workspace`(ADDED/MODIFIED):资产显示编号;编号用于 LLM 沟通与多图意图。
- `frontend-experience`(MODIFIED):思考流式(空回复不留空气泡)、消息渲染(markdown);(ADDED)资产编号角标、消息队列与插队 UI。
- `realtime-transport`(ADDED):消息队列与插队/中断的 WS 协议。

## Impact

- 后端:`internal/agent/`(prompt、tools、agent orchestrator 的 turn 取消、chatmodel fallback)、`internal/transport/`(队列/中断事件)、`internal/workspace/`(AssetView 编号)、`cmd/server/main.go`(入站派发/取消)。
- 前端:`web/src/store/controller.ts`(队列、插队、空回复抑制、reasoning)、chat 组件(markdown 渲染、队列 UI)、workspace 组件(编号角标)、`web/src/lib/utils.ts`(移除 stripMarkdown,新增渲染)。
- 数据:编号为派生展示属性,不入库(按显示顺序计算),无 schema 变更。
- 风险:turn 中断需与既有 generation/video 的取消协调(已存在 task Cancel,但 agent turn 本身的取消是新增);markdown 渲染需严格防注入。
