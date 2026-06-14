# Change: 基于用户会话的模型动态切换

## Why
当前会话理解模型是**全局单例**(`Orchestrator.model`,启动时按 `cfg.ChatPrimary`/`ChatTest` 构造,所有会话共享),生图/生视频 provider 也在启动时构造一次经 service 共享。规格明确「模型服务端配置驱动:用户不可选择或切换」。

业务希望让**每个用户(会话)能自行选择各场景使用的模型**,互不影响:用户 A 切到 claude-sonnet-4-6 不应改变用户 B 的模型。需要把"模型选择"从全局降为**会话级状态**,并提供一个前端配置入口让用户按场景挑选。已确认:四场景(逻辑推理/图生图/文生图/图生视频)全部可切、目录仅显示已配置可用的模型、选择持久化到会话、模型 icon 用内置厂商品牌 SVG。

## What Changes
- **会话级模型覆盖(core)**:新增一份**模型目录**(每个模型含 id、显示名、所属场景、厂商、厂商 icon key),由服务端按"凭证是否已配置"过滤为可用列表。每个会话可为四个场景分别记录一个模型覆盖;未覆盖则回退服务端默认配置。选择**持久化到现有 `preferences` 表**(session_id + key/value,无需迁移)。
- **会话级模型解析**:`Orchestrator.Handle` 启动一轮时,按会话的 chat 覆盖**即时构造该轮的 chatModel**(而非用全局单例);生图/文生图/视频在 task 启动(Start)时按会话覆盖解析对应 provider。**进行中的任务不受影响**:chat 轮按会话串行且各自持有自己的 model 实例;异步任务在 Start 时已固化所用 provider。新选择只对"切换之后启动的新轮/新任务"生效。
- **切换后自我介绍**:切换**逻辑推理(chat)模型**后,服务端立即用新模型发一条简短自我介绍,经现有流式通道下发(turn_start/增量/turn_end),不占用用户输入、不写脏滑动窗口的正常对话语义(作为系统触发的一轮)。切换生图/文生图/视频模型则静默生效(这些模型不对话,无自我介绍语义)。
- **API**:新增 `GET /api/session/{id}/models`(返回按场景分组的可用模型目录 + 当前会话选择)与 `POST /api/session/{id}/models`(设置某场景的模型覆盖,触发持久化;若为 chat 场景则触发自我介绍)。
- **前端**:新增一个配置 icon(齿轮/滑块)入口,点击弹出模型选择弹窗,按四场景分区、每个模型卡片展示厂商品牌 SVG + 名称,当前选中高亮;切换即调用 API 并即时反馈;chat 切换后在对话流里看到新模型的自我介绍。内置各厂商品牌 SVG(OpenAI/Gemini/Claude/Qwen/Doubao/Wan/Veo)。

不在范围(Non-Goals):
- 不做跨会话/跨设备的用户账户体系(项目无鉴权;"用户"= 现有 session)。
- 不改各 provider 的协议实现(复用已合入的适配器与工厂)。
- 不引入模型质量/成本自动路由;仅用户手动选择。
- 不改变"进行中任务"的语义(本就独立);不做运行中任务的热替换。

## Impact
- Affected specs:
  - `provider-configuration`(ADDED:模型目录与可用性过滤)
  - `conversation-orchestration`(MODIFIED:从"用户不可切换"改为"用户可按会话切换,服务端仍管控可选集";ADDED:切换后自我介绍)
  - `frontend-experience`(ADDED:模型选择入口与弹窗)
- Affected code:
  - `internal/config`(模型目录定义 + 可用性判定)
  - `internal/agent/agent.go`(会话级 chat model 解析 + 自我介绍触发)
  - `internal/generation`、`internal/video`(Service 支持按会话覆盖解析 provider)
  - `internal/session` 或新 `internal/usermodel`(会话选择的读写,基于 preferences 表)+ 路由
  - `internal/store`(preferences 读写 helper,如尚缺)
  - `web/src`(配置入口、弹窗组件、品牌 SVG、store/api)
- 兼容:未做任何选择的会话行为与现状完全一致(回退服务端默认)。
