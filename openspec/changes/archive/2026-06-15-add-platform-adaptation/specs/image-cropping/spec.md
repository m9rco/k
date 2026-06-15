# image-cropping Specification (delta)

## MODIFIED Requirements

### Requirement: 非 AI 纯裁剪
系统 SHALL 在用户选择目标尺寸后，对图片执行纯图像处理（裁剪/缩放）生成目标尺寸产物，此过程不依赖 AI 模型。纯裁剪 SHALL 同时承担两个角色：(1) 作为 `platform-adaptation` 智能路由在**比例一致**时选用的确定性快路径；(2) 作为前端**手动裁剪兜底**入口，供用户在不希望调用 AI 时显式选用。裁剪 SHALL 按所选裁剪模式（`cover`/`contain`/`anchor`/`rect`，缺省 `cover`）执行。系统 SHALL 仅对标记为**可裁剪产出（`producible`）**的尺寸执行裁剪；对不可由纯裁剪产出的规格（如视频本体、外部视频链接），系统 SHALL 拒绝并返回明确反馈，而非静默跳过。

注：「切尺寸/适配尺寸」意图的**默认实现**不再是纯裁剪，而是 `platform-adaptation`（见该 capability）；纯裁剪在比例一致时被其复用，比例差异大时由其改走 AI 重绘。

#### Scenario: 按所选尺寸裁剪
- **WHEN** 用户从选择器中选择一个或多个可裁剪的目标尺寸
- **THEN** 系统对源图执行裁剪/缩放并为每个所选尺寸产出对应图
- **AND** 各产物回填工作区且不调用任何 AI 模型

#### Scenario: 作为平台适配的比例一致快路径
- **WHEN** `platform-adaptation` 判定源图与目标尺寸比例一致并选用快路径
- **THEN** 系统通过纯裁剪/缩放产出该尺寸，不调用 AI 模型
- **AND** 产物记录其渠道/尺寸归属

#### Scenario: 作为手动裁剪兜底
- **WHEN** 用户显式选择「手动裁剪」并指定尺寸与模式
- **THEN** 系统执行纯裁剪产出，不调用 AI 模型，绕过平台适配的 AI 路径

#### Scenario: 按所选模式裁剪
- **WHEN** 用户在发起裁剪时指定了裁剪模式
- **THEN** 系统对每个目标尺寸均按该模式产出
- **AND** 未指定模式时按 `cover` 产出

#### Scenario: 横竖版来源适配
- **WHEN** 源图为横版而所选目标为竖版（或相反）
- **THEN** 系统在裁剪/缩放时保持主体可见的前提下适配目标比例

#### Scenario: 拒绝不可裁剪规格
- **WHEN** 用户或 Agent 请求裁剪一个标记为 `producible=false` 的尺寸（如某渠道的视频规格）
- **THEN** 系统不产出图片
- **AND** 返回明确的"该规格无法由裁剪产出"反馈
