## MODIFIED Requirements

### Requirement: 等待态分级状态机
系统 SHALL 通过分级等待 loading 态覆盖所有关键交互，确保用户操作后立即获得视觉反馈，杜绝"冷场"。覆盖范围 SHALL 包含：消息发送（turn_start → LoadingBubble）、文件上传（API 期间按钮 spinner）、尺寸选择提交（running 期间确认按钮 spinner）、重试生成（调用期间菜单项 disabled）。

#### Scenario: 消息发送等待态
- **WHEN** 用户提交消息后，turn_start 前
- **THEN** 对话区展示 P1 LoadingBubble（三点弹跳）
- **AND** 超时或非流式信号后升级为 P2 静态 spinner

#### Scenario: 文件上传等待态
- **WHEN** 用户触发文件上传（拖入或点击按钮选文件）
- **THEN** 上传按钮变为 disabled 状态并展示内联 `<Loader2 animate-spin>`，直至上传 API 全部返回
- **AND** 上传完成后按钮恢复可用

#### Scenario: 尺寸选择提交等待态
- **WHEN** 用户在尺寸选择器中点击确认
- **THEN** 确认按钮内联展示 spinner 并 disabled，防止重复提交
- **AND** 提交完成（适配任务创建）后对话框关闭

#### Scenario: 重试生成防连点
- **WHEN** 用户点击资产卡「重试生成」菜单项
- **THEN** 菜单项立即变为 disabled，防止重复触发
- **AND** 异步调用完成后视觉恢复（菜单已关闭或任务进入排队态）

## ADDED Requirements

### Requirement: 参考图来源标识
系统 SHALL 在 AI 产物卡片上展示其参考图来源的可视化标识。后端 SHALL 在资产列表中为 AI 产物补充 `referenceIds`（从生成来源记录读取）。前端 SHALL 在 `referenceIds.length > 1` 时渲染「参考 N 张」徽章；hover 时展开 ≤4 张参考图缩略图（宽 20px 头像叠叠乐样式），帮助用户快速感知产物来源。

#### Scenario: 参考图徽章展示
- **WHEN** 工作区展示一张由 N ≥ 2 张参考图生成的 AI 产物
- **THEN** 卡片左下角展示「参考 N 张」徽章（与尺寸标注同侧）
- **AND** 单张参考或上传/裁剪产物不展示此徽章

#### Scenario: hover 展开参考缩略图
- **WHEN** 用户 hover 上述卡片
- **THEN** 徽章区域展开最多 4 张参考图的小缩略图（20px 宽，叠叠乐排列）
- **AND** 超出 4 张时显示「+N」角标

#### Scenario: referenceIds 后端填充
- **WHEN** 资产列表 API 返回 AI 产物
- **THEN** `referenceIds` 字段包含该产物生成时所用参考图的 id 列表
- **AND** 无参考图的产物（裁剪/上传）`referenceIds` 为空或缺省

### Requirement: AI 操作在对话区同步呈现
系统 SHALL 确保所有 AI 参与的图片处理行为（包含对话驱动和直接触发的重试）在对话区均有对应的工具卡片呈现，不出现"工作区有进度、对话区无痕迹"的割裂体验。重试生成触发时 SHALL 立即在 chat 插入 `running` 工具卡；任务终态时 SHALL 更新为 `done` / `failed`。

#### Scenario: 重试触发时插入工具卡
- **WHEN** 用户触发资产重试生成
- **THEN** 对话区立即出现「重试生成」工具卡（running 状态，icon RotateCcw）
- **AND** 工作区同时出现任务占位骨架

#### Scenario: 重试完成更新工具卡
- **WHEN** 重试任务完成（done 或 failed）
- **THEN** 对话区对应工具卡更新为终态（✓ done 或 ✗ failed + 错误信息）
- **AND** 工作区占位替换为产物图片（done 时）或失败卡（failed 时）
