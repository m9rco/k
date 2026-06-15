## Context
弱会话模型（运行时实际是 openai 分支的 deepseek 系，见记忆 [[chat-model-runtime-config]]）会「只用文字假装执行」。系统已有：
- `looksLikeFakeExecAck`（fakeack.go）：进度动词 + 产物/工作区引用双信号检测假 ack。
- 自纠正重试循环（agent.go）：`maxAttempts=2`，检测到假 ack 则追加 `fakeAckCorrection` 严厉纠正后重跑一次。
- `streamOnce`：每个 attempt 内把 `chunk.Content` 以 `done:false` 实时 `emit(EventMessage)`，驱动打字机 UI。
- 前端 `onAssistantDelta`（controller.ts）：`typer.current.target += text` 累积，仅在 `done:true` 把 `typer.current` 清零。

**应用阶段发现的真实根因（比初版提案更深）**：`streamOnce` 原先靠数 `ra.Stream()` 输出流里的 `chunk.ToolCalls` 来统计本轮工具调用数。但 eino react agent 的图结构（`eino@v0.9.6/flow/agent/react/react.go`）决定了 END 节点的输出流要么是「最后一轮模型的纯文本」，要么是「ReturnDirectly 工具返回的 tool 消息」——**两条路径都不含模型那条带 `tool_calls` 的 assistant chunk**。而本项目所有生成类工具（edit_image / generate_icon / generate_image_from_text / image_to_video / search_images）都在 `ToolReturnDirectly` 集合里。结果：`turnCalls` 几乎**恒为 0**。这导致：
1. 生成类工具真执行后，工具返回的友好提示（"正在生成…产物会出现在左侧工作区"）命中 `looksLikeFakeExecAck`；
2. `turnCalls==0` 使 `shouldRetryFakeAck` 误判为「假执行」→ 触发重试 → **真的再调一次工具** → 产生重复产物（用户实测：换角色一次得到两张图）；
3. 最终 `turnCalls` 仍 0 → 走 honestFail 兜底 → 显示「没能执行」文案，与"已出现两张图"自相矛盾。

约束：spec「流式对话输出」明确要求**真·流式**、且「不提供让该调用退回无差别静态等待的非流式默认路径」。因此**不能**改成「缓冲全部增量到 attempt 结束才下发」——那等于退回非流式。

## Goals / Non-Goals
- Goals：
  1. **工具调用计数反映真实执行**，杜绝 ReturnDirectly 工具被误判未执行而重复触发。
  2. 重试时前端不再出现重复确认文字。
  3. 重试耗尽仍未真正调工具时，给用户**诚实**反馈而非假确认。
  4. 用户反馈「没生成 X」时，本轮能确定性地重新触发上一轮意图。
- Non-Goals：
  - 不改 `looksLikeFakeExecAck` 的检测阈值（保持保守、近零误报）。
  - 不引入新的会话模型往返来「判断用户是否在抱怨」（用确定性关键词，零额外 LLM 成本）。
  - 不改 `maxAttempts`（仍为 2，单次纠正）。
  - 不放弃 `ToolReturnDirectly`（它防止小模型把 queued 当未完成而循环重调，仍需保留）。

## Decisions

### D0（根因修复）：工具调用计数改用回调观测真实执行，取代 stream-chunk 计数
- **What**：新增 per-turn `toolExecTracker`，在工具节点回调 `OnStart` 里记录每次真实执行的 `{id, name, args}`（id 取自 `compose.GetToolCallID(ctx)`，name/args 取自 `tool.CallbackInput`）。`streamOnce` 不再从 `chunk.ToolCalls` 派生工具列表、也不再从流里 emit `tool_call` 事件（改由 `OnStart` emit，shape 不变）；其签名简化为只返回 `reply`。重试判定改用「本次 attempt 的真实执行增量」（tracker 快照差值），全轮 `turnCalls`、remediation、`turnAssistantMessages`、`turn_end.toolCalls` 全部基于 tracker 快照。
- **Why**：回调对每次真实工具执行都触发（无论是否 ReturnDirectly），是权威信号源；stream-chunk 计数在本框架下结构性失真。这是「重复产物」与「假执行误判」的共同根因，必须先修。
- **Alternatives**：
  - 关掉 `ToolReturnDirectly` 让模型 tool_calls 进流 → 会让小模型把 {status:queued} 当未完成而循环重调，回归更糟。✗
  - 解析 ReturnDirectly 工具返回的 tool 消息反推 → 脆弱、与工具返回格式耦合。✗

