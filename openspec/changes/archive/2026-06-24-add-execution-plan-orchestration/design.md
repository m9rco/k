## Context
复合宣发指令在一句话里串联多步（找图/换角色/换背景 → 切尺寸 → 生视频）。现状靠会话模型自行用 `await_result` 同步前序产物、再把 `asset_id` 填进后续工具。两个结构性障碍使其不可靠：

1. **ReAct + ToolReturnDirectly**：异步任务类工具（`AsyncTaskTools()`）的结果直接路由到 END、不回喂模型。模型在一轮内无法稳定"观察前序产物 → 决策后续步骤"，多步串联只能寄望模型一次性并行 emit 多个 tool_call，而并行调用无法表达"第二步依赖第一步产物"。
2. **弱会话模型**：默认会话模型偏弱，漏步、漏设 await、串错底图频发（见 memory `generation-source-vs-reference-primary`、`multi-image-edit-image-array-field`）。

用户已确认采用**显式执行计划**方案，失败语义为**立即中断 + 报告**。

## Goals / Non-Goals
- Goals
  - 复合多步请求由服务端**确定性串行执行**，前序产物 `asset_id` 可靠注入后续步骤。
  - 模型只负责**分解**（产出步骤+依赖+占位符），不负责 await/轮询/串联控制流。
  - 失败立即中断、保留已完成产物、向用户清晰报告进度。
  - 前端可见执行计划的逐步进度。
- Non-Goals
  - 不做条件分支/循环/并行 DAG 调度——仅"线性有序步骤 + 前序产物注入"（覆盖目标场景，避免过度设计）。
  - 不引入独立的"规划器 LLM"二次调用——复用会话模型在本轮通过 `submit_plan` 工具产出计划。
  - 不改变单步请求路径——单工具请求继续直调，不强制走计划。

## Decisions

### D1：计划由模型经 `submit_plan` 工具产出，而非独立规划调用
会话模型在识别到复合请求时调用 `submit_plan`，参数为有序步骤数组。这样：模型仍是意图分解的决策者（符合既有"模型最终决策"原则），但**执行控制流移交服务端**，绕开 ReAct 无法可靠串联异步工具的限制。
- **Alternatives considered**：(a) 让模型自行多轮 await 串联——现状，已证不可靠；(b) 服务端独立 planner LLM 二次调用——增加一次模型往返与一致性风险，且与"模型为唯一决策者"冲突。

### D2：步骤参数用占位符引用前序产物
每个 step 形如 `{id, tool, args}`，`args` 中可含占位符字符串 `$<stepId>.asset_id`（或 `.asset_ids`）。执行器在运行某步前，将其 args 里的占位符替换为已完成前序步骤的真实产物 id。
- 示例：`step2.args.source_asset_id = "$step1.asset_id"`。
- 解析失败（引用了不存在/未完成步骤、产物为空）→ 视为该步失败，按 D4 中断。

### D3：执行器串行驱动，复用既有工具与 awaitTask
执行器不重写各能力，而是**按 step.tool 调用既有工具实现**（edit_image/adapt_to_platform/image_to_video/generate_variants/text_to_image/search_images/overlay_text/extract_layer），异步任务步骤内部强制 `await_result` 语义（复用 `ToolDeps.awaitTask` 的 120s 轮询）。`adapt_to_platform` 需补 `await_result` 字段以参与中间步骤。
- 计划本身不是 `ToolReturnDirectly`——`submit_plan` 的最终结果（各步产物摘要）回喂模型，使模型可在计划结束后给用户一句自然语言总结。

### D4：失败立即中断 + 报告（用户选定）
任意步骤返回错误、超时、或产物为空 → 执行器停止，不再执行后续步骤。已完成步骤的产物保留工作区。`submit_plan` 返回结构化结果：`{completed:[...], failed:{step, reason}, pending:[...]}`，模型据此告知用户"已完成第 1 步换角色，在第 2 步切尺寸时因 XXX 失败"。

### D5：plan 生命周期事件
新增出站事件，复用既有 SSE/WS 通道与 `Notify` 接线：
- `plan_created`：`{planId, steps:[{id, tool, title}]}` —— 前端立即渲染计划卡片骨架。
- `plan_step_started` / `plan_step_done` / `plan_step_failed`：`{planId, stepId, ...}`。
- `plan_done`：`{planId, status:"completed"|"aborted"}`。
- 各步内部仍照常发既有 `task_*` 事件，计划卡片与产物占位互不冲突（计划卡片是上层进度视图）。

### D6：预分类识别复合请求
`ClassifyIntent` 增加复合信号检测（关键词：然后/接着/再/并/同时/做成…尺寸/各平台/N 个尺寸 + 出现≥2 个白名单动作）。命中时意图提示追加"建议用 submit_plan 一次提交完整步骤"。仅提示，不强制——模型仍可判定为单步直调。

## Risks / Trade-offs
- **串行总时长 = 各步之和**：复合任务变慢（每步 await 数十秒）。可接受——正确性优先于速度，且前端逐步进度可缓解等待焦虑。
- **占位符语法被模型填错**：执行器对无法解析的占位符按失败中断并报告，不静默吞错；prompt 给出明确示例降低出错率。
- **120s/步超时**：长尾生图可能超时致中断。沿用既有 awaitTask 超时；若实测不足可单独调参，不在本提案扩面。
- **与既有 `await_result` 多任务串联并存**：`submit_plan` 是更强保障；单一 await 链路保留不删，避免破坏既有行为。

## Migration Plan
- 纯增量：新增工具 + 执行器 + 事件，不移除既有 `await_result` 链路。
- `adapt_to_platform` 补 `await_result` 为加法式可选字段，默认 false，向后兼容。
- 前端计划卡片为新增渲染；旧客户端忽略未知 plan_* 事件不报错（沿用既有"未知事件忽略"约定）。

## Open Questions
- 计划步骤数上限（建议先定 6，防止模型产出过长计划）——实现时定为常量，超出则截断并提示。
