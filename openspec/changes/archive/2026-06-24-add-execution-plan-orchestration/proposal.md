# Change: 显式执行计划编排（强化 Agent 多步串联联动）

## Why
复合指令（如「第二张作为模板，换人物为第一张的角色，做成 iOS 4 个尺寸」）目前依赖会话模型自觉地：给第一步设 `await_result=true`、读出返回的 `asset_id`、再正确传入第二步。弱会话模型在这类任务上高频翻车：漏掉后续步骤（只换了角色没切尺寸）、未设 await 导致下一步拿到空产物、并行调用导致 `adapt_to_platform` 用了旧底图而非刚换好角色的新图。

而 ReAct 框架对异步任务工具走 `ToolReturnDirectly`（工具结果直接路由到 END、不回喂模型），使模型很难在一轮内可靠地观察前序产物再决策后续——真正的"前序产物喂入后序"串联缺乏确定性保障。

## What Changes
- 新增 **`submit_plan` 工具**：会话模型把一句话的复合需求**分解为结构化执行计划**（有序步骤，每步含工具名、参数、依赖与产物占位符引用），一次性提交给服务端。模型仍是分解决策者，但**串行执行交由服务端确定性驱动**，不再依赖模型自行 await/串联。
- 新增 **服务端计划执行器**：按依赖顺序**逐步串行执行**每个工具，把前序步骤产物 `asset_id` 通过占位符（如 `$step1.asset_id`）解析注入后续步骤参数，每步同步等待完成再进入下一步。
- **失败立即中断 + 报告**：任意步骤失败（工具错误/超时/产物为空）立即停止后续步骤，保留已完成产物，向用户明确告知"已完成哪几步 / 在第几步因何失败"。
- **给 `adapt_to_platform` 补 `await_result`**：使其能作为计划中间步骤被串联（当前它缺该字段，只能当链条末端）。
- **计划进度实时下发**：新增 plan 生命周期事件（计划创建/各步开始-完成-失败/计划结束），前端渲染**执行计划卡片**逐步点亮进度，失败步骤标红并显示原因。
- **确定性预分类识别复合请求**：检测多步信号（「然后/接着/再/并/做成 N 个尺寸」等），注入意图提示引导模型优先 `submit_plan`；单步请求仍直调单工具，不强制走计划。
- 强化 system prompt：明确「复合多步请求用 `submit_plan` 一次提交完整步骤，由系统串行执行」的规范，并保留既有单步直调、白名单、澄清优先等约束。

## Impact
- Affected specs:
  - `multi-task-pipeline`（MODIFIED 串联执行 + ADDED 计划模型/串行驱动/占位符/失败中断）
  - `conversation-orchestration`（MODIFIED 工具白名单纳入 submit_plan、ADDED 复合请求预分类与计划分发）
  - `realtime-transport`（ADDED 计划生命周期出站事件协议）
  - `frontend-experience`（ADDED 执行计划卡片渲染）
- Affected code:
  - `internal/agent/tools.go`（新增 `submit_plan` 工具与计划执行器；给 `adaptArgs` 补 `await_result`）
  - `internal/agent/prompt.go`（system prompt 串联规范 + 能力说明）
  - `internal/agent/intent.go`（复合请求预分类）
  - `internal/agent/agent.go`（计划执行的事件下发接线）
  - `internal/transport/event.go`（plan 事件类型）
  - `web/src/`（计划卡片组件与事件处理：`lib/types.ts`、`store/controller.ts`、`components/chat/`）
