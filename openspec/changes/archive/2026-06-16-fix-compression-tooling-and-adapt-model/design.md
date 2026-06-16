# Design: fix-compression-tooling-and-adapt-model

## 1. 根因分析

### 1.1 压缩路径的 reverse-few-shot 漂移

`compressLocked`（`window.go:258`）将 `recent[:foldCount]` 打包成 summary，`defaultSummarizer` 对每条消息只保留文本：

```
assistant{tool_calls:[edit_image]} → "(called tools: edit_image)"
role:tool → "tool: [edit_image result ref=xxx] …"
```

这两条 summary 行以 `SystemMessage` 注入 Anthropic/OpenAI 的 `system` 字段。**`messages` 列表中不再存在任何 `assistant{tool_calls}→tool_result` 结构**。模型的 few-shot 信号消失，决策漂移到"纯文字回复"。

2d7894a 的孤立修复（foldCount 跳过 role:tool 开头）保证了序列合法性，但**不保证** recent 里仍有工具调用样例。当 keepRecent < 最近一次完整工具交换所占消息数时，样例会被整轮折叠进 summary，漂移仍然发生。

### 1.2 三触发因子的独立性

| 触发因子 | 窗口结构影响 | 当前是否破坏工具调用 |
|---|---|---|
| 自然压缩（budget 超限） | 折叠旧轮到 summary | **是**（本提案的 bug） |
| 切换 chat 模型（`SwitchModel`） | 无（仅更换下一轮的 chatModel 实例） | 不影响窗口；新模型若工具调用能力弱则会退化，但这是模型能力问题，不是 bug |
| 上下文清理（`ResetContext`） | 重置为 system-only 的空窗口 | 不影响；空窗口的第一轮是新鲜的，无漂移 |

结论：**压缩是唯一明确的 bug**。切换模型和清理上下文是合理行为，但可以用诊断日志来实证确认。

### 1.3 诊断日志设计

在 `Handle` 尾部已有一条日志（`turn done toolCalls=%d`），扩充它：

```
agent: session=%s turn done model=%s compressed=%t \
  recent_roles=%s has_tool_exchange=%t \
  toolCalls=%d replyLen=%d cancelled=%t
```

- `model`：当前轮使用的 model id（帮助区分"切换模型后第一轮"）
- `compressed`：`w.Compressed()`
- `recent_roles`：recent 消息角色序列，如 `user,assistant[tc],tool,user`（快速看 fewshot 结构）
- `has_tool_exchange`：recent 中是否存在至少一对 `assistant{tool_calls}→role:tool`

### 1.4 压缩后保持工具就绪（Tool-Primed Window）

**约束**：当 `w.summary != nil`（已压缩）且历史曾有工具调用时，recent 窗口里 SHALL 保留至少一段完整的 `assistant{tool_calls}→tool_result` 交换（one or more pairs）。

**实现策略（最小改动）**：在 `compressLocked` 的 foldCount 计算后加一个 back-off：

```go
// 如果折叠后 recent 里没有完整工具交换且窗口曾有工具调用（由 lastToolExchange 标记），
// 向后退 foldCount 直到 recent 保留至少一个工具交换对，或无法再退（此时跳过本轮压缩）。
```

为此，引入 `Window.hasEverCalledTool bool`（初始 false；只要 Append 一条 assistant{tool_calls} 消息就置 true），用于区分"从未用过工具"的纯聊天窗口（无需保护）。

```
compressLocked 伪码：
  foldCount := len(recent) - keepRecent
  advance past orphan tool messages （现有逻辑）
  if hasEverCalledTool && !recentHasToolExchange(recent[foldCount:]) {
      orig := foldCount
      // back off：向 recent 方向移动 foldCount 直到 recent 里有工具交换
      for foldCount > 0 && !recentHasToolExchange(recent[foldCount:]) {
          foldCount--
      }
      if foldCount == 0 { foldCount = orig } // 找不到 → 恢复原值照常压缩（不阻塞）
  }
```

`recentHasToolExchange(msgs)` = msgs 中存在 `assistant{tool_calls}` 且其后紧跟至少一个对应的 `role:tool`。

这是 O(n) 扫描，msgs 最多 keepRecent+几条，开销可忽略。**best-effort**：当无论怎么退都无法在
recent 保留工具交换（例如 keepRecent 很小、工具交换在很靠前的位置）时，恢复原始 foldCount 照常
压缩，绝不因坚持保留而陷入"压不动"的死循环——这是与早期实现（`break` 跳过本轮）的关键区别，
后者会让既有的 `TestWindowSummaryPreservesAssetAnchor` 等用例无法触发压缩。

### 1.5 再次请求即再次执行（两层去重的移除/约束）