### D1：重试前下发「轮内增量重置」事件，而非缓冲
- **What**：新增 `EventTurnReset`（wire `"turn_reset"`）。`runTurn` 在判定 `shouldRetryFakeAck` 为真、即将追加纠正消息重跑前，`emit(EventTurnReset)`。前端收到后：flush 并**删除**当前 in-flight 的 assistant 气泡与 reasoning 块，重置 `typer.current` / `reasoner.current`，回到 loading 态。
- **Why**：满足真·流式约束（第一遍仍逐字渲染），只在罕见重试路径丢弃脏增量。加法式、向后兼容——旧前端遇未知事件类型按既有「未知事件不致错」规则忽略（仅退化为旧的重复 bug，不崩溃）。
- **Alternatives**：
  - 缓冲到 attempt 结束再下发：违反真·流式 spec。✗
  - 前端按「done 之间内容去重」：脆弱，两遍文字可能不完全相同。✗

### D2：重试耗尽后的诚实兜底
- **What**：扩展 `remediationAction` 决策表，新增一支 `remediateHonestFail`：当 `toolCalls==0 && !cancelled && !capsuleAsked` 且 `looksLikeFakeExecAck(reply)` 为真时触发。`runTurn` 据此把 `reply` 替换为诚实反馈文案（区别于纯空轮的 `remediateClarify`/`remediateRefuse`）；若预分类命中白名单意图，则优先 `remediateClarify`（询问缺失参数）而非仅给文案。
- **Why**：对应诉求 a——「实际没产生 function call 但有似乎在做工具的行为时，应立即跟用户反馈」。假确认文字绝不能冒充成功回复。
- **决策表新增行**（在现有判断之后、`default` 之前）：
  - `toolCalls==0 && looksLikeFakeExecAck(reply) && hint.Whitelisted && hint.MissingKeyParam` → `remediateClarify`
  - `toolCalls==0 && looksLikeFakeExecAck(reply)` → `remediateHonestFail`（替换为诚实文案）

### D3：用户反馈驱动的补救提示
- **What**：新增 `looksLikeMissingOutputComplaint(userText)`（确定性正则，如「没(有)?(生成|做|出|跑)」「怎么没」「没看到」「失败了吗」+ 产物/模型/工具词）。在 `runTurn` 构造本轮上下文时，若该函数命中**且上一轮 assistant 消息零工具调用**（查 window/store 的上一轮结构），注入一条结构化「补救提示」System/User 旁注，标明「用户反馈上一轮 X 未真正执行，请本轮真正调用对应工具」。该提示仅引导，模型仍可推翻（与既有「意图提示」同构，见 prompt.go:63 第 11 条）。
- **Why**：对应诉求 b——用户反馈没生成某模型产物时，根据用户上一次反馈给出互动、重新触发真实调用。
- **Alternatives**：让会话模型自己从历史推断 → 弱模型不可靠，确定性识别更稳。

## Risks / Trade-offs
- **重置信号闪烁**：用户可能瞥见第一遍假文字短暂出现又消失。→ 可接受（重试路径罕见），且远好于重复文字常驻。
- **诚实文案误伤**：极少数真调了工具但 `looksLikeFakeExecAck` 也命中的情况 → 因兜底前置条件是 `toolCalls==0`，真调过工具不会进此分支，安全。
- **反馈识别误报**：用户正常描述里含「没生成」可能误触发补救提示 → 仅注入「提示」不强制路由，模型可推翻；且加了「上一轮零工具调用」前置条件收窄触发面。

## Migration Plan
纯加法：新增事件类型 + 决策表分支 + 一个识别函数。旧前端忽略未知事件（退化为旧行为，不崩）。无数据结构/持久化变更。先后端（事件 + 兜底 + 单测）→ 后前端（消费重置）→ 端到端冒烟。

## Open Questions
- 诚实失败文案具体措辞（实现阶段定稿，需符合 CLAUDE.md 语气：克制、不夸张）。
