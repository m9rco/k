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
系统 SHALL 以**流式增量**方式把分析报告输出到 web 对话区。分析完成后，报告块 SHALL **自动折叠**（collapsed = true），使对话区保持简洁；用户可手动展开查看完整报告。折叠标题 SHALL 显示「宣发分析」，展开状态 SHALL 显示「分析中」。

#### Scenario: 报告流式显示
- **WHEN** 视觉分析进行中
- **THEN** 分析报告逐段实时出现在 web 对话区
- **AND** 用户无需等待分析全部完成才看到输出

#### Scenario: 分析完成自动折叠
- **WHEN** 分析报告流式输出完毕（done = true）
- **THEN** 分析块立即收起（collapsed = true），仅显示「宣发分析」摘要行
- **AND** 用户点击该行可手动展开查看完整报告内容

#### Scenario: 分析阶段在适配前可见
- **WHEN** 完整适配流程执行
- **THEN** 对话区呈现：上传阶段 → 分析流式报告（完成后折叠）→ 适配开始
- **AND** 各阶段有清晰的阶段标识或分隔

### Requirement: 分析报告按图片集缓存复用
系统 SHALL 以**有序 URL 列表的内容指纹**为 key，在进程内缓存分析报告。同一批图（相同图片集指纹）的多次适配请求 SHALL 复用已有报告，**不重复调用视觉模型**。缓存命中时分析阶段 SHALL 向 chat 提示复用已有分析，跳过流式输出。进程重启后缓存失效，下次请求重新分析（可接受）。

#### Scenario: 同批图多尺寸复用报告
- **WHEN** 系统对相同图片集再次请求适配（图片集指纹一致）
- **THEN** 系统直接复用已缓存的分析报告，不再调用视觉模型
- **AND** chat 提示「复用已有主题分析」
- **AND** 适配继续使用该报告作为主题约束

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

