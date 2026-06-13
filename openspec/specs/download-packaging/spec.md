# download-packaging Specification

## Purpose
TBD - created by archiving change add-asset-studio-mvp. Update Purpose after archive.
## Requirements
### Requirement: 单个资产下载
系统 SHALL 为工作区中每个图片/视频产物提供下载入口。

#### Scenario: 下载单图
- **WHEN** 用户对某个产物点击下载
- **THEN** 系统返回该产物原文件供下载

### Requirement: 批量打包下载
系统 SHALL 支持用户多选资产后批量下载，打包过程由 Web 后端完成（zip）。zip 内的文件 SHALL 按**渠道/尺寸**维度组织目录结构，使产物按交付平台清晰归类；对没有渠道/尺寸归属的资产（如上传源图或纯生成图）SHALL 归入一个约定的兜底目录。

#### Scenario: 批量打包
- **WHEN** 用户多选若干资产并点击批量下载
- **THEN** 后端将所选资产打包为单个 zip 并返回供下载

#### Scenario: 按渠道与尺寸分目录
- **WHEN** 批量打包包含具有渠道/尺寸来源的裁剪产物
- **THEN** zip 内按 渠道/尺寸 维度组织目录（如 `taptap/icon-512/...`）
- **AND** 无渠道/尺寸归属的资产归入约定的兜底目录

#### Scenario: 跳过无效项
- **WHEN** 批量选择中包含尚未生成完成或已失效的条目
- **THEN** 打包仅包含有效产物
- **AND** 通过通知告知用户哪些条目被跳过

