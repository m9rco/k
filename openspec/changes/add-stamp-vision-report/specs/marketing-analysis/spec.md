## ADDED Requirements

### Requirement: 按需只读宣发分析端点

系统 SHALL 提供一个只读 HTTP 端点 `POST /api/session/{id}/vision-report`，输入一组**有序的** asset id（`{assetIds: string[]}`，上限 16），返回该参考图集的宣发要素报告，供 chat 适配流程之外的场景（如图章模式参考图区）按需展示。该端点 SHALL 优先命中缓存：命中时直接返回缓存报告而不调用视觉模型；未命中时现场分析并将结果写回缓存。

该端点 MUST 复用与适配流程**完全相同**的缓存 key 规则（见「分析报告缓存 key 单一真源」），使图章模式按需分析与 `adapt_to_platform` 适配流程**双向共享**同一份 `vision_reports` 缓存。

端点行为契约：

- 当注入的分析能力缺失（vision 未配置）时 SHALL 返回 503。
- 当 `assetIds` 为空或超过 16 时 SHALL 返回 400。
- 分析失败（上游错误/全部 asset 不可读）时 SHALL 返回 200 且响应体 `{available: false, error}`（优雅降级，不向调用方抛硬错误），且 SHALL NOT 把失败结果写入缓存。
- 成功时 SHALL 返回 200 且响应体 `{available: true, report, count}`，`count` 为参与分析的参考图数。

#### Scenario: 命中缓存秒回不调模型
- **WHEN** 请求的参考图集对应的缓存 key 已有报告
- **THEN** 端点直接返回该缓存报告，不调用视觉模型
- **AND** 即使此刻 vision 不可用，只要缓存命中仍能返回

#### Scenario: 未命中现场分析并回写
- **WHEN** 请求的参考图集此前未分析过（缓存未命中）
- **THEN** 端点现场调用视觉模型分析该有序参考图集，产出报告
- **AND** 将报告写入按同一 key 键的全局缓存，供后续适配/再次展示复用

#### Scenario: vision 未配置返回 503
- **WHEN** 分析能力未注入（vision/COS 不满足配置条件）
- **THEN** 端点返回 503

#### Scenario: 参数非法返回 400
- **WHEN** `assetIds` 为空，或数量超过 16
- **THEN** 端点返回 400

#### Scenario: 分析失败优雅降级
- **WHEN** 现场分析失败（上游超时/错误），或全部 asset 字节不可读
- **THEN** 端点返回 200 且 `{available: false, error}`
- **AND** 不把失败结果写入缓存

### Requirement: 分析报告缓存 key 单一真源

系统 SHALL 以**单一**的纯函数 `vision.CacheKey(md5s []string)` 作为分析报告缓存 key 的唯一来源，适配前置阶段（`agent.visionThemeReport`）与按需只读端点 MUST 都经由它计算 key，确保两条路径对**同一参考图集**得到**完全一致**的 key，从而共享 `vision_reports` 缓存。

key 规则 SHALL 为：单图返回该图原始字节的裸 md5（与「新上传图片即时分析预热」按 md5 缓存对齐，使单图预热可被复用）；多图（≥2）返回 `md5("group:" + 有序逗号拼接的各图 md5)`，对参考图**顺序敏感**。

#### Scenario: 单图 key 等于裸 md5
- **WHEN** 参考图集只含一张图
- **THEN** `CacheKey` 返回该图原始字节的裸 md5
- **AND** 与上传预热写入的单图缓存命中同一行

#### Scenario: 多图 key 顺序敏感
- **WHEN** 参考图集含两张及以上图
- **THEN** `CacheKey` 返回 `md5("group:"+有序拼接)`
- **AND** 同一组图不同顺序得到不同 key

#### Scenario: 两条路径共享同一 key
- **WHEN** 图章按需端点与 `adapt_to_platform` 对相同有序参考图集计算 key
- **THEN** 两者得到完全一致的 key
- **AND** 一方算出的报告可被另一方缓存命中复用

### Requirement: 按需报告编辑回写与重新分析

系统 SHALL 允许就地编辑按需分析报告并写回缓存：`PUT /api/session/{id}/vision-report` 接受同一有序 `assetIds` 组与编辑后的 `report` 文本，按 `vision.CacheKey` 写入 `vision_reports`，使适配流程与后续展示复用编辑版。空报告 SHALL 返回 400，未注入 SHALL 返回 503。

系统 SHALL 支持对当前参考图组合重新分析：按需端点（`POST`）接受 `force` 标志，为真时 SHALL 绕过缓存、现场重跑视觉模型，并将新报告写回同一 key。

#### Scenario: 编辑回写复用
- **WHEN** 用户编辑某参考图组合的报告并保存
- **THEN** 系统按该组合的共享 key 写回 `vision_reports`
- **AND** 后续对该组合的展示与适配命中编辑版

#### Scenario: 空报告拒绝
- **WHEN** 写回请求的报告文本为空
- **THEN** 端点返回 400，不写缓存

#### Scenario: 强制重新分析绕过缓存
- **WHEN** 按需请求携带 `force=true`
- **THEN** 系统忽略缓存命中，现场重跑视觉模型并将新报告写回同一 key