「同一张图同一批尺寸，再次适配时工作区空空如也」由两层去重叠加导致，分别在 service 层与 Agent 层修复：

**service 层（移除 `findAdapted`）**：`AdaptToPlatform` 原本在每个尺寸前调用 `findAdapted`
做"会话级去重"——命中已持久化的 `(源图, 尺寸)` 旧产物即 `AdaptViaReused`、不发起新生成。直接
**删除该调用**，每次适配都真正发起裁剪/AI 重绘。`findAdapted` 函数体暂留（无调用方），可由后续
清理 change 删除。轮内 `dedup.firstSeen` 保留（挡同一轮并行重复调用）。

**Agent 层（System Prompt 约束）**：即使 service 层放行，模型看到上下文里已有一次成功的
`adapt_to_platform` 记录，会自行推断"之前已经做过了"而不调工具，只回"产物已在工作区，可查看
图N"。这是 reverse-few-shot 之外、模型拒绝调工具的第二条路径。修复是在 prompt.go 两处加
「再次请求即再次执行」约束：

- **核心规则1**（通用，覆盖所有工具）：历史完成过的操作不代表本轮无需再做；用户再次发起命中
  能力的请求就必须再次调用工具，禁止以"之前已做过/产物已在工作区/可查看图N"为由跳过。
- **规则14**（平台适配专项）：同图同尺寸再次发起也要重新调 `adapt_to_platform`。

### 1.6 适配请求级 Gemini 路由

`Handle` 构建 `ToolDeps` 时注入 `AdaptModelOverride = gemini-3-pro-image`，`adaptProvider(d)`
在 AI 重绘时优先用它（否则回退 `ImageOverride`）：

```go
// agent.go: Handle
if pc, ok := o.cfg.ResolveImageModel(config.SceneImage, "gemini-3-pro-image"); ok {
    deps.AdaptModelOverride = &pc
}
// tools.go: newAdaptTool 的 AI 重绘
outcomes, err := d.Generation.AdaptToPlatform(ctx, …, adaptProvider(d))
```

**关键认知（一度误判后修正）**：image 场景下所有模型共用同一套凭证——
`sceneCredential(SceneImage)` 对 gpt-image 和 gemini-3-pro-image 都返回 `ImagePrimary.BaseURL`
/ `ImagePrimary.APIKey`。于是 `gemini-3-pro-image` 解析为 `Provider=gemini` + ImagePrimary 的
base/key，`NewProvider` 据 `Provider=gemini` 建 `GeminiProvider` 打 `:generateContent`；只要
ImagePrimary 网关（yunwu 类统一网关）支持 Gemini 原生协议即可工作。

`ResolveImageModel` 内部已调 `IsModelAvailable`（要求 `ImagePrimary.APIKey != ""`），返回
`ok=true` 即代表 image 场景凭证就绪——不会注入空 key。凭证缺失时 `ok=false`，
`AdaptModelOverride` 保持 nil，`adaptProvider(d)` 回退到 `ImageOverride`，适配不失败。

> 历史波折：此路由一度被误回滚——当时把"空工作区"误判为 Gemini 凭证 fallback 失败，但真正
> 根因是 §1.5 的 `findAdapted` 去重（若 Gemini 真的失败，任务会进 `failed` 状态、不会留下可被
> 去重命中的成功产物；而日志显示工具成功、产物存在，正说明问题在去重而非生图）。去重移除后
> 路由已正确接回。

## 2. 不变量

- `ResetContext`（上下文清理）后 `hasEverCalledTool = false`，新窗口不受保护约束，符合"清理后干净启动"的预期。
- `keepRecent` 的语义不变（只是在"工具就绪"约束下可能少折叠一轮）。
- 适配不再做持久化去重；轮内 `dedup.firstSeen` 仍挡同一轮并行重复调用。
- 普通图生图（`edit_image`）仍用会话 `ImageOverride`，不受 Gemini 适配路由影响。

## 3. 测试策略

- `window_test.go`：`TestCompressPreservesToolExchange`（有工具历史，压缩后 recent 仍含工具
  交换）、`TestCompressChatOnlyNoConstraint`（纯聊天不受约束，需用大消息超过 256 最小预算）；
  原 `TestCompressNoOrphanToolMessage` 保留。
- 既有 `TestWindowSummaryPreservesAssetAnchor` 必须仍通过——验证 back-off 的 best-effort 回退
  不会阻塞应当发生的压缩。
- `adapt_test.go`：`TestAdaptReRequestRegenerates`（再次请求生成新 task/asset，不重用）；删除
  失效的 `TestAdaptDifferentSizeNotDeduped` / `TestAdaptSessionLevelDedup`。
- 诊断日志、prompt 约束主要靠 `go build` + 既有 agent 测试套件兜底，外加人工验证
  （tasks.md 验证清单）。
