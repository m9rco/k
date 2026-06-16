# marketing-analysis Specification

## Purpose
TBD - created by archiving change add-vision-guided-adaptation. Update Purpose after archive.
## Requirements
### Requirement: 视觉宣发要素分析
系统 SHALL 支持以一组**公网可拉取的图片 URL** 为输入，调用 `grok-4-fast`（yunwu.ai，OpenAI 兼容 `/chat/completions`，多模态 `image_url` content parts）对这批宣发素材进行主题分析，产出结构化的宣发要素报告。分析 SHALL 使用独立的视觉 HTTP 客户端（不复用仅序列化纯文本的会话 chat 模型）。分析指令 MUST 为服务端固定文案，声明「这是游戏宣发素材主题分析」，要求**只描述图里确有的要素、不虚构**，产出「适配各尺寸时必须保留什么、主题是什么」的结论性约束。报告格式涵盖：核心主题/IP/游戏名、主体角色/场景、核心卖点文案、视觉风格基调、主配色调、**绝不可丢失的要素**、各尺寸适配注意点。

#### Scenario: 分析多张参考图并产出报告
- **WHEN** 系统以一组参考图 URL 发起视觉分析
- **THEN** 系统经 grok-4-fast 视觉模型产出该批图的宣发要素报告
- **AND** 报告包含核心主题、必须保留的要素、适配注意点

#### Scenario: 分析指令防注入
- **WHEN** 报告提示词组装
- **THEN** 分析指令完全由服务端固定文案构成，不嵌入用户自由文本
- **AND** 图片内容作为 image_url 传入，而非作为可改写指令

#### Scenario: 视觉模型不可用降级
- **WHEN** grok-4-fast 调用失败（超时/凭证未配置/网络错误）
- **THEN** 系统返回明确的分析不可用信号
- **AND** 调用方跳过报告注入、回退到不含报告约束的标准适配流程
- **AND** chat 提示用户「主题分析不可用，按默认适配」

### Requirement: 分析报告流式 chat 输出
系统 SHALL 以**流式增量**方式把 grok-4-fast 的分析报告输出到 web 对话区，作为适配流程的第二阶段，使用户实时看到报告生成过程。分析阶段 SHALL 在「参考图发布」阶段完成后、图生图适配开始前执行。流式输出 SHALL 复用既有实时对话事件通道（`EventMessage` 增量推送）。分析完成后，报告留存为内部字符串供阶段 3 注入。

#### Scenario: 报告流式显示
- **WHEN** 视觉分析进行中
- **THEN** 分析报告逐段实时出现在 web 对话区
- **AND** 用户无需等待分析全部完成才看到输出

#### Scenario: 分析阶段在适配前可见
- **WHEN** 完整适配流程执行
- **THEN** 对话区呈现：上传阶段 → 分析流式报告 → 适配开始
- **AND** 各阶段有清晰的阶段标识或分隔

### Requirement: 分析报告按图片集缓存复用
系统 SHALL 以**有序 URL 列表的内容指纹**为 key，在进程内缓存分析报告。同一批图（相同图片集指纹）的多次适配请求 SHALL 复用已有报告，**不重复调用视觉模型**。缓存命中时分析阶段 SHALL 向 chat 提示复用已有分析，跳过流式输出。进程重启后缓存失效，下次请求重新分析（可接受）。

#### Scenario: 同批图多尺寸复用报告
- **WHEN** 系统对相同图片集再次请求适配（图片集指纹一致）
- **THEN** 系统直接复用已缓存的分析报告，不再调用视觉模型
- **AND** chat 提示「复用已有主题分析」
- **AND** 适配继续使用该报告作为主题约束

