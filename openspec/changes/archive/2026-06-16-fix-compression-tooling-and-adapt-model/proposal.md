# Fix: 压缩后工具调用退化 + 再次适配不再静默跳过 + 适配路由到 Gemini

> 注：change-id 保留为 `fix-compression-tooling-and-adapt-model`（创建时如此命名）。

## Why

围绕"会话 Agent 行为正确性"的一组问题，统一表现为 **Agent 该调工具时不调**：

### 1. 上下文一压缩，模型就停止调用工具

会话进行到 context 触发压缩后，Agent 开始"只回话、不调工具"——本该调 `edit_image` /
`adapt_to_platform` 的轮次退化成纯文本确认。

现有代码（`internal/agent/window.go`）已对一个**症状**做了修复（2d7894a）：折叠边界落在
`assistant{tool_calls}` 与其 `role:tool` 结果之间会产生孤立 ToolMessage，被 provider 拒绝/
静默丢弃。但这只保证了"序列合法"，**没有解决根因**：

- `compressLocked` 把更早轮次折叠进 summary 时，`defaultSummarizer` 把
  `assistant{tool_calls}` 退化成一行散文 `(called tools: edit_image)`，整体塞进一条
  `SystemMessage`。
- 折叠后，**recent 窗口里可能一条"真实的 `assistant{tool_calls}`→`tool_result` 结构"都不剩**。
- 这与 `restoreLocked` 注释里的 **reverse-few-shot** 同源：历史里看不到"过去都在调工具"
  的结构性证据，模型据此推断"这是个纯聊天会话"，于是停止调工具。

### 2. 同一张图同一批尺寸，再次适配时工作区空空如也

用户对已适配过的图再次发起"适配到 TapTap 竖版/分享 ICON"，结果工作区什么也没出。排查发现
**两层去重叠加**导致：

- **service 层**：`generation.AdaptToPlatform` 调用 `findAdapted` 做"会话级去重"——
  以 `(源图, 尺寸)` 命中已持久化的旧产物就直接 `AdaptViaReused`、不发起新生成。用户再次发起
  时被静默重用，看不到新产物。
- **Agent 层**：即便 service 层放行，模型看到上下文里**已有一次成功的 `adapt_to_platform`
  记录**，会自行推断"之前已经做过了"，于是不调工具，只回"产物已在工作区，你可以查看图2和图3"。
  这是压缩 reverse-few-shot 之外，模型拒绝调工具的**第二条路径：自主判断"无需再做"**。

用户明确诉求：**再次发起就是要一份新产物**，去重不应阻止重新生成。

### 3. 选"AI 平台适配"时用 gemini-3-pro-image 出图

`adapt_to_platform` 的 AI 重绘路径固定走 `gemini-3-pro-image`，仅作用于本次适配请求，不改变
会话已选的 image 模型、不影响 `edit_image` 等其它图生图操作。

> 实现波折记录：此项一度被误回滚。当时把"空工作区"误判为 Gemini 凭证 fallback 导致，但真正
> 根因是 §2 的 `findAdapted` 去重。事实上 image 场景下所有模型共用同一套凭证
> （`sceneCredential(SceneImage)` → `ImagePrimary.BaseURL/APIKey`），`gemini-3-pro-image`
> 解析为 `Provider=gemini` + ImagePrimary 的 base/key；只要该网关支持 Gemini 原生协议即可工作。
> `ResolveImageModel` 内部已调 `IsModelAvailable`（要求 `ImagePrimary.APIKey != ""`），`ok=true`
> 即代表凭证就绪，不会注入空 key。去重移除后该路由已正确接回。

## What Changes

- **诊断（先于修复）**：每轮结束打一条结构化日志，记录 `model`、`compressed`、recent 中是否
  存在完整 `assistant{tool_calls}→tool_result` 交换、本轮真实工具执行数。用于把"压缩后不调
  工具"经验性归因到压缩/切换/清理三因子。
- **压缩保持工具就绪**：压缩后，当会话曾调用过工具，recent 窗口 SHALL 尽量保留一段**完整且
  真实**的 `assistant{tool_calls}→tool_result` 交换作为 few-shot 锚点（best-effort：无法
  在预算内保留时仍照常压缩，不阻塞）。
- **澄清三触发点预期**：明确"切换模型"与"上下文清理"都 SHALL NOT 反训练模型停用工具。
- **移除会话级适配去重**：`AdaptToPlatform` SHALL NOT 再以 `(源图, 尺寸)` 命中旧产物而跳过；
  每次适配请求都真正发起裁剪/AI 重绘。轮内重复调用防护（`dedup.firstSeen`）保留。
- **再次请求即再次执行（prompt 约束）**：System Prompt SHALL 明确——历史轮次完成过的操作
  不代表本轮无需再做；用户本轮再次发起命中能力的请求（哪怕完全相同），Agent SHALL 再次调用
  对应工具重新生成，SHALL NOT 以"之前已做过/产物已在工作区/可查看图N"为由跳过工具调用。
- **适配请求级 Gemini 路由**：`adapt_to_platform` 的 AI 重绘 SHALL 固定用 `gemini-3-pro-image`，
  作用域仅限本次适配请求；`gemini-3-pro-image` 不可用（image 场景凭证未配置）时 SHALL 优雅
  回退到会话 image override 或服务默认，不使适配失败。

## Capabilities Affected

- `conversation-orchestration`（MODIFIED：Context 滑动窗口管理；ADDED：压缩后工具调用连续性、
  诊断可观测性、再次请求即再次执行）
- `platform-adaptation`（REMOVED：会话级适配去重；ADDED：AI 重绘的请求级 Gemini 路由）

## Impact

- 代码：`internal/agent/window.go`（压缩保持工具就绪 + 测试）、`internal/agent/agent.go`
  （诊断日志、Gemini 请求级路由注入）、`internal/agent/tools.go`（`AdaptModelOverride` 字段 +
  `adaptProvider` helper）、`internal/generation/adapt.go`（移除 `findAdapted` 去重及死代码）、
  `internal/agent/prompt.go`（核心规则 + 规则14 加"再次请求即再次执行"）。
- 行为：压缩后 Agent 持续调工具；再次发起适配真正重新生成；AI 适配产物由 gemini-3-pro-image 生成。
- 风险：低。去重移除会让"重复同请求"多花一次生图成本，但符合用户预期；其余路径不变。
