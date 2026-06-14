## Context
模型选择当前是全局的:
- chat:`Orchestrator.model *chatModel` 在 `NewOrchestrator` 时按 `cfg.ChatPrimary`/`ChatTest` 构造一次,`Handle` 的每一轮都复用同一实例(`react.NewAgent{ToolCallingModel: o.model}`)。
- 生图/文生图/视频:provider 在 `main.go` 启动时由工厂 `generation.NewProvider`/`video.NewProvider` 构造,注入各 Service,所有会话共享。

会话隔离已有基础:`Handle` 按 sessionID 串行(`sessionTurnLock`)、可取消(`o.cancels[sessionID]`);异步任务 `Service.Start` 即时捕获参数。存储层有 `preferences(session_id,key,value)` 表,天然适合存会话级选择。

已确认:四场景全部可切、目录仅显示已配置可用、持久化到会话、icon 用内置品牌 SVG。约束(project.md):小团队内部、无鉴权("用户"=session)、二进制轻量、模型 id 不暴露给用户改写(只能从服务端给的可选集里选)。

## Goals / Non-Goals
- Goals
  - 每个会话可为四场景各选一个模型,互不影响,持久化,刷新/重连仍在。
  - 新选择只影响"切换后启动的新轮/新任务";进行中的不受影响。
  - 切 chat 模型后立即由新模型做简短自我介绍。
  - 可选模型集由服务端按凭证可用性管控(用户不能选未配置/不存在的模型)。
- Non-Goals
  - 不做账户体系、跨设备同步;不改协议实现;不做自动路由;不做运行中任务热替换。

## Decisions

### D1: 模型目录 + 可用性过滤(服务端权威)
在 `internal/config` 定义静态模型目录:每项 `{ID, DisplayName, Scene, Vendor, IconKey}`,Scene ∈ {chat, image, text_to_image, video}。`Available(cfg)` 依据各场景凭证(api_key/base_url 是否解析出非空)过滤出可用项。前端只能从该集合选,提交的 model id 必须在可用集内,否则 API 拒绝(用户不可注入任意模型——保留"服务端管控可选集"的安全语义)。IconKey 映射到前端内置品牌 SVG(openai/gemini/claude/qwen/doubao/wan/veo)。

### D2: 会话级选择存储 —— 复用 preferences 表
key 形如 `model.chat` / `model.image` / `model.text_to_image` / `model.video`,value 为 model id。读:`GetSessionModelOverrides(sessionID) map[scene]modelID`;写:`SetSessionModel(sessionID, scene, modelID)`。无 schema 迁移(preferences 已存在)。未设置的 scene 回退服务端默认。

### D3: chat —— 每轮按会话解析 model(关键改动)
把 `Handle` 里 `ToolCallingModel: o.model` 改为 `resolveChatModel(sessionID)`:
- 读会话 `model.chat` 覆盖 → 在 config 的 chat 模型目录里找到对应 `ModelConfig`(base_url/api_key 走已合入的三层回退)→ `newChatModel(mc)`。
- 无覆盖 → 用现有默认(`cfg.ChatPrimary`/`ChatTest`)。
- chatModel 构造极轻(只持 config + http.Client),每轮新建无性能顾虑,且天然实现"进行中轮用旧 model、新轮用新 model"(各轮持有自己的实例,`o.model` 全局单例可保留作默认或移除)。

替代方案:维护 `map[sessionID]*chatModel` 缓存。否决——构造廉价、缓存反而引入失效/并发复杂度,违背简单优先。

### D4: 生图/文生图/视频 —— Start 时按会话解析 provider
现状 Service 持有单一 provider。改为:Service 暴露"按 provider 配置生成 Generator/Provider"的能力,或在 `Start(params)` 时接收一个 `ModelOverride`(scene 对应的 model id),由调用方(agent 工具)从会话覆盖解析出 `ImageProviderConfig` 并传入,Service 用它即时构造 provider 执行该 task。task 一旦 Start,其 provider 固化 → 进行中不受影响。
- 倾向:在 `GenerateParams`/video `Params` 增可选 `ProviderConfig`(零值时用 Service 默认),最小侵入、与"按 task 固化"语义吻合。

### D5: 切换后自我介绍(仅 chat)
`POST /api/session/{id}/models` 当 scene==chat 且切换成功后,触发一次**系统发起的轮**:服务端用新 model 以固定 system+引导(如"用一句话中文介绍你自己并说明你能帮用户做的宣发素材操作")生成,经 hub 走正常 turn_start/增量/turn_end 下发。
- 不写坏滑动窗口:作为独立轮处理;自我介绍可作为一条 assistant 消息进入窗口(让用户看到上下文连续),或标记为不计入摘要——倾向计入(就是一条正常助手发言)。
- 切其他场景:仅持久化 + 返回成功,无自我介绍。
- 自我介绍与正在进行的对话轮的关系:复用 `sessionTurnLock`,排在当前轮之后,避免交叠。

### D6: 前端弹窗
- 入口:工作区/顶栏加一个配置 icon(齿轮)。
- 弹窗:四个分区(逻辑推理/图生图/文生图/图生视频),每区列出该场景可用模型卡片(品牌 SVG + 名称 + 当前选中态),点选即 `POST` 并乐观更新;chat 切换后用户在对话流看到自我介绍。
- 目录与当前选择由 `GET /api/session/{id}/models` 提供;遵循 CLAUDE.md 的设计令牌(zinc 调色、subtle border、200ms 过渡、shadcn 风格 Dialog)。
- 品牌 SVG 内置在 `web/src`(各厂商 logo),按 IconKey 取用。

## Risks / Trade-offs
- 与已归档"用户不可切换模型"规格冲突 → 本变更 MODIFIED 该 requirement 为"用户可在服务端管控的可选集内按会话切换";保留"不能注入任意模型 id"的注入防护语义。
- 自我介绍占用一轮、与用户输入竞争锁 → 用 sessionTurnLock 串行,且自我介绍轮可被新用户输入打断(沿用现有 cancel 机制)。
- 厂商品牌 SVG 属第三方商标 → 仅内部工具使用、不对外分发;以最小、可替换的方式内置(集中一个 icon 映射,便于撤换)。
- 生图等 Service 改造面 → 用可选 ProviderConfig 注入,零值回退默认,既有调用点不破坏。

## Migration Plan
1. 模型目录 + 可用性过滤(config),纯新增。
2. preferences 读写 helper + models API(GET/POST),不接前端也可单测。
3. chat 每轮按会话解析 model;默认路径行为不变(回退默认)。
4. 生图/文生图/视频 Start 接受可选 ProviderConfig。
5. 自我介绍触发(chat 切换)。
6. 前端入口 + 弹窗 + 品牌 SVG + store/api。
7. `go test ./...`、`go vet`、前端 tsc/build;`openspec validate --strict`。
回滚:纯增量 + 回退默认;`git revert` 即恢复全局行为,未选择的会话不受影响。

## Open Questions
- 自我介绍文案是否要可配置?默认用固定中文模板,先不做可配置。
- 弹窗入口放顶栏还是工作区工具条?倾向顶栏靠近压缩开关处;apply 时按前端布局定。
