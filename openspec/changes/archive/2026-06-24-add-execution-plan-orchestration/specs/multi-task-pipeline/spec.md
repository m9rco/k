## MODIFIED Requirements
### Requirement: 单指令串联多工具执行
系统 SHALL 支持 Agent 在一轮对话内完成用户一句话描述的复合任务（多个连续操作）。对于**前后步骤存在产物依赖**的复合请求（如「换角色 → 切尺寸」「找图 → 生视频」），Agent SHALL 通过 `submit_plan` 工具一次性提交一份**结构化执行计划**（有序步骤），由服务端**确定性串行执行**，而非依赖模型自行 await 与手动串联。Agent 仍是计划分解的决策者；执行控制流（串行驱动、产物传递、失败处理）由服务端负责。对于无依赖的单步请求，Agent SHALL 直接调用对应单工具，不强制走计划。

#### Scenario: 复合依赖指令经计划串联执行
- **WHEN** 用户发出「第二张作为模板，换人物为第一张的角色，做成 iOS 4 个尺寸」
- **THEN** Agent 调用 `submit_plan` 提交两步计划：step1=edit_image(change_character，source=第二张，reference=第一张)、step2=adapt_to_platform(source=$step1.asset_id，size_ids=iOS 4 个尺寸)
- **AND** 服务端先执行 step1 并等待其产物完成，再把 step1 产物 asset_id 注入 step2 的 source 后执行 step2
- **AND** 最终工作区包含换好角色的图与其 4 个尺寸适配产物

#### Scenario: 中途步骤失败时整体立即中断
- **WHEN** 计划某步骤失败（工具报错 / 超时 / 产物为空）
- **THEN** 执行器立即停止，不执行任何后续步骤
- **AND** 已成功步骤的产物保留在工作区
- **AND** `submit_plan` 返回结构化结果，标明已完成步骤、失败步骤及其原因、未执行步骤；Agent 据此告知用户"已完成第几步、在第几步因何失败"

#### Scenario: 无依赖单步请求不走计划
- **WHEN** 用户只要求单个操作（如「把这张图换个背景」）
- **THEN** Agent 直接调用 edit_image，而非 submit_plan
- **AND** 行为与既有单步直调一致

## ADDED Requirements
### Requirement: 结构化执行计划工具
系统 SHALL 提供 `submit_plan` 工具，入参为有序步骤数组，每个步骤 SHALL 含：步骤 id、工具名（限白名单内可编排工具）、该工具的参数对象。步骤参数中 SHALL 支持以占位符 `$<stepId>.asset_id` / `$<stepId>.asset_ids` 引用前序步骤的产物，使后续步骤能消费前序产物。计划步骤数 SHALL 受上限约束（防止超长计划）；超出上限时系统 SHALL 截断并提示。`submit_plan` 的最终结果 SHALL 回喂会话模型（非 ToolReturnDirectly），使模型可在计划结束后向用户作一句自然语言总结。

#### Scenario: 提交带产物占位符的计划
- **WHEN** Agent 调用 submit_plan，step2 的 source_asset_id 填为 "$step1.asset_id"
- **THEN** 系统接受该计划并在执行 step2 前将占位符解析为 step1 实际产出的 asset_id
- **AND** step2 以该真实 asset_id 作为输入执行

#### Scenario: 占位符无法解析按失败处理
- **WHEN** 某步骤引用了不存在/未完成的步骤，或被引用步骤的产物为空
- **THEN** 系统将该步骤视为失败，按"失败立即中断"语义停止并报告
- **AND** 不以空值或臆造值继续执行

#### Scenario: 计划步骤数超上限被收敛
- **WHEN** Agent 提交的步骤数超过系统上限
- **THEN** 系统截断到上限并在结果中提示已截断
- **AND** 不因超长计划无限执行

### Requirement: 计划串行执行与产物传递
系统的计划执行器 SHALL 按步骤顺序**串行**执行，每个异步任务步骤 SHALL 同步等待其任务完成（复用既有 await 轮询语义）后才进入下一步。执行器 SHALL NOT 重新实现各能力，而是按步骤工具名调用既有工具实现（edit_image / adapt_to_platform / image_to_video / generate_variants / generate_image_from_text / search_images / overlay_text / extract_layer）。为支持作为中间步骤被串联，`adapt_to_platform` SHALL 提供可选 `await_result` 字段：置为 true 时同步等待 AI 重绘任务完成并在结果中返回产物 asset_id。

#### Scenario: 异步步骤同步等待后再进入下一步
- **WHEN** 执行器执行一个异步生图步骤
- **THEN** 执行器等待该步任务完成并取得其产物 asset_id 后，才开始下一步
- **AND** 下一步若引用该产物，能拿到真实 asset_id 而非空值

#### Scenario: adapt_to_platform 作为中间步骤返回 asset_id
- **WHEN** 计划中 adapt_to_platform 步骤设 await_result=true 且后续步骤依赖其产物
- **THEN** 该步同步等待 AI 重绘完成并返回产物 asset_id
- **AND** 后续步骤可消费该 asset_id

#### Scenario: 执行器复用既有工具实现
- **WHEN** 计划某步骤工具名为 edit_image
- **THEN** 执行器调用既有 edit_image 实现完成该步，颜色适配/参照归属等既有行为保持不变
