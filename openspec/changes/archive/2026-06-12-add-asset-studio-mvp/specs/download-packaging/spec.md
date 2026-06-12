## ADDED Requirements

### Requirement: 单个资产下载
系统 SHALL 为工作区中每个图片/视频产物提供下载入口。

#### Scenario: 下载单图
- **WHEN** 用户对某个产物点击下载
- **THEN** 系统返回该产物原文件供下载

### Requirement: 批量打包下载
系统 SHALL 支持用户多选资产后批量下载，打包过程由 Web 后端完成（zip）。

#### Scenario: 批量打包
- **WHEN** 用户多选若干资产并点击批量下载
- **THEN** 后端将所选资产打包为单个 zip 并返回供下载

#### Scenario: 跳过无效项
- **WHEN** 批量选择中包含尚未生成完成或已失效的条目
- **THEN** 打包仅包含有效产物
- **AND** 通过通知告知用户哪些条目被跳过
