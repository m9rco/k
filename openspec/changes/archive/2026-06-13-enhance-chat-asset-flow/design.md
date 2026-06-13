# Design — Enhance Chat & Asset Flow

## Context

审计确认的现状(代码证据见 proposal.md):
- reasoning 正常路径已逐 chunk 流式;只有 `fallbackStream`(`chatmodel.go:147-176`)一次性发 reasoning。
- 资产标识全程用 `asset_id`(store/workspace/prompt/tools 一致),无序号。前端多选资产时 `cmd/server/main.go` 把 `[reference assets: id…]` 前缀注入用户文本。
- `MessageBubble` 用 `stripMarkdown` 删除标记(`utils.ts:13-30`),无 markdown 渲染库。
- `edit_image` 已有 `source_asset_id`(被编辑)/`reference_asset_ids`(参照,首个为主)双语义;`primaryAndExtras()`(`generation/service.go:102-117`)解析。
- `sendMessage`(`controller.ts:350-368`)无队列、无 thinking 拦截;后端 `Handle` 同步串行但无队列/取消。
- 空回复时后端仍发 `message{text:"",done:true}` + turn_end,前端建空气泡。
- 已存在的取消能力:generation/video service 各有 `Cancel`(上一个 change 加的),但 **agent turn 本身**(模型流 + ReAct 循环)没有取消入口。

7 个需求决策(用户已定:按显示顺序编号 / 插队两者都要 / 轻量安全 markdown)。

## Goals / Non-Goals

**Goals**
- 思考全程流式(含降级路径)且为简体中文。
- 资产有稳定的"按显示顺序"编号,既给用户看也给 LLM 用来沟通"图N"。
- LLM 能据编号区分参照物 vs 被编辑目标,正确处理多图组合意图。
- markdown 被渲染(轻量安全)而非剥离或暴露源码。
- 会话进行中支持排队、手动提前、打断当前轮。
- 杜绝"思考完不回消息"的空气泡断裂。

**Non-Goals**
- 不引入完整 markdown(表格/围栏高亮/HTML)。
- 不改模型硬编码、provider 逻辑、capsule 机制。
- 不持久化编号(纯派生展示)。
- 不做多 session 并发编排。

## Decision 1 — 资产显示编号(按显示顺序)

**问题**:用户用"图2图3"指代,LLM 只拿到 asset_id;反过来 LLM 也无法用"图N"跟用户说。

**方案**:
- 编号是**派生属性**:按工作区当前显示顺序(已有 order:`controller.ts` 维护的 display order)从 1 递增。移除/重排后实时重算,不入库。
- 前端:资产卡片左上角加"图N"角标(与现有 kind/分辨率角标并列)。
- 后端→LLM:每轮把"编号映射"作为上下文注入。当用户消息带选中资产时,`cmd/server/main.go` 现在注入 `[reference assets: id…]`;改为同时注入**人类可读的编号表**,例如 `[工作区: 图1=asset_a(上传), 图2=asset_b(生成)…] [选中: 图2, 图3]`,使 LLM 既懂"图2图3"又能在回复里用"图N"。
- LLM 输出仍用 asset_id 调工具(工具 schema 不变),但**说明性文本**里用"图N"称呼,前端无需翻译。

**权衡**:编号随重排变化,可能与历史对话里提过的"图2"语义漂移;但符合用户直觉(所见即所指),且每轮重新注入当前映射,LLM 始终基于最新编号。稳定永久编号方案被否(与"第几张"的视觉位置不一致)。

**编号映射的来源**:前端是 display order 的权威。两种实现:(a) 前端在发消息时附带当前有序 asset_id 列表,后端据此生成编号表;(b) 后端按 created_at 排序自行编号。选 (a):前端 order 包含用户拖拽重排,后端无此信息。故入站消息扩展一个可选的 `assetOrder`(有序 asset_id 列表),后端据此构造编号表注入 prompt。

## Decision 2 — 多图意图理解

**问题**:"根据图2图3生成新图"(纯参照,无 source)vs"把图2图3放进图4"(图4 是 source,图2图3 是参照)需要 LLM 正确映射到 `reference_asset_ids`/`source_asset_id`。

**方案**:在 prompt 的工具使用层增加明确规则与示例:
- "根据图X图Y…生成/创作新图" → 图X图Y 作为 `reference_asset_ids`(参照风格/主体),**不设** `source_asset_id`。
- "把图X图Y…放进/融合到图Z" / "在图Z基础上…" → 图Z 作为 `source_asset_id`(被编辑底图),图X图Y 作为 `reference_asset_ids`。
- 编号→asset_id 由注入的编号表解析。
- edit_image 的参数描述同步澄清两者语义边界。

`primaryAndExtras()` 行为不变(已支持 source 缺省时用 refs[0]),只是 LLM 现在能更准地填这两个字段。

## Decision 3 — markdown 渲染替代剥离

**问题**:`stripMarkdown` 删标记导致残缺纯文本;需求要"渲染"。

