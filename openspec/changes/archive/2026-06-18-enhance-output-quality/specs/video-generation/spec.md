# video-generation Specification Delta

## ADDED Requirements

### Requirement: 生视频 Prompt LLM 扩写
在组装最终图生视频 prompt 前，系统 SHALL 调用 `VIDEO_PROMPT_LLM`（默认 `claude-haiku-4-5-20251001`）对用户的简短动作描述做宣发导向的富化扩写，产出 2-3 句专业英文 prompt，覆盖：主体动作、镜头运动、节奏感与光影延续。扩写指令 SHALL 完全由服务端固定文案构成，用户动作描述以结构化 slot 传入（注入防护）。当有 `themeReport` 时，系统 SHALL 将其精简版（取核心主题/IP/必须保留要素）附加到扩写上下文，使视频内容紧扣宣发主题。

当 LLM 调用失败或超时（≤ 5s），系统 SHALL 降级回使用原始动作描述，不阻断视频生成任务。扩写结果 SHALL 缓存在任务 Params，retry 时复用，不重复调用 LLM。

#### Scenario: 短动作描述被 LLM 富化
- **WHEN** 用户输入「让角色走起来」
- **THEN** 系统在组装 prompt 前调用 LLM 扩写，产出包含动作细节、镜头运动与光影描述的英文 prompt
- **AND** 最终传入视频供应商的 prompt 为扩写后的版本

#### Scenario: 有 themeReport 时注入主题约束
- **WHEN** 视频任务带有非空 themeReport（来自上游分析缓存）
- **THEN** 扩写 LLM 接收 themeReport 精简版作为附加上下文
- **AND** 扩写结果要求视频内容与宣发主题一致（不虚构主题外元素）

#### Scenario: LLM 不可用时降级
- **WHEN** LLM 调用超时（> 5s）或返回错误
- **THEN** 系统以原始动作描述继续视频生成，不向用户暴露 LLM 失败
- **AND** 降级行为记录到日志

#### Scenario: retry 复用扩写结果
- **WHEN** 视频任务因源图上传失败触发 retry
- **THEN** 系统复用首次扩写的 prompt，不重复调用 LLM

### Requirement: 视频源图质检（代理质检，Phase 1）
系统 SHALL 在生视频任务提交供应商前，对源图（`SourceAssetID` 对应的图片字节）执行视觉质检，产出 `VideoQualitySignal`（`subject_score`、`appeal_score`、`hints`）。此阶段为**尽力而为**：质检失败或不可用时 SHALL NOT 阻断视频任务，仅将信号记录到资产元数据并将 hints 追加到扩写 LLM 的上下文（使视频 prompt 尝试弥补源图的构图/吸引力不足）。

#### Scenario: 源图质检信号注入 prompt 扩写
- **WHEN** 源图质检返回非空 hints（如「主体偏左，建议构图居中」）
- **THEN** 系统将 hints 追加到 LLM 扩写上下文，使扩写结果包含弥补该缺陷的镜头运动建议（如「缓慢推进使主体居中」）

#### Scenario: 质检器未配置时跳过
- **WHEN** VideoQualityChecker 未配置
- **THEN** 系统跳过源图质检，正常进行视频生成

#### Scenario: 质检失败不阻断视频任务
- **WHEN** 源图质检调用超时或返回错误
- **THEN** 系统记录日志，继续发起视频生成
- **AND** 不向用户报告质检失败
