# reference-publishing Specification

## Purpose
TBD - created by archiving change add-vision-guided-adaptation. Update Purpose after archive.
## Requirements
### Requirement: 参考图发布到公共对象存储
系统 SHALL 支持把一组工作区图片发布到公共对象存储（COS），返回可公网拉取的 URL 列表，供视觉分析模型按 URL 读取。对象键 MUST 以图片**内容 md5（hex）+ 扩展名**构成（扩展名由 mime 推断），使同一图片内容始终映射到同一对象键（内容寻址、天然幂等）。发布 SHALL 在 COS 未配置时优雅不可用（返回明确的不可用信号），而非崩溃。

#### Scenario: 按 md5 发布并返回 URL
- **WHEN** 系统发布一组工作区图片
- **THEN** 每张图以其内容 md5 + 扩展名为对象键存入 COS
- **AND** 返回各图的公网 URL 列表

#### Scenario: COS 未配置降级
- **WHEN** COS 未配置而请求发布
- **THEN** 系统返回明确的「发布不可用」信号，调用方据此跳过依赖该 URL 的后续步骤，不崩溃

### Requirement: md5 全局去重缓存
系统 SHALL 维护一张**全局（不按会话隔离）的 `md5 → url` 持久缓存**（独立数据库表）。发布单张图片前 SHALL 先按内容 md5 查缓存：命中则直接返回缓存 URL 且**不再上传 COS**；未命中则上传后将 `md5→url` 写入缓存。该去重 SHALL 跨会话生效——任意会话上传过的内容，其他会话再次发布同一内容时直接复用。

#### Scenario: 命中缓存跳过上传
- **WHEN** 待发布图片的内容 md5 已存在于缓存
- **THEN** 系统直接返回缓存的 URL
- **AND** 不向 COS 重复上传该内容

#### Scenario: 未命中则上传并写缓存
- **WHEN** 待发布图片的内容 md5 不在缓存
- **THEN** 系统上传到 COS 并把 `md5→url` 写入缓存
- **AND** 后续（含其他会话）相同内容的发布直接命中

#### Scenario: 跨会话复用
- **WHEN** 会话 A 已发布某图片内容，会话 B 随后发布相同内容
- **THEN** 会话 B 直接复用会话 A 产生的 URL，不重复上传

### Requirement: 发布阶段对话反馈
系统 SHALL 把「参考图发布」作为一个对话阶段反馈到 web 页面：开始发布、（可选）逐张完成、全部就绪。反馈 SHALL 复用既有实时对话事件通道，使用户在适配开始前看到这一阶段的进展。

#### Scenario: 发布阶段可见
- **WHEN** 系统开始发布选中参考图
- **THEN** web 对话区出现该阶段的进展反馈（开始 → 全部就绪）
- **AND** 反馈经既有实时对话通道下发

