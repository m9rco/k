## ADDED Requirements

### Requirement: 新上传图片即时分析预热
系统 SHALL 在每张**新上传**图片落库后，**异步、尽力而为**地对其执行视觉宣发要素分析并按内容 md5 缓存结论，使后续适配/改图无需再次分析即可直接复用。预热 SHALL：发布该图到 COS（按 md5 去重，复用 `reference-publishing`）→ 调 `grok-4-fast` 视觉分析（复用 `marketing-analysis` 既有分析能力）→ 将报告写入按 md5 键的全局报告缓存（`vision_reports`）。预热 SHALL NOT 阻塞上传 HTTP 响应；其失败 SHALL 仅记录日志、不影响上传成功。当某图内容 md5 已存在缓存时 SHALL 跳过分析（零成本）。当 COS 或视觉模型未配置时 SHALL 静默跳过预热，不报错。

#### Scenario: 上传后异步预热分析
- **WHEN** 用户上传一张此前未分析过（md5 未命中缓存）的新图片
- **THEN** 系统在上传成功响应后异步发布该图并经 grok-4-fast 分析，将报告按 md5 写入全局缓存
- **AND** 上传响应不被分析过程阻塞

#### Scenario: 后续适配命中预热结论
- **WHEN** 用户随后对该图（相同 md5）发起 AI 适配
- **THEN** 系统直接复用预热阶段缓存的分析报告，不再调用视觉模型

#### Scenario: 重复内容跳过预热
- **WHEN** 上传图片的内容 md5 已存在于报告缓存
- **THEN** 系统跳过分析，不重复调用视觉模型

#### Scenario: 预热不可用静默降级
- **WHEN** COS 未配置或 grok-4-fast 不可用
- **THEN** 系统跳过上传预热，不向用户报错
- **AND** 上传正常成功，后续适配按既有按需分析/降级流程进行

#### Scenario: 预热失败不影响上传
- **WHEN** 上传预热的发布或分析步骤失败
- **THEN** 系统仅记录日志，上传本身仍判定为成功
