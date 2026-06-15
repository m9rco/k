# Design: anchor-context-continuity

## 核心数据流

```
任务完成 (SSE task_done)
    ↓
generation.Service 调用 TaskAnnouncer hook
    ↓
main.go taskAnnouncer → hub.Send(task_done 事件)
    ↓ (同时)
Orchestrator.OnTaskDone(sessionID, assetID) ← 【新增】
    → sessions[sessionID].lastProducedAssetID = assetID

下一轮 Handle:
    buildNumbering(…) → 若无 ref/refs，查 lastProducedAssetID
        → BuildAssetNumbering 注入 [上次产物: 图N=id]
```

## 组件设计

### 1. `Orchestrator.lastProduced` map

```go
// 在 Orchestrator 结构体新增：
lastProduced map[string]string // sessionID → last produced asset_id
```

- 线程安全：复用 `o.mu`
- 生命周期：进程内存，重启后丢失（可接受；重启后历史从 DB 恢复，LLM 可从对话记录推断）
- 更新时机：`generation.Service` / `video.Service` 任务完成回调

### 2. 任务完成回调钩子

`generation.Service` 已有 `TaskAnnouncer` 接口将任务完成通知 hub。新增平行的 `AssetCallback func(sessionID, assetID string)` 注入点，在任务状态变为 `done` 时调用。

**不改动 SSE/WS 协议**：回调纯服务端内部，不影响现有事件格式。

### 3. `buildNumbering` 增加 last-produced 注解

```go
// 当 len(refs)==0 && ref=="" 且有 lastProduced 时：
lastProduced := orch.LastProduced(sessionID) // 新增方法
return agent.BuildAssetNumbering(refList, nil, lastProduced) // 第3个参数新增
```

`BuildAssetNumbering` signature 变为：
```go
func BuildAssetNumbering(order []AssetRef, selected []string, lastProduced string) string
```

输出示例：
```
[工作区: 图1=abc(generated), 图2=def(generated)] [上次产物: 图2]
```

### 4. System Prompt 新规则（Rule 12）

> 当消息携带 `[上次产物: 图N]` 且用户本轮未明确指定操作对象时，默认将图N 作为操作对象（source_asset_id 或主要 reference），无需再反问"请问要操作哪张图"。

### 5. Context Summarizer 资产锚点

`Window` 新增 `lastAssetOp` 字段：
```go
type assetOp struct {
    SourceID string // 被编辑的图
    OutputID string // 产出的图
}
```

`compressLocked` 在压缩时从被折叠的消息里提取最新的 assetOp（从 `[工作区…] [选中…]` + `[edit_image result ref=…]` 模式匹配），将其写入 summary 末尾作为结构化锚：

```
Earlier conversation summary:
- user: 把这张图换个山景背景
- assistant: (called tools: edit_image)
[最近编辑: source=abc123 → output=def456]  ← 锚点
```

### 6. Clarify Capsule 预填上次产物

`remediationClarify` 和 `clarify_intent` 在生成"操作哪张图"的选项时，检查 `lastProducedAssetID`，若存在则将其作为第一个选项预填：

```go
ClarifyOption{
    Label:        "继续在上一张（图N）上修改",
    Value:        "[asset lastProducedID] " + originalIntent,
    EditableHint: "继续修改图N",
}
```

## 权衡

| 方案 | 优点 | 缺点 |
|---|---|---|
| 后端 lastProduced 注入 | 零前端改动，纯服务端，重启丢失可接受 | 需要 task 完成回调路径 |
| 前端自动选中新产物 | 直观 | 影响 frontend 状态机，超出本 change 范围 |
| 增强 system prompt 规则 | 最简单 | 只是引导，不解决 context 压缩断链 |

**选择**：三者并用，后端注入（#3-4）为主，summarizer 锚点（#5）为辅，capsule 预填（#6）为 UX 改善。

## 不涉及的改动

- WS/SSE 协议格式不变
- 前端不改（选中状态、工作区 UI）
- 已有 fake-exec / remediation 逻辑不动（仅在 conversation-orchestration spec 补充文档描述）
