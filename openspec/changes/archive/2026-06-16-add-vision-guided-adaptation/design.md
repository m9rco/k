# Design: 视觉分析驱动的尺寸适配

## Context

用户诉求：适配各平台尺寸时「主题不够突出、容易发散」。解法是引入一个**先理解、再适配**的流程：

1. 选中图上传 COS（md5 作对象路径，全局去重，存库，命中直接拼 URL），chat 反馈为一个阶段；
2. 调 yunwu.ai 的 `grok-4-fast`（视觉），传入 URL 列表，流式输出宣发要素分析报告，chat 反馈为一个阶段；
3. 报告作为主题约束驱动图生图，产出各尺寸不偏离主题的适配图。

现状（已核实）：
- `internal/cos/cos.go` 的 `Uploader.Upload(ctx, name, data, contentType)` 已可用（视频流在用），但 name 由调用方给、**无 md5、无持久映射**。
- `internal/store` 用 `CREATE TABLE IF NOT EXISTS` 建表，无迁移框架；`AssetRecord` 无 md5/url 字段，有 `meta` JSON 列。
- 会话 chat 模型是手写的（`chatmodel.go`），请求体只序列化 `Content`（string）+ `tool_calls`，**不支持多模态 `image_url`**——视觉分析必须走独立 HTTP 客户端。
- yunwu 公共网关已配（`https://yunwu.ai/v1`，`COMMON_BASE_URL`/`YUNWU_*`），但目录无 `grok-4-fast`。
- chat 阶段反馈可用既有 `EventMessage`（流式增量）+ `EventToolCall`/`EventToolResult`。
- `adapt_to_platform` 工具 → `Generation.AdaptToPlatform`，按尺寸路由裁剪快路径 / AI 重绘（`Start` 异步任务）。

## Goals / Non-Goals

**Goals**
- AI 重绘前先产出主题报告并作为约束注入，使产物紧扣主题、不发散。
- COS 上传按 md5 全局去重，避免重复上传同一张图。
- 三阶段对 chat 可见（上传 / 分析流式 / 适配）。
- 分析报告按图片集缓存复用（同批图多尺寸只分析一次）。

**Non-Goals**
- 不改确定性裁剪快路径（比例一致时裁剪不偏离主题，无需分析）。
- 不引入厂商 SDK（视觉分析走手写 HTTP，沿用既有适配器约定）。
- 不把视觉能力塞进会话 chat 模型（独立客户端，避免污染主对话序列化）。
- 不做分析报告的人工编辑回路（本期报告为内部约束，不暴露为可编辑产物）。

## Decisions

### D1. COS 发布：md5 内容寻址 + 全局持久去重

**对象键**：`{basePath}/refs/{md5(hex)}{ext}`（ext 由 mime 推断）。md5 是内容指纹，同图同键，天然幂等。

**去重缓存表**（新建，全局跨会话）：
```sql
CREATE TABLE IF NOT EXISTS cos_uploads (
    md5         TEXT PRIMARY KEY,
    url         TEXT NOT NULL,
    content_type TEXT NOT NULL DEFAULT '',
    created_at  DATETIME NOT NULL
);
```
发布单图流程：算 md5 → 查表命中则直接返回 url（**不碰 COS**）→ 未命中则 `Upload` 后写表。表全局（不带 session_id），符合「内容相同即复用」。

**为何独立表而非 assets.meta**：去重是**跨会话、跨资产**的内容级映射，挂在某条 asset 上语义不符；独立表 O(1) 查 md5，也便于将来清理/统计。

**为何 md5 足够**：本工具仅小团队内部用，非对抗场景，md5 碰撞概率可忽略；选 md5 而非 sha256 是因为它够短、做对象键更干净（与用户「图片路径为图片md5即可」一致）。

### D2. 视觉分析：独立 HTTP 客户端 + grok-4-fast

会话 chat 模型不支持多模态，故新增**一次性视觉分析调用**（仿 `optimize.go` 的 tool-free 一次性 completion，但走独立 HTTP，因为要发 `image_url` content parts）：

- 端点：yunwu 公共网关 `{base}/chat/completions`，model=`grok-4-fast`。
- 请求：单条 user message，`content` 为多模态数组——一段固定分析指令（system 角色或拼在 user 文本前）+ 每个 URL 一个 `{type:"image_url", image_url:{url}}` part。
- **流式**：`stream:true`，逐 chunk 转成 `EventMessage` 增量推到 chat（同会话回复的流式范式），让用户实时看到分析报告生成。
- 输出：结构化但人类可读的报告（核心主题、主体/角色、核心卖点文案、风格基调、配色、**绝不可丢失的要素**、各尺寸适配注意点）。报告同时留存为内部字符串供阶段 3 注入。

