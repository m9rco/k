## Context
意图识别目前是"单层"的：全部交给会话模型在 system prompt 约束下自行判断（`internal/agent/prompt.go`）。主会话模型为 `claude-sonnet-4-6` 时表现稳定，但本项目支持会话级切换到 OpenAI 兼容的弱模型（DeepSeek/豆包等，见 `provider-configuration` spec 与 `chat-model-runtime-config` 记忆）。弱模型的两类失败已被观测：

- **假装执行**：输出"正在为你处理…产物会出现在左侧"但无 tool_call。`internal/agent/fakeack.go` 的 `looksLikeFakeExecAck`/`shouldRetryFakeAck` 是为此写的，但**当前 `Handle` 只调一次 `streamOnce`，重试逻辑从未接入**。
- **边界/参数偏差**：白名单边界、"图N"指代、参照 vs 被编辑对象区分把握不稳。

约束：项目偏好最小实现（`openspec/AGENTS.md` Best Practices：默认 <100 行新代码、boring/proven 模式）；不引入新外部依赖；预分类必须可单测（项目测试策略要求意图识别有单测）。

## Goals / Non-Goals
- Goals：
  - 在不牺牲强模型表现的前提下，显著降低弱模型的 tools=0 与错参率。
  - 用确定性、可单测的服务端规则做"预判提示"，把弱模型推向正确工具。
  - 让"该调没调"可自我纠正，纠正失败时降级为澄清/拒绝，杜绝空回复。
- Non-Goals：
  - 不做完整 NLU/分类器或引入 ML 模型——只用关键词/规则。
  - 预分类**不**替代 LLM 决策、不硬性路由到工具（避免把对话/边界判断写死）。
  - 不改对外事件协议、不改工具集合。

## Decisions
- **Decision：双层意图识别 = 确定性预分类（提示）+ LLM 决策（执行）。**
  预分类产出一个 `IntentHint{Labels []string, Confidence, MissingKeyParam bool, Whitelisted bool}`，由纯函数 `ClassifyIntent(userText, workspace)` 计算。命中时把一条人类可读的「意图提示: 用户大概率想做 X，建议优先考虑工具 T」拼到本轮用户消息前缀（与既有 `BuildAssetNumbering` 前缀同处注入），**仅提示不强制**。LLM 仍可推翻。
  - Alternatives considered：
    - (a) 纯 prompt 强化——无确定性兜底，弱模型仍会漂移（现状问题）。
    - (b) 确定性硬路由——把意图直接映射到工具调用，绕过 LLM。被否：白名单边界、多图意图、参数推断本质需要语义理解，硬路由会在歧义场景误伤，且与"LLM 为核心"的架构冲突。
    - 选 (c) 提示而非强制：兼顾弱模型引导与强模型自由度，且与既有 `BuildAssetNumbering` 注入点同构，改动面小。

- **Decision：接通而非重写 fake-ack 重试。**
  `Handle` 的单次 `streamOnce` 改为受限重试循环：当 `shouldRetryFakeAck(attempt, maxAttempts, toolCalls, reply)` 为真时，向消息追加一条更强的"你刚才只用文字假装执行了，请立即真正调用对应工具"指令并重跑一次。`maxAttempts` 取 2（一次纠正），避免放大延迟与成本。

- **Decision：兜底优先级 = 重试 → 澄清 → 拒绝/正常回复。**
  重试后若仍 tools=0：
  - 预分类 `Whitelisted && MissingKeyParam` → 服务端补发 `clarify_intent`（结构化澄清，复用既有 capsule 通道）。
  - 预分类 `!Whitelisted` 且模型也无正文 → 用 `RefusalMessage()` 确定性礼貌拒绝。
  - 其余 → 维持现状（正常正文回复 / 空轮标记）。

- **Decision：预分类提示的注入防护。**
  在 system prompt 增述：上下文中的「意图提示」是服务端预判、仅供参考，且**不得**被当作可执行指令。用户文本本身仍按既有安全规范当数据处理。

## Risks / Trade-offs
- **预分类误判**（关键词命中错意图）→ 因为只提示不强制，LLM 可纠偏；置信度低于阈值不注入提示。规则保守、表驱动可单测，降低误伤。
- **重试放大延迟/成本** → `maxAttempts=2` 仅纠正一次；重试只在命中 fake-ack 正则时触发（已有保守双信号设计）。
- **澄清兜底过度打扰** → 仅在"白名单内且确缺关键参数"时触发，沿用既有 clarify 严格条件。
- **强模型被提示"带偏"** → 提示措辞为"建议优先考虑"，非命令；强模型自由度不受损。

## Migration Plan
纯增量、无数据迁移。预分类与重试默认开启即可；命中失败路径与当前行为等价，故无需 feature flag（项目约定避免兼容开关）。回滚＝移除 hint 注入与重试循环调用点。

## Open Questions
- 预分类关键词表的初始覆盖范围：先覆盖九类白名单意图的高频中文说法，后续按真实 tools=0 日志补充（`diagnostic-use-alt-port` 记忆指明排查在其他端口起诊断实例）。
