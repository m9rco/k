# Change: 视觉分析驱动的尺寸适配（先上传→再分析→后适配）

## Why

当前平台适配的 AI 重绘路径直接拿源图 + 服务端模板出图，模型对「这批图到底在宣发什么」没有显式认知，导致**主题不够突出、容易发散**。用户诉求：先用一个视觉模型读懂图里的宣发要素，产出一份分析报告，再把报告作为约束喂给图生图，让各尺寸产物**紧扣主题、不偏离**。

落到工程上拆成三个可观测阶段，每阶段都向 chat 反馈：
1. **选中图上传 COS**（md5 作为对象路径，全局去重，存库，传过即拼 URL）——为视觉模型提供可公网拉取的图片链接；
2. **grok-4-fast 视觉分析**（yunwu.ai，传入拼好的 URL 列表，流式输出宣发要素报告）；
3. **报告驱动的尺寸适配**（把报告作为主题约束注入图生图，产出各尺寸不偏离的适配图）。

## What Changes

- **新增 `reference-publishing` 能力**：把一组工作区图片发布到 COS，对象键为**图片内容 md5 + 扩展名**；新增**全局（跨会话）`md5→url` 持久缓存表**，命中则直接拼 URL、跳过上传。发布过程作为一个 chat 阶段反馈（开始/逐张完成/全部就绪）。
- **新增 `marketing-analysis` 能力**：调用 **yunwu.ai 的 `grok-4-fast`（视觉）**，传入 COS URL 列表，分析这批宣发图的核心主题/主体/卖点/风格/不可丢失要素，**流式**把报告输出到 chat 作为一个阶段。分析报告**按图片集（URL 列表的稳定指纹）缓存复用**——同一批图反复适配多个尺寸只分析一次。
- **修改 `platform-adaptation`**：AI 重绘路径在重绘前编排「发布→分析」两阶段，并把分析报告作为**主题锚定约束**注入图生图提示（与既有低发散 harness 协同），使各尺寸产物紧扣报告主题、不发散。**仅 AI 重绘路径**触发该流程；确定性裁剪快路径不变（裁剪不偏离主题，无需分析）。
- **修改 `provider-configuration`**：模型目录新增 `grok-4-fast`（视觉分析用），经 yunwu 公共网关；新增**视觉分析 HTTP 适配器**（手写，OpenAI 兼容 `/chat/completions` 多模态 content parts），因现有会话 chat 模型仅序列化纯文本 `Content`、不支持 `image_url`。

## Impact

- Affected specs:
  - `reference-publishing`（新增）
  - `marketing-analysis`（新增）
  - `platform-adaptation`（修改：AI 重绘路径前置发布+分析，报告注入；与未归档的 `harden-gpt-image-2-harness` 同改本 spec，需顺序应用，见 design）
  - `provider-configuration`（修改：新增 grok-4-fast 目录项 + 视觉适配器选型）
- Affected code:
  - `internal/cos/cos.go`（md5 命名约定 / 复用上传）
  - `internal/store/store.go`（新增 `cos_uploads` 表 + 读写方法）
  - `internal/agent/`（新增视觉分析一次性调用，仿 `optimize.go`；编排发布→分析→适配的阶段反馈）
  - 新增视觉分析适配器（手写 HTTP 多模态，OpenAI 兼容）
  - `internal/config/catalog.go`（grok-4-fast 目录项 + scene）
  - `internal/generation/adapt.go` / `prompt.go`（报告作为主题约束注入 AI 重绘）
  - `internal/agent/tools.go`（`adapt_to_platform` 编排接入发布+分析阶段）

## Dependency note

本 change 与 `harden-gpt-image-2-harness` 都修改 `platform-adaptation`。两者关注点不同（前者尺寸/收敛/低发散 harness，本 change 主题分析注入），但 spec delta 需在 `harden-gpt-image-2-harness` 归档后再校验合并，避免 MODIFIED 同名 Requirement 冲突。详见 `design.md`。