**分析指令要点**（服务端固定、注入安全）：声明这是「游戏宣发素材主题分析」，要求**只描述图里确有的要素、不虚构**，产出"适配各尺寸时必须保留什么、主题是什么"的结论性约束。

### D3. 报告缓存：按图片集指纹复用

分析报告以**有序 URL 列表的指纹**（如 `md5(join(sortedURLs))`）为 key 缓存（进程内 LRU 或复用 `cos_uploads` 模式的小表，本期进程内即可，重启失效可接受）。同一批图适配多个尺寸/多次请求只分析一次。缓存命中时阶段 2 直接复用报告（chat 可提示「复用已有分析」）。

**为何不持久化报告**：报告是过程性约束、与模型版本/提示相关，持久化收益低；图片集级进程缓存已覆盖「同批图多尺寸」的主成本场景。

### D4. 编排：仅 AI 重绘路径前置「发布→分析」

`AdaptToPlatform` 现按尺寸路由：比例一致→裁剪快路径；否则→AI 重绘。**只有当本次请求存在至少一个 AI 重绘尺寸时**，才在重绘前编排：

```
[阶段1 发布] 选中图 → md5 去重发布 COS → chat: "已上传 N 张参考图"
[阶段2 分析] grok-4-fast(URLs) → 流式 chat 报告 → 缓存报告
[阶段3 适配] AI 重绘各尺寸，报告作为主题约束注入 prompt
```

纯裁剪请求**不触发**发布/分析（裁剪不偏离主题）。混合请求（部分裁剪、部分重绘）只为重绘部分跑分析。

**阶段如何反馈 chat**：阶段 1/2 在工具执行期间通过 orchestrator 的事件出口发 `EventMessage`（阶段 2 流式）；阶段 3 沿用既有异步任务 + SSE 进度。三者共享同一 turn 的 `trace_id`。

### D5. 报告注入图生图：主题锚定约束

阶段 3 把报告浓缩为**主题锚定段**，拼进既有低发散 harness 的 `[CONTEXT]`/`[PRESERVE]` 之间（见 `harden-gpt-image-2-harness` 的四段式）。报告为服务端生成的内部文本，经 `Sanitize` 后注入，不接受用户改写。注入位置使「这批图的主题是 X、必须保留 Y」先于具体修改指令，强化主题约束。

**与 harden-gpt-image-2-harness 的关系**：那个 change 已把 prompt 重构为 `[CONTEXT]/[PRESERVE]/[MODIFY]/[AVOID]` 四段并改了尺寸/收敛。本 change 在其基础上多注入一段「主题报告」。**实现顺序**：先归档 harden-gpt-image-2-harness，再实现本 change，注入点落在四段式的 PRESERVE 之后。

## Risks / Trade-offs

- **R1：延迟增加**——AI 重绘前多两步（上传 + 视觉分析）。缓解：md5 去重让重复图零上传；报告按图片集缓存让同批多尺寸只分析一次；仅 AI 重绘路径触发。
- **R2：grok-4-fast 不可用/超时**——分析失败不应阻断适配。降级：分析失败时记日志并**跳过报告注入**，回退到现有 harness 直接重绘（产物仍可用，只是少了主题强化），chat 提示「分析不可用，按默认适配」。
- **R3：COS 未配置**——发布不可用时整条视觉流程不可用。降级同 R2：跳过发布+分析，直接走现有重绘；不报错崩溃。
- **R4：md5 计算/大图开销**——md5 对图片字节是廉价操作；上传本就要读字节，无额外 IO。
- **R5：与 harden-gpt-image-2-harness 的 spec 冲突**——两 change 同改 `platform-adaptation`。通过实现/归档顺序串行化（见 D5），校验时确保 MODIFIED 的是归档后的最新 Requirement 文本。

## Migration

- 新增 `cos_uploads` 表（`CREATE TABLE IF NOT EXISTS`，与现有建表一致，无破坏）。
- 新增 `grok-4-fast` 目录项与视觉适配器，纯增量；未配置 yunwu 凭证时该能力优雅不可用。
- 既有适配产物与裁剪路径不受影响；仅 AI 重绘路径行为增强。

## Open Questions

- 分析报告的语言：默认中文（与产品语言一致）还是中英混排？倾向中文报告 + 关键约束英文短语（图生图模型对英文约束更敏感）。实现时在分析指令里固定。
- grok-4-fast 的确切 wire 名称与 yunwu 是否需特殊 header：实现前用一次真实调用确认（目录项 `Model` 字段可调）。
