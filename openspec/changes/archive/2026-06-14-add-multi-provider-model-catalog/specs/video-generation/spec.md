## ADDED Requirements

### Requirement: 多供应商图生视频适配器
系统 SHALL 支持图生视频能力在多个厂商模型间经配置切换,包括既有供应商与 Google Veo(`veo_3_1_fast_components_vip`、`veo_3_1_components_vip`)。Veo 以手写 HTTP 适配器对接其异步任务形态(提交任务 → 轮询状态 → 拉取结果视频),实现与既有图生视频相同的统一接口,由配置的 `Provider` 键选型。切换供应商 SHALL 不要求改动工作区占位/进度反馈/COS 源图发布等既有异步管线。当所选供应商未配置或不可用时,系统 SHALL 礼貌降级告知该能力暂不可用而非崩溃。

#### Scenario: 经配置使用 Veo 生视频
- **WHEN** 生视频后端配置 `VIDEO_PROVIDER=veo` 及其 model/base_url/api_key
- **THEN** 系统经 Veo 适配器以源图 + 动作描述提交任务、轮询至完成并拉取视频
- **AND** 产物作为视频资产回填工作区,进度经实时通道反馈

#### Scenario: 切换供应商复用既有管线
- **WHEN** 在既有供应商与 Veo 之间切换配置
- **THEN** 工作区即时占位、阶段化进度、COS 源图发布等行为保持一致

#### Scenario: Veo 未配置降级
- **WHEN** 选用 Veo 但其凭证/模型未配置
- **THEN** 系统报告该能力暂不可用而非崩溃
