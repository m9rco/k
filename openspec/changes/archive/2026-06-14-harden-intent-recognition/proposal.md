# Change: 强化意图识别（确定性预分类 + 补救闭环）

## Why
当前意图识别完全依赖 system prompt 引导（`internal/agent/prompt.go`），把全部判断压在会话模型身上。当会话模型切到较弱的 OpenAI 兼容模型（如测试用 DeepSeek/豆包，见 `chat-model-runtime-config` 记忆）时，常见两类偏差：

1. **该调工具却没调**——模型用文字"假装执行"（"好的，正在为你处理…产物会出现在左侧"）却不发 tool_call，工作区空空。代码里已有 `looksLikeFakeExecAck`/`shouldRetryFakeAck`（`internal/agent/fakeack.go`）能识别这种情况，但**从未接入 `Handle` 的轮循环**——目前 `streamOnce` 只跑一次，没有任何自我纠正重试，这是已写好却闲置的补救逻辑。
2. **该拒却乱答 / 参数理解偏差**——弱模型对白名单边界、"图N"指代、参照物 vs 被编辑对象的区分把握不稳，产生 tools=0 或错参调用。

现有 spec 的「意图识别与白名单分发」要求里虽写了"减少 tools=0 出现频率"的目标，但**没有任何确定性机制兜底**，纯靠 prompt。本变更补上这一层：在调用 LLM 前做一层服务端确定性预分类，把用户话语归一为结构化「意图提示」注入上下文，强引导弱模型走向正确工具；并接通已有的 fake-ack 自我纠正重试，失败时降级为结构化澄清，杜绝空回复。

## What Changes
- 新增**确定性意图预分类层**（`internal/agent` 内，纯函数、可单测）：在每轮调用 LLM 前，用服务端关键词/规则把用户文本归一为一个或多个候选意图标签 + 置信度，并据此向本轮上下文注入一条结构化「意图提示」（hint），强引导模型选对工具。预分类**只提示、不强制**——LLM 仍是最终决策者，命中即直达工具，未命中不阻断正常对话。
- **接通已有的 fake-ack 自我纠正重试**：当一轮 tools=0 且回复疑似"假装执行"时，按现有 `shouldRetryFakeAck` 判定，携带更强指令自我纠正重试一次（受 MaxAttempts 限制）。
- **补救兜底闭环**：重试仍未行动且预分类判定意图明确但缺关键参数时，自动发起结构化澄清（`clarify_intent`）引导用户，而非留空回复；预分类判定为白名单外时，走确定性礼貌拒绝（接通已有 `RefusalMessage()`），不再消耗一次模型往返。
- **预分类提示规范化进 system prompt**：在分层 system prompt 中明确"上下文中可能出现『意图提示: …』，它是服务端预判，仅供参考，最终以你对用户真实意图的理解为准"，避免提示本身被当作可执行指令（复用既有注入防护表述）。
- 扩充意图识别相关单测：预分类规则表驱动测试、fake-ack 重试决策表、白名单外快速拒绝。

## Impact
- Affected specs: `conversation-orchestration`
- Affected code:
  - `internal/agent/prompt.go`（新增意图提示构建 + system prompt 增述；复用 `Capabilities`/`RefusalMessage`）
  - `internal/agent/agent.go`（`Handle` 接入预分类 hint 注入、fake-ack 重试循环、澄清/拒绝兜底）
  - `internal/agent/fakeack.go`（接通现有 dormant 逻辑，必要时微调强化指令文案）
  - 新增 `internal/agent/intent.go` + `intent_test.go`（确定性预分类纯函数与规则表）
- 非破坏性：预分类仅注入提示、不改变工具集与对外事件协议；命中失败时行为与现状一致。
