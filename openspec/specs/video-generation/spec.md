# video-generation Specification

## Purpose
TBD - created by archiving change expand-studio-capabilities. Update Purpose after archive.
## Requirements
### Requirement: 单图加动作描述生成视频
系统 SHALL 支持用户基于工作区中的**单张图片**加一段**动作描述**（如"让角色走起来""镜头缓慢推进"）生成短视频，经服务端硬编码的图生视频供应商产出，产物作为视频资产回填工作区。生成 SHALL 以异步任务执行，进度经实时通道反馈，且工作区 SHALL 在任务一经创建即出现占位与流式进度（与生图任务一致的即时反馈），而非等本轮对话结束才显现。用户的动作描述文本 SHALL 经注入防护后由服务端模板组装为最终提示。

#### Scenario: 图生视频
- **WHEN** 用户选择一张图并要求"让画面里的角色走起来"
- **THEN** 系统以该图为输入、动作描述为引导调用图生视频供应商
- **AND** 产物作为视频资产（视频类型）回填工作区，可预览/下载

#### Scenario: 进度反馈
- **WHEN** 图生视频任务进行中
- **THEN** 工作区为该产物展示占位与阶段化进度
- **AND** 占位在任务创建后即出现、本轮对话结束前即可见
- **AND** 任务进行期间用户仍可进行其他操作

#### Scenario: 从工作区显式入口发起
- **WHEN** 用户通过放大面板或右键菜单的「生成视频」入口、对某张图填写动作描述并确认
- **THEN** 系统以该图为源发起图生视频，无需用户依赖特定对话话术
- **AND** 工作区即时出现该任务的占位与进度

#### Scenario: 供应商未配置时降级
- **WHEN** 图生视频供应商未配置或不可用
- **THEN** 系统礼貌告知该能力暂不可用，而非崩溃

#### Scenario: 动作描述注入防护
- **WHEN** 动作描述中包含试图改写系统指令的内容
- **THEN** 系统通过结构化 slot 承接用户输入并由服务端模板组装最终提示
- **AND** 用户文本不被直接拼接为可改写系统行为的提示

### Requirement: 生视频意图纳入白名单
系统 SHALL 将「生视频」从预留意图激活为可执行意图，纳入 Agent 的工具白名单。

#### Scenario: 命中生视频意图
- **WHEN** 用户请求让某张图动起来
- **THEN** Agent 识别为生视频意图并分发到图生视频工具
- **AND** 工具调用过程以事件形式可见于前端

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