**方案**:
- 前端新增轻量安全渲染器(自实现或极小依赖):支持 **粗体/斜体/标题/无序+有序列表/链接/行内代码**;**代码块**渲染为纯文本预格式块(展示内容,不当源码暴露);**禁止原始 HTML**(防 XSS/注入)。
- 替换 `MessageBubble` 里的 `stripMarkdown` 调用为渲染。流式中途的半截 markdown 标记需容错(未闭合的 `**` 不应破坏渲染)——渲染器对不完整标记按纯文本处理。
- prompt 仍倡导简洁纯文本(选择类走 capsule),渲染是**兜底**:模型违规输出 markdown 时 UI 正确呈现而非暴露源码。

**权衡**:引入 markdown 解析有 XSS 面;通过"禁 HTML + 仅白名单语法"控制。不用重型库(react-markdown+rehype)以控体积与攻击面,优先轻量实现或经裁剪的安全库。

## Decision 4 — 思考流式(降级)+ 中文

**reasoning 降级流式**:`fallbackStream` 当前一次性发整段 reasoning。改为对 reasoning 也按 rune chunk(与正文一致的 24-rune/帧)re-chunk 下发,保持打字机。

**思考中文**:在 system prompt 语言层补一句"思考过程(thinking/reasoning)也使用简体中文"。对 Anthropic extended thinking,thinking 语言受 system prompt 指令影响,补此约束即可;对 OpenAI/deepseek 分支同样生效。这是 prompt 层改动,无协议变更。

## Decision 5 — 消息队列 + 插队(两者都要)

**问题**:会话进行中的新输入需排队、可提前、可打断。

**前端(队列权威在前端)**:
- 维护一个待发队列。`thinking===true` 时,新 `sendMessage` 不直接发,而是入队并在 UI 展示(可见的排队条目)。
- 当前轮 `turn_end` 到达后,自动出队队首并发送。
- **手动提前**:用户可把队列中某条移到队首。
- **打断当前轮**:用户触发"打断",前端发一个**取消信号**给后端,并立即发送指定消息(或队首)。

**后端(turn 取消)**:
- `Handle` 接收一个可取消的 context;新增入站消息类型 `cancel_turn`(或 interrupt),后端取消该 session 正在进行的 `Handle`(取消模型流 + ReAct 循环)。
- 取消时:停止下发该轮增量,发一个 turn_end(标记 cancelled),不污染 window(已产出的部分仍按现有逻辑落 window/持久化,保证连贯;或标记中断——见下)。
- 取消与已存在的 generation/video task Cancel 解耦:打断 agent turn 不取消已提交的异步生图任务(那是独立的工作区任务,有自己的取消入口),只中止对话轮的模型推理。

**串行保证**:同一 session 的 `Handle` 必须串行(避免 window 竞争)。后端对每 session 加处理锁/单 worker,队列在前端,后端只需保证"取消旧 turn → 起新 turn"的原子切换。

**权衡**:队列放前端实现简单、所见即所得;后端只需提供取消入口与串行保证。代价:刷新页面丢失未发队列(可接受,未发即未发生)。turn 取消需要 ReAct 循环响应 ctx 取消——Eino 的 Stream 支持 ctx;模型流读取处检查 ctx.Done()。

## Decision 6 — 空回复连贯性

**根因**:模型把"该对用户说的话"写进 reasoning,content 为空(用户给的问候例子)。

**三层处理**:
1. **prompt 根治**:强化"面向用户要说的话必须放进正式回复正文,思考区不替代回复;遇到不支持的请求,也要在正文礼貌说明并列能力"。
2. **前端抑制空气泡**:`onAssistantDelta`/`turn_end` 收到空 reply 且无工具/无 capsule 时,不创建/移除空 assistant 气泡。
3. **保底**:若 turn 结束确实无任何 content、工具、capsule,前端展示一条保底提示(或后端补一条标准"能力说明")避免完全无反馈。优先 1+2,3 作为最后兜底。

**turn_end 已携带** toolUsed/hasCapsule(上个 change),前端据此判断是否该有正文;扩展 replyEmpty 判断即可。

## Risks / Open Questions

- markdown 渲染的 XSS:严格禁 HTML、白名单语法、链接 `href` 仅允许 http(s)。
- turn 取消的边界:模型流已发出的 reasoning/部分 content 如何记入 window——倾向"记入已产出部分 + 标记中断",保证下一轮上下文连贯;需在 spec 明确。
- 编号漂移:历史对话里的"图2"与当前不一定同一资产;每轮注入最新映射缓解,但跨轮指代仍可能歧义(记为已知限制)。
- 前端队列与后端取消的时序:打断信号与新消息需保证后端"先取消旧轮再起新轮",避免两轮交叠写 window。

## Migration

- 无 schema 变更(编号派生)。
- 新增入站消息类型(cancel_turn、assetOrder 字段)为加法;旧客户端不受影响。
- 前端 `stripMarkdown` 移除、渲染替换为纯前端改动。
