## Context
四类能力当前各自硬编码供应商形态:
- 会话理解(`internal/agent/chatmodel.go`):已抽象为按 `ModelConfig.Provider` 选 `anthropic`/`openai` 两条 HTTP 路径,真·流式 + 思考增量。
- 生图(`internal/generation/http_provider.go`):固定 OpenAI `images/{generations,edits}`,b64_json 响应;`FailoverGenerator` 主备切换。
- 生视频(`internal/video/provider.go`):固定 happyhorse(DashScope 异步 task,submit→poll→fetch)。
- 文生图:无独立能力(生图 service 的 `primaryID==""` 路径可无源图,但无工具/入口,且 wan/qwen 形态不同)。

已合入 `provider-configuration`:每后端可经 `<PREFIX>_PROVIDER/_BASE_URL/_API_KEY` 三层回退配置。本次在其上做「按配置选适配器」。所有模型经 yunwu 代理。约束(project.md):不拉厂商 SDK,二进制轻量;模型服务端配置、用户不可选。

## Goals / Non-Goals
- Goals
  - 新增模型经**改配置**即可切换,默认行为与现状一致。
  - 形态不同的厂商各有一个**手写轻量 HTTP 适配器**,实现统一接口;选型由配置键驱动。
  - 文生图作为新能力接入(工具 + 入口 + 适配器),复用异步任务管线。
  - 每个适配器有 httptest 表驱动单测,失效/降级路径有覆盖。
- Non-Goals
  - 不引官方 SDK;不做质量/成本动态路由;不做 text-to-video、流式生图。

## Decisions

### D1: provider 选型键 —— 复用 Provider 字段 + 可选 Format
会话模型已用 `ModelConfig.Provider`(anthropic/openai)选路径。生图/生视频沿用同一思路:在 `ImageProviderConfig` 上用 `Provider` 作为适配器选择键。取值约定:

| 能力 | Provider 取值 | 适配器 |
|------|--------------|--------|
| 生图 | `openai`(默认) | 现有 `images/{generations,edits}` b64 |
| 生图 | `gemini` | Gemini 适配器(见 D3) |
| 文生图 | `dashscope` | wan/qwen 异步 task 适配器 |
| 生视频 | `happyhorse`(默认) | 现有 DashScope 适配器 |
| 生视频 | `veo` | Veo 适配器(见 D4) |

工厂函数 `func newImageProvider(cfg) Provider` / `newVideoProvider(cfg) Provider` 按 `cfg.Provider` switch,default 回退现有实现(向后兼容)。**不为 Provider 取值新增枚举类型**,用字符串常量 + switch,符合「boring/proven」。

### D2: 逻辑推理(会话理解)—— 多为纯配置,补字段解析
- `claude-haiku-4-5-20251001`、`claude-sonnet-4-6`:走现有 `anthropic` 分支,改 `CHAT_PRIMARY_MODEL` 即可。
- `gpt-5.4`:走 `openai` 分支(chat/completions),纯配置。
- `doubao-seed-2-0-mini-260428`:走 `openai` 分支;需确认 reasoning 字段名(火山/doubao 经 yunwu 可能用 `reasoning_content` 或不同 key)。apply 阶段对文档确认;若字段不同,在 openAI 响应解析处做兼容(已有 `reasoning_content` 解析,新增别名即可)。
- 不新增协议分支。规格「硬编码」改为「配置驱动、用户不可选」。

### D3: Gemini 图生图适配器
两种可能形态,apply 时按 yunwu 文档二选一:
- **(A) OpenAI 兼容**:yunwu 把 gemini-image 暴露为 `/v1/images/generations|edits`(或 chat completions 带图)。→ 复用现有 `HTTPProvider`,仅 `Provider=openai` + 配 model/base_url,**零新代码**。
- **(B) 原生 generateContent**:`POST /v1beta/models/{model}:generateContent`,请求 `contents[].parts[].inline_data{mime_type,data(base64)}`,响应同结构取 inline_data。→ 新写 `geminiProvider` 实现 `Provider` 接口,解析 base64 图。多图参考放入多个 parts。

设计先把工厂选型与接口定下;具体走 A 还是 B 在 apply 第一步用一次真实请求探明(任务 2.1)。

### D4: Veo 图生视频适配器
异步 task 形态(与 happyhorse 类似但字段不同):submit(带源图 URL + 提示)→ 返回 task id → poll 直到终态 → 取结果视频 URL → fetch。Veo 经 yunwu 的请求体/轮询路径/结果字段在 apply 按文档落定。复用现有 `video.Service` 的 COS 上传源图 + 异步任务 + 进度反馈管线;仅 provider 实现替换。`Provider=veo` 选型。

### D5: 文生图(text-to-image)新能力
- 适配器:wan/qwen 经 yunwu 多为 DashScope 异步 task(`image-synthesis`),submit→poll→fetch,形态接近 video 适配器。
- 接入:新增 agent 工具 `generate_image_from_text`(纯文本,无源图),纳入工具白名单;工作区新增「文生图」显式入口。复用异步任务/进度/回填管线(与生图/生视频一致)。
- 与现有「图生图」区分:图生图需源图驱动调色板/尺寸继承;文生图无源图,尺寸由用户/默认决定。
- 放置:新建 `internal/texttoimage/` 包(与 generation 对称),或在 generation 内加 `text` 子路径。倾向独立包以隔离异步 task 形态。apply 时定。

### D6: 配置与装配
- `.env.example` 增:各能力的 `*_PROVIDER` 取值说明 + 新模型样例(gemini/wan/qwen/veo/doubao/gpt-5.4)。
- `main.go`:`newImageProvider`/`newVideoProvider`/`newTextToImageProvider` 工厂按 cfg 装配;未配新 provider 时与现状完全一致。

## Risks / Trade-offs
- 文档不可直连(apifox SPA + 网络限制)→ 各适配器的精确请求/响应字段在 apply 阶段用真实请求/文档逐个核实;设计层只锁接口与选型,不锁字节级 schema。Mitigation:每适配器先写 httptest 契约测试,再对真实端点验证。
- Gemini A/B 形态未定 → 工厂+接口先行,实现延后探明,不阻塞其余适配器并行。
- doubao reasoning 字段差异 → 解析处做多 key 兼容,缺失时降级为无思考(不报错)。
- 适配器增多 → 用统一 `Provider` 接口 + 工厂 switch 收敛,避免散落 if;每个适配器单一职责、可独立测。

## Migration Plan
1. 先做生图/生视频工厂选型层(default 回退现有实现),跑现有测试确认零回归。
2. 逐适配器:Gemini(探形态)、Veo、wan/qwen 文生图,各自 httptest 契约测 + 真实端点验证。
3. chat 新模型:配置切换 + doubao 字段兼容 + 验证。
4. 文生图工具/入口接入。
5. 全量 `go test ./...` + `go vet`;`openspec validate --strict`。
回滚:纯增量 + 工厂 default 回退,`git revert` 即恢复;未改配置的部署不受影响。

## Open Questions
- Gemini 经 yunwu 是 OpenAI 兼容(A)还是原生 generateContent(B)?→ apply 任务 2.1 探明。
- wan/qwen 是否同一 DashScope `image-synthesis` 形态、参数是否一致?→ apply 任务 4.1 确认。
- Veo 经 yunwu 的 submit/poll 路径与结果字段?→ apply 任务 3.1 确认。
- doubao reasoning 字段名?→ apply 任务 1.1 确认。
(以上均为 apply 阶段对文档/端点的字节级确认,不影响本提案的接口与选型架构。)
