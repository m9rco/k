## 1. 后端 · 计划数据模型与执行器
- [x] 1.1 在 `internal/agent` 定义计划数据结构：`planStepArg{ID, Tool, Args}`、`submitPlanArgs{Steps}`，及执行结果 `planResult{PlanID, Status, Truncated, Steps}` / `planStepResult`（`plan.go`）
- [x] 1.2 实现占位符解析 `resolvePlaceholders`：扫描 step.Args 中 `$<stepId>.asset_id` / `.asset_ids`，用已完成步骤产物替换；引用缺失/产物为空时返回可识别错误
- [x] 1.3 实现串行执行器 `runPlan`：按序执行各步，异步任务步骤强制 `await_result=true` 复用 `awaitTask` 同步等待；按 step.Tool 路由到既有工具实现（经 `tool.InvokableRun` 复用，零重复）
- [x] 1.4 失败立即中断：任一步出错/超时/产物空即停止，后续步骤标 skipped，保留已完成产物，组装 planResult 返回
- [x] 1.5 步骤数上限常量 `maxPlanSteps=6`，超出截断并在结果中标 `Truncated`

## 2. 后端 · submit_plan 工具与 adapt 改造
- [x] 2.1 给 `adaptArgs` 增加可选 `await_result` 字段；置 true 时对 AI 重绘步骤（仅 task_id 的 size）轮询并回填 asset_id（默认 false 向后兼容）；`asyncMarshal` 同步识别 outcomes 形状
- [x] 2.2 新增 `newSubmitPlanTool`：入参为有序步骤数组，调用执行器，返回 planResult 结构化 JSON（非 ToolReturnDirectly，结果回喂模型）
- [x] 2.3 在 `Tools()` 注册 submit_plan（捕获已构建的 chainable 工具 map）；确认未加入 `AsyncTaskTools()`

## 3. 后端 · 预分类与 System Prompt
- [x] 3.1 `intent.go`：增加复合多步信号识别 `looksCompound`（连接词/多尺寸尾 + ≥2 白名单动作；单一 adapt 不误判），`IntentHint.Compound` 暴露给提示构建
- [x] 3.2 `prompt.go` BuildIntentHint：命中复合信号时追加“建议用 submit_plan 一次提交完整步骤”引导
- [x] 3.3 `prompt.go` SystemPrompt：重写规则 8 为计划编排规范（含 `$stepN.asset_id` 占位符示例、失败立即中断说明、单步直调边界）

## 4. 后端 · 事件下发
- [x] 4.1 `transport/event.go`：新增 plan 事件类型（plan_created / plan_step_started / plan_step_done / plan_step_failed / plan_done）
- [x] 4.2 执行器经 `PlanEmitter` 接口下发事件；`agent.go` 的 `planEmitter` 适配器接线到 hub，注入 `ToolDeps.PlanEvents`
- [x] 4.3 各步内部既有 `task_*` 事件照常下发（复用既有工具实现，未改其事件路径）、与 plan 事件并存

## 5. 前端 · 计划卡片
- [x] 5.1 `lib/types.ts`：新增 `PlanItem` / `PlanStepStatus` 类型
- [x] 5.2 `store/controller.ts`：处理 plan_* 事件（`onPlanEvent` + `patchPlan`），维护计划与各步状态；未知事件天然忽略
- [x] 5.3 `components/chat/plan-card.tsx`：新增执行计划卡片组件，逐步点亮/标红/skipped，遵循既有科技感风格与过渡
- [x] 5.4 计划卡片与既有产物回填、长任务占位并存（纯上层进度视图，不触碰资产处理）

## 6. 测试与验证
- [x] 6.1 占位符解析表驱动单测（正常引用、缺失引用、空产物、多 asset_ids、嵌套 slice、字面量）`TestResolvePlaceholders` / `TestParseStepOutput`
- [x] 6.2 执行器串行/失败中断单测（mock 工具：成功链路 `TestRunPlanSuccessChain`、第二步失败中断 `TestRunPlanAbortsOnFailure`、非串联工具拒绝、超限截断）
- [x] 6.3 预分类复合信号识别表驱动单测 `TestClassifyIntentCompound`（复合命中 / 单动作不命中 / 模糊不命中）
- [x] 6.4 事件序列断言：`recordEmitter` 在执行器测试中断言 created→step_done×N→completed 与 step_failed→aborted
- [x] 6.5 `go build ./...`、`go test ./...`、`go vet` 全绿；前端 `npm run build` 通过
- [ ] 6.6 手测目标场景「第二张换成第一张角色 → iOS 4 尺寸」：需配置生图/适配供应商后端到端验证（本机无凭证，未执行）
