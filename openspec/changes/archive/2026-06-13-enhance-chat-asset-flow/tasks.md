# Tasks — Enhance Chat & Asset Flow

按"后端协议/编排 → 资产编号 → 前端 → 验证"推进。标 [P] 可并行。

## 1. 思考流式(降级)+ 中文(需求 1、3)
- [x] 1.1 `internal/agent/chatmodel.go` `fallbackStream`:对 `full.ReasoningContent` 按 rune 分片(与正文一致 24-rune/帧)逐帧下发,不再整段一次性发(新增 `chunkRunes` helper)。
- [x] 1.2 `internal/agent/prompt.go` 语言层新增"思考过程(thinking/reasoning)也使用简体中文"。
- [x] 1.3 单测:`TestChunkRunes`、`TestDegradedStreamChunksReasoning`(降级路径 reasoning 多帧且不丢失);`prompt_test` 断言含思考中文指令。

## 2. 资产编号映射注入(需求 2、5)
- [x] 2.1 入站协议扩展:`internal/transport/ws.go` `Inbound` 增加可选 `assetOrder []string`(按显示顺序的 asset_id)。
- [x] 2.2 `cmd/server/main.go` 入站派发:据 `assetOrder` + 选中 refs 构造"图N=asset_id(kind)"编号映射文本,注入用户消息上下文(`buildNumbering` + `agent.BuildAssetNumbering`)。
- [x] 2.3 `internal/agent/prompt.go` 工具使用层:新增编号映射解释 + 多图意图区分规则与示例(根据图X图Y生成新图=ref 无 source;放进图Z=Z 为 source、X/Y 为 ref)。
- [x] 2.4 `internal/agent/tools.go` edit_image 参数描述澄清 source/reference 语义边界。
- [x] 2.5 单测:`prompt_test` 断言多图意图示例与编号解析指令;`TestBuildAssetNumbering` 覆盖编号构造纯函数。

## 3. 对话轮中断(需求 6 后端)
- [x] 3.1 `internal/agent/agent.go`:`Handle` 使用可取消 ctx;`Orchestrator` 维护每 session 的 cancel 句柄 + 处理锁(`sessionTurnLock` 串行)。
- [x] 3.2 新增 `CancelTurn(sessionID)`:取消该 session 进行中的模型流/ReAct 循环;取消时发标记 cancelled 的 turn_end,不取消已提交的异步生图/视频任务。
- [x] 3.3 `internal/transport/ws.go` + `cmd/server/main.go`:新增入站类型 `cancel_turn`,派发到 `CancelTurn`;Handle 改为 goroutine 异步派发(`runTurn`)以保证 readPump 可读取中断,串行锁保证"取消旧轮→起新轮"有序。
- [x] 3.4 单测:`TestSessionTurnLockIsStablePerSession`、`TestCancelTurnFiresRegisteredCancel`。

## 4. 空回复连贯性(需求 7)
- [x] 4.1 `internal/agent/agent.go`:turn_end 增加 `replyEmpty`/`cancelled` 标记(reply 为空且无工具、无 capsule);空 reply 不再发 `message{done:true}` 空帧。
- [x] 4.2 `internal/agent/prompt.go`:强化"面向用户要说的话必须写进正文,思考区不替代回复;不支持的请求也在正文礼貌说明并列能力"。
- [x] 4.3 验证:问候"你好"得到真实正文回复、无空气泡(E2E)。

## 5. 前端:markdown 渲染(需求 4)
- [x] 5.1 `web/src/lib/markdown.tsx` 新增轻量安全 markdown 渲染(粗体/斜体/标题/列表/链接/行内代码;代码块纯文本块;禁 HTML;链接仅 http(s);容忍未闭合标记)。
- [x] 5.2 `web/src/components/chat/message-bubble.tsx`:assistant 文本改用 `<Markdown>` 渲染;`web/src/lib/utils.ts` 删除 `stripMarkdown`。

## 6. 前端:资产编号角标(需求 2)
- [x] 6.1 `web/src/components/workspace/asset-card.tsx`:据显示顺序展示"图N"角标(accent 色),与类型/尺寸角标共存;`workspace-panel.tsx` 传 `index`。
- [x] 6.2 发送消息时附带当前有序 asset_id 列表(`orderedAssetIds` → `assetOrder`),供后端构造编号映射。

## 7. 前端:消息队列 + 插队(需求 6)
- [x] 7.1 `web/src/store/controller.ts`:`thinking===true` 时新输入入队(不立即发);turn_end 后自动出队队首发送(`sendNow`/`sendMessage`/queue 状态)。
- [x] 7.2 队列 UI(`composer.tsx`):展示待发消息;支持手动提前到队首(`promoteQueued`)、移除(`removeQueued`)。
- [x] 7.3 打断当前轮:发送 `cancel_turn` 入站 + 立即发送指定消息(`interruptSend`);队列项与输入框各有打断入口(Zap 图标)。
- [x] 7.4 空回复抑制:turn_end 带 replyEmpty 且无工具/capsule/已产出时,补一条保底说明(`producedRef` 跟踪)。

## 8. 验证与回归
- [x] 8.1 `go build ./...` + `go vet` 通过;`go test ./internal/...` 全绿(新增 chatmodel/prompt/agent/transport 单测)。
- [x] 8.2 前端 `tsc -b` + `npm run build` 通过。
- [x] 8.3 端到端(Playwright):问候不留空气泡且有正文;图N 角标渲染;agent 把"图7"解析为 asset id 并作 source 调 edit_image;会话中入队 + 自动出队;思考为中文。
- [x] 8.4 回归:reasoning 打字机、capsule、turn_start/turn_end、tool 卡片、持久化恢复不退化。
- [x] 8.5 `openspec validate enhance-chat-asset-flow --strict` 通过。
