# Proposal: anchor-context-continuity

## 问题陈述

用户在连续对话中对同一张图做多轮修改时（如"换背景" → "再换个角色" → "调一下文案"），Agent 有时会操作错误的图片，或在 context 压缩后丢失"上次产物"的指向，导致每轮都从原始图出发而非在最新产物上叠加编辑。

更广泛来看，项目在以下两个维度存在改进空间：

1. **上下文记忆连续性**：模型幻觉兜底机制已较完善（fake-exec 检测、重试、honest-fail、remediation），但"多轮编辑链"在 context 压缩后断链的问题尚未系统处理。
2. **模型幻觉兜底**：现有机制已覆盖"假执行确认"和"缺少产物投诉"两类场景，但存在若干尚未覆盖的漏洞。

## 当前状态（代码分析）

### 已实现的幻觉兜底机制

| 机制 | 实现位置 | 作用 |
|---|---|---|
| Fake-exec 检测 + 重试 | `fakeack.go` + `agent.go` | 检测模型只回文字未调工具，最多1次重试 |
| Missing-output 投诉检测 | `fakeack.go` + `agent.go` | 下一轮注入 `BuildRemediationHint` |
| `clarify_intent` 选择框 | `tools.go` | 类 Cursor 结构化反问，2-4个选项 |
| 确定性意图预分类 | `intent.go` | 注入 `[意图提示]` 前缀引导工具选择 |
| Honest-fail 兜底 | `fakeack.go` | 重试后仍无工具调用则替换为真实失败反馈 |
| Dedup guard | `tools.go` | 防同轮重复工具调用 |
| 工作区编号映射 | `prompt.go` | `[工作区: 图N=id]` 注入，解决"图2"等引用 |

### "连续改图记忆"的根本缺口

**数据流分析**：

```
Turn N:
  前端发送: {ref: "asset_A", assetOrder: ["asset_A"]}
  后端注入: "[工作区: 图1=asset_A(generated)] [选中: 图1] 换背景"
  LLM 调用: edit_image(source_asset_id=asset_A)
  工具返回（LLM context 里）: "好的，正在按你的要求处理这张图..."  ← 只有中文，无 asset_id
  异步任务完成 → 产出 asset_B

Turn N+1:
  前端发送: {assetOrder: ["asset_A", "asset_B"]}  ← 无 ref/refs
  后端注入: "[工作区: 图1=asset_A(generated), 图2=asset_B(generated)] 再换个角色"
                                                              ↑ 没有 [选中] 注解！
  LLM 必须从历史对话推断"上次产物是图2"
```

**三个结构性缺口**：

1. **Async 工具结果不携带 asset_id**：`asyncMarshal` 对 standalone 路径返回纯中文字符串，LLM 无法从 tool result 知道产出了哪个 asset。

2. **`[选中]` 注解只在用户显式点选时存在**：follow-up turns 无显式选中时，`[工作区]` 前缀列出所有图但无"焦点"标注，LLM 依赖历史推断，context 压缩后断链。

3. **Context 压缩丢失编辑链**：`defaultSummarizer` 将旧轮压缩为 bullet points，"在图A上编辑产出图B"的结构信息变成模糊摘要，甚至被截断。

## 优化方向

### 方向一：后端注入"上次产物"锚点（最高优先级）

在 `Orchestrator` 中增加 per-session `lastProducedAssetID` 跟踪（任务完成时更新），在 `buildNumbering` 中当无 ref/refs 时注入 `[上次产物: 图N=id]` 前缀。System prompt 新增对应处理规则。

**效果**：无需用户显式选图，"连续改图"场景下 LLM 自动锚定上次产物，不依赖 context 压缩后能否推断。

### 方向二：Context 压缩保留资产编辑锚点

`Window` summarizer 在压缩时提取最近的"source→output 资产对"写入结构化 summary 锚点（非自然语言），压缩后不断链。

### 方向三：Clarify Capsule 预填上次产物

当 clarify_intent 询问"操作哪张图"时，把`[上次产物]` 作为第一个预填选项（"继续在图N上修改"），降低用户重新选图的摩擦。

### 方向四（已有机制增强）：扩展 fake-exec 和 honest-fail 覆盖范围

当前 `looksLikeFakeExecAck` 仅覆盖中文进行时描述，扩展至更多边缘模式（如模型描述"已生成"但无工具调用）。

## 变更范围

本 change 涉及三个新能力 delta：

- `sticky-last-output`：跟踪并注入"上次产物"锚点
- `summary-asset-anchor`：Context 压缩时保留资产编辑链
- `clarify-recent-context`：Clarify capsule 预填上次产物选项

以及对 `conversation-orchestration` spec 的若干 MODIFIED 条目（fake-exec 覆盖增强）。

## 不在本 change 内

- 前端 UI 改动（自动选中新产物等）— 属于 `frontend-experience` 的独立 change
- 模型自身提示词优化（已在 `prompt.go` 覆盖）— 不重复建模
- 跨 session 记忆 — 超出当前项目范围
