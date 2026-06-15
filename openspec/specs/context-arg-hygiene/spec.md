# context-arg-hygiene Specification

## Purpose
TBD - created by archiving change anchor-context-continuity. Update Purpose after archive.
## Requirements
### Requirement: 系统提示约束描述取自本轮原话
Agent system prompt SHALL 包含一条规则：调用生图类工具时，描述类参数（`background_desc`/`character_desc`/`text_content`/`desc`/`motion`）必须依据用户【本轮】诉求填写，不得照抄历史轮次中曾用过的旧描述；历史里出现的旧描述只代表过去那次需求，与本轮无关。该规则同时声明：命中编辑/生成意图时这些描述参数不得为空，应从用户本轮原话提炼非空描述；仅当用户连方向都未给出时才用 clarify_intent 询问。

#### Scenario: 本轮描述不沿用旧值
- **GIVEN** 更早一轮用户曾"改成蓝色背景"（历史工具调用含 background_desc="蓝色背景"，结构完整保留在窗口中作为少样本）
- **WHEN** 本轮用户说"把背景换成中国风"
- **THEN** 模型据本轮原话生成中国风的 background_desc，而非沿用历史里的"蓝色背景"

#### Scenario: 历史工具调用结构不被改写
- **WHEN** 系统将已完成的工具调用轮次写入 context 窗口（实时或重启恢复）
- **THEN** 该工具调用的参数原样保留（含其原始描述取值），不删除、不脱敏字段
- **AND** 模型仍能从中看到过去轮次确实调用了工具并带齐了参数（正向少样本不被破坏）

### Requirement: 描述缺失在工具入口拦截并以选择框反馈
`edit_image` 在启动异步任务前 SHALL 校验命中意图所需的描述参数：`change_background` 需 `background_desc`、`change_character`/`add_character` 需 `character_desc`、`change_text` 需 `text_content`。为空或仅空白时，由于 edit_image 为 ToolReturnDirectly（返回 Go error 会以空回复中止整轮、用户只看到报错且重试复用空参数无法恢复），系统 SHALL **不返回 error**，而是通过 clarify 回调向用户弹出一个带 2-4 个可选/可改具体选项的结构化问题（如换背景给出「中国风/赛博朋克/简约纯色/自然风光」），并返回一个良性结果（映射为空 ack，不产生多余气泡、不启动必败任务）。该 clarify 回调与模型主动调用 clarify_intent 共用同一通道，故 turn_end 会标记 hasCapsule，后续 follow-up 被正确抑制。

#### Scenario: 空描述弹出选择框而非报错
- **WHEN** edit_image 以 `intent=change_background` 调用但 `background_desc` 为空或仅空白
- **THEN** 工具不返回 Go error、不中止整轮、不创建异步任务
- **AND** 通过 clarify 回调向用户弹出"背景想换成什么风格"的结构化问题，含可选/可改的具体选项
- **AND** 工具返回良性结果，前端不出现多余空气泡，且 turn_end 标记有待答 capsule

#### Scenario: 其余意图同样弹选择框
- **WHEN** edit_image 以 change_character / add_character / change_text 调用但对应描述为空
- **THEN** 分别就角色描述 / 文案内容弹出对应的结构化选择框，均不报错、不启动任务

#### Scenario: 描述齐全时正常执行
- **WHEN** edit_image 各意图的描述参数非空
- **THEN** 不触发 clarify，正常启动生成任务

