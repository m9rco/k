# asset-workspace Delta: stamp-album-workspace

## REMOVED Requirements

### Requirement: 工作区批量切尺寸工具栏入口
~~系统 SHALL 允许用户在工作区多选资产后，发起对所选资产的**批量切尺寸**操作（工具栏入口）。~~

**Removal rationale**：该入口被集邮册视图中的「生成全部」按钮取代。批量适配意图现由集邮册面板承载，工具栏入口删除以减少入口重复和 UI 噪声。SizePicker 对话框本身保留，仍由单个资产卡片的「切尺寸」调用。

#### Scenario: ~~批量切尺寸工具栏按钮~~（已移除）
- ~~**WHEN** 工作区有已选资产~~
- ~~**THEN** 工具栏显示「批量切尺寸」按钮~~

## MODIFIED Requirements

### Requirement: 可操作预览/工作区（视图模式枚举）
系统 SHALL 提供工作区视图切换，支持三种模式：`grid`（网格全览）、`timeline`（时间轴流水线）、`stamp`（集邮册）；所选视图持久化至 sessionStorage。

**修改内容**：在原有 `grid | timeline` 枚举中新增 `stamp`。

#### Scenario: 三种视图模式切换
- **WHEN** 用户点击工具栏视图 toggle 中的任意图标
- **THEN** 视图区切换到对应模式（网格 / 时间轴 / 集邮册）
- **AND** 选择持久化到 sessionStorage
