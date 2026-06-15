# Tasks: anchor-context-continuity

## 能力一：sticky-last-output（后端注入上次产物锚点）

- [x] 1. `Orchestrator` 新增 `lastProduced map[string]string`（sessionID → assetID），复用 `o.mu` 保护；新增 `SetLastProduced(sessionID, assetID)` 和 `LastProduced(sessionID) string` 方法
- [x] 2. 在 `generation.Service` 中新增 `onAsset`/`SetAssetCallback` 注入点，任务 `done` 时调用；`video.Service` 同样处理
- [x] 3. `main.go` 在构造 services 后注入回调：`genSvc/vidSvc/t2iSvc.SetAssetCallback(orch.SetLastProduced)`
- [x] 4. `agent.BuildAssetNumbering` 新增第3个参数 `lastProduced string`；当 selected 为空且 lastProduced 非空且该 id 在 order 中时，输出追加 `[上次产物: 图N]`
- [x] 5. `buildNumbering` in `main.go` 传入 `orch.LastProduced(sessionID)` 作为第3个参数
- [x] 6. `SystemPrompt()` 在「工具使用规范」新增 Rule 12：`[上次产物: 图N]` 含义与默认操作对象规则
- [x] 7. 为 `BuildAssetNumbering` 新增参数的单测；为 `SetLastProduced / LastProduced` 新增单测

## 能力二：summary-asset-anchor（Context 压缩保留编辑链）

- [x] 8. `Window` 新增 `lastAssetOp assetOp{ SourceID, OutputID string }` 字段
- [x] 9. `extractAssetOp` 扫描被折叠消息提取 source/output：**source 来自 edit 工具调用的 `source_asset_id` 参数**（实际生产流的 tool result 是 `[edit_image 已执行]`，不含 ref，故改从工具调用参数取）；**output 由 `[上次产物: 图N]` 注解对照 `[工作区: 图N=id]` 映射解析**。`compressLocked` 按字段 merge（source/output 常分属不同压缩批次，整体覆盖会丢一侧）
- [x] 10. 生成 summary 时，若 `lastAssetOp` 非零则 `stripAssetAnchor` 后追加 `[最近编辑: source=<id> → output=<id>]`；二次压缩时不重复
- [x] 11. 单测：含"有编辑链锚点""无编辑轮次不造锚点""锚点不重复"三个 case

## 能力三：clarify-recent-context（有上次产物时不询问；万不得已才澄清）

- [x] 12. `hasWorkspaceImage`（`intent.go`）扩展：`[上次产物:` 前缀同样视为"有图可操作"，使 `MissingKeyParam` 在有 lastProduced 时始终为 false
- [x] 13. `remediationAction`（`fakeack.go`）：加注释说明 `[上次产物:]` 经 `hasWorkspaceImage` 已令 `MissingKeyParam=false`，clarify 分支自然不触发
- [x] 14. `remediationClarify` 接收 `lastProduced string` 参数；非空时在选项列表首位插入"继续在上一张图上修改"选项（降级兜底）
- [x] 15. `agent.go` 调用 `remediationClarify` 时传入 `o.LastProduced(sessionID)`
- [x] 16. 为 `hasWorkspaceImage` 扩展（`MissingKeyParam` 用例）和 Task 14 新增单测

## 验证

- [x] 17. 运行 `go build ./... && go vet ./... && go test ./...` 全绿
- [ ] 18. 手工验测：连续3轮"换背景→换角色→换文案"均在上次产物上叠加，不回退到原图；显式重新选图1后切换到图1（需运行服务端 + 真实模型，留待用户验收）

## 验收期发现的 bug 修复（端到端补全）

### Bug 2a：第二次改图落在原图而非上次产物（焦点粘连，sticky-last-output 前端半场）

- [x] 19. `controller.ts` `sendNow`：发送后清空 `selected`（选中 id 已随消息下发，不跨轮粘连）
- [x] 20. `controller.ts` `applyTaskEvent` task_done：单产物 kind（generate/video）完成时把 `selected` 切到新产物 `data.assetId`；search/crawl 不改写。`kind` 在 setState 前从 `stateRef` 捕获，避开未应用更新的竞态（frontend-reviewer 指出）

### Bug 2b：增加角色被做成替换角色（edit-intent-add-vs-replace）

- [x] 21. `generation/prompt.go` 新增 `EditCharacterAdd EditKind="add_character"` + BuildPrompt 分支（"Add a new character…do NOT replace"）
- [x] 22. `tools.go` editArgs intent 枚举加 `add_character` + 描述区分；dispatch switch 放行；character_desc 描述更新；edit_image 工具描述与触发词更新
- [x] 23. `intent.go` 新增"增加角色"预分类规则；`prompt.go` Capabilities 加"增加角色"条目
- [x] 24. `generation_test.go` 新增 `TestBuildPromptAddVsReplaceCharacter`

### Bug 1：历史工具参数污染导致背景描述串轮（context-arg-hygiene）

- [x] 25. ~~脱敏历史工具调用参数~~ **已撤销**：改写历史 args（删除或脱敏）会腐化"工具调用作正向少样本"的设计，两次引发回归（模型省略 background_desc → 生图层 "background description required"，重试无用）。最终方案不动历史结构，改用 26+27
- [x] 26. `prompt.go` 新增 Rule 13【描述取自本轮用户原话】：禁止照抄历史旧描述、描述参数命中意图时不得为空、方向都没给才 clarify
- [x] 27. `tools.go` edit_image 入口校验描述缺失：因 edit_image 为 ToolReturnDirectly，**返回 Go error 会中止整轮、用户只看到 `[LocalFunc] failed to invoke tool` 报错且重试无用**（用户实测捕获）。改为 `editMissingDesc` 检测缺失 → 调 `d.Clarify` 弹结构化选择框（换背景给中国风/赛博朋克/简约/自然风光等）+ 返回良性 `statusClarified`（映射空 ack），不报错、不启动必败任务
- [x] 28. `ResetContext` 同步清理 `lastProduced`（清上下文后不再注入旧的 `[上次产物]`）
- [x] 28b. `dedup_test.go` 重写为 `TestEditToolMissingDescClarifies`（4 意图 + 空白值，断言无 error、弹 capsule、良性空结果）+ `TestEditToolProceedsWithDesc`

### 验证

- [x] 29. `go build ./... && go vet ./... && go test ./...` 全绿；前端 `npx tsc -b` 通过
