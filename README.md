# Game Asset Studio

对话式游戏宣发素材生成系统。在一个对话窗口里完成 **换角色 / 换背景 / 换文案 → 切渠道尺寸 → 预览 / 重试 → 下载** 的闭环，并延伸到文生图、图生视频、图层精修、物料爬取、网页搜索等能力。后端 Go 单二进制，前端用 `embed` 嵌入，开箱即跑。

> 仅供小团队内部使用，无鉴权。密钥通过环境变量注入，**不硬编码**。所有需要密钥的能力在密钥缺失时**优雅降级**（工具移出白名单 / 明确报"暂未配置"），核心的上传/裁剪/下载在零密钥下即可跑。

## 架构

> 📐 深度架构与技术细节见 [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md)：系统全景、端到端数据流、各子系统深潜、关键工程卡点与决策复盘、垂类护城河（含 mermaid 架构图/流程图）。

```
cmd/server          单二进制入口（HTTP + 优雅退出 + embed 前端 + 全部能力装配）
internal/config     集中配置（env 三级回退 + 渠道尺寸/模型目录 JSON）
internal/store      SQLite 持久化（sessions / assets / tasks / vision_reports …）
internal/session    匿名 session（浏览器指纹生成、复用、隔离）
internal/transport  实时层：WebSocket（对话）+ SSE（任务进度，含 Last-Event-ID 重连）
internal/agent      Eino ReAct 编排：意图白名单分发 + context 滑动窗口 + 工具注册 + submit_plan 串行编排
internal/generation 生图：主/备 failover、颜色适配、prompt 注入防护、质检重试、outpaint 收敛、抠图、异步任务
internal/vision     视觉：营销主题分析、质检评审、像素预筛、主体定位、选区/点/多边形识别
internal/crop       纯图像裁剪（cover/contain、锚点适配），数据驱动渠道尺寸
internal/layering   图层精修：大模型分层 + 掩码/框抠图到透明层 → 固定画布拼接
internal/composite  拼接画布产物持久化（浏览器合成 PNG 落库，确定性、无 AI）
internal/textoverlay 文字叠加：服务端字体光栅渲染 CTA/角标/定档大字，逐字检测缺字
internal/copywriting 宣发文案生成（结构化文案，走对话模型）
internal/video      图生视频（happyhorse / veo，异步，经 COS 发布源图）
internal/crawl      游戏物料爬取（可插拔源，未配置时降级）
internal/websearch  网页搜索（DDG 文本 + Bing 图片，无需密钥）
internal/cos        腾讯云 COS 上传（把本地图发布为公网 URL，供图生视频/openai 视觉用）
internal/imageopt   图片压缩/优化（无损优先）
internal/workspace  工作区：列资产/任务、上传、部分重试、选区识别、按需分析
internal/download   单图下载 + 批量 zip 打包（跳过无效项并报告）
internal/usermodel  per-session 模型选择状态
internal/log        结构化诊断日志（JSON，按 trace_id/session_id 串链路）
internal/id         短 id 生成
web/                前端单页（原生 ES 模块，无构建步骤，go:embed）
configs/channels.json   渠道尺寸目录（渠道 → 素材类型 → 尺寸，三级数据驱动）
configs/platforms.json  兼容保留的两级平台预设（缺 channels.json 时回退投影）
configs/fonts/          文字叠加 CJK 主字体（Noto Sans SC，OFL 可商用，已入库）
```

## 运行

```bash
# 1. 配置密钥。最小可跑：仅前端 + 上传/裁剪/下载，无需任何密钥。
#    一把 COMMON_* 网关密钥即可点亮对话/生图/视觉等大部分能力。
export COMMON_API_KEY=sk-...                 # 共享网关密钥（yunwu/common）
export COMMON_BASE_URL=https://yunwu.ai/v1   # 共享网关端点（默认已是此值）

# 2. 启动
go run ./cmd/server
# 或构建单二进制
go build -o asset-studio ./cmd/server && ./asset-studio

# 3. 打开 http://localhost:8080
```

## 环境变量

配置采用**三级回退**：每个后端优先读自己的 `<PREFIX>_PROVIDER/_BASE_URL/_API_KEY`，缺失则回退到共享的 `COMMON_*`（旧名 `YUNWU_*` 仍作别名），再回退到内置默认。因此**一把 `COMMON_API_KEY` 即可驱动全部模型后端**；想把某个模型换供应商，只需设该后端自己的几个变量。`<PREFIX>_MODEL` 无 common 层（各后端有各自默认 id）。

### 服务 / 存储

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ADDR` | `:8080` | HTTP 监听地址 |
| `DB_PATH` | `data/asset-studio.db` | SQLite 文件路径 |
| `ASSET_DIR` | `data/assets` | 生成/裁剪产物存储目录 |
| `ASSET_RETENTION_HOURS` | `0` | 产物保留时长（0 = 永久保留） |
| `ASSET_PUBLIC_BASE` | — | 产物公网基址（图生视频等需要） |
| `CONFIG_CHANNELS` | `configs/channels.json` | 三级渠道尺寸目录 |
| `CONFIG_PLATFORMS` | `configs/platforms.json` | 兼容保留的两级平台预设 |
| `CONTEXT_TOKEN_BUDGET` | `8000` | 对话上下文滑动窗口 token 预算 |
| `OVERLAY_FONT_PATH` | `configs/fonts/NotoSansSC-Regular.otf` | 文字叠加 CJK 主字体路径（见「文字叠加字体」） |
| `LOG_FILE` | `data/logs/app.log` | JSON 日志路径（空 = 仅 stderr） |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `LOG_MIRROR_STDERR` | `false` | 额外把日志镜像到 stderr（本地开发用） |

### 模型后端

| 变量 | 默认值 | 说明 |
|------|--------|------|
| **共享网关** | | |
| `COMMON_API_KEY` / `COMMON_BASE_URL` / `COMMON_PROVIDER` | — / `https://yunwu.ai/v1` / — | 所有后端的共享回退（别名 `YUNWU_API_KEY`/`YUNWU_BASE_URL`） |
| **对话（逻辑推理 / 主 agent）** | | |
| `CHAT_PRIMARY_PROVIDER` | `openai` | `openai`→chat/completions，`anthropic`→Messages API，`taiji`… |
| `CHAT_PRIMARY_MODEL` | `deepseek-v4-flash` | 主对话模型 id |
| `USE_TEST_MODEL` | `false` | 切换到 `CHAT_TEST_*` 测试模型 |
| `CHAT_TEST_MODEL` | `claude-sonnet-4-5-20250929` | 测试模型 id（`CHAT_TEST_API_KEY` 别名 `DEEPSEEK_API_KEY`） |
| **图生图（主 + 备 failover）** | | |
| `IMAGE_PRIMARY_PROVIDER` / `IMAGE_PRIMARY_MODEL` | `openai` / `gpt-image-2` | 主生图（`gemini`→`gemini-3-pro-image` 等） |
| `IMAGE_BACKUP_PROVIDER` / `IMAGE_BACKUP_MODEL` | `openai` / `gpt-image-2` | 备用生图 |
| `IMAGE_OUTPAINT_MODEL` | `gemini-3.1-flash-image` | 极端比例适配的 outpaint 收敛模型（缺密钥回退到留白补边） |
| **文生图（wan/qwen，异步）** | | |
| `TEXT_TO_IMAGE_PROVIDER` / `TEXT_TO_IMAGE_MODEL` | `dashscope` / — | 缺密钥时该能力禁用、工具移出白名单 |
| **视觉 / 质检** | | |
| `VISION_PROVIDER` / `VISION_MODEL` | `gemini` / `gemini-flash-latest` | 营销分析（gemini inline 免 COS；`openai` 走 image_url 需 COS） |
| `LAYER_SPLIT_MODEL` | `gemini-2.5-pro` | 图层精修的分层/分割模型（须为分析模型，非生图模型） |
| `LAYER_SPLIT_MASKS` | `0` | 置 `1` 才请求分割掩码（透明抠图）；默认框选不透明裁切 |
| `QUALITY_MODEL` | `gemini-flash-latest` | 适配质检评审模型（缺密钥则质检关闭、一律通过） |
| `QUALITY_THRESHOLD` | `75` | 质检加权总分通过线 |
| `KEY_ELEMENTS_FIDELITY_MIN` | `60` | 关键元素保真硬红线（0 = 关闭） |
| `QUALITY_MAX_RETRY` | `2` | 质检不过的最大重生成次数 |
| `PIXEL_BLUR_THRESHOLD` | `80` | 像素预筛的模糊阈值（Laplacian 方差；0 = 关闭） |
| `PIXEL_BORDER_MAX_RATIO` | `0.15` | 像素预筛允许的纯色边带最大占比 |
| **图生视频** | | |
| `VIDEO_PROVIDER` / `VIDEO_MODEL` | `happyhorse` / — | `veo` 可选；别名 `HAPPYHORSE_*`。需 COS 才启用 |
| `VIDEO_PROMPT_LLM_MODEL` | `claude-haiku-4-5-20251001` | 视频运动提示词增强用的 LLM |
| **物料爬取** | | |
| `CRAWL_BASE_URL` / `CRAWL_API_KEY` | — | 无 common 回退；端点未设即"未配置"（别名 `CRAWL_ENDPOINT`） |
| **COS（发布源图为公网 URL）** | | |
| `COS_SECRET_ID` / `COS_SECRET_KEY` / `COS_REGION` / `COS_BUCKET` / `COS_BASE_PATH` / `COS_PUBLIC_URL_PREFIX` | — | 六项齐全才算已配置；图生视频依赖它 |

模型可在前端**按 session 逐场景切换**（对话/图生图/文生图/图生视频四个 scene，见 `internal/config/catalog.go` 的模型目录）；服务端以配置的默认模型启动。

## 文字叠加字体

`overlay_text`（把 CTA / 折扣角标 / 定档大字确定性叠加到图上）用服务端字体光栅渲染，**逐字检测缺字**——任一字符无字形即报错，绝不渲染豆腐块。

- **ASCII / Latin**：开箱即用，内置 Go Bold 回退字体。
- **中文（CJK）**：仓库已内置 `configs/fonts/NotoSansSC-Regular.otf`（Noto Sans SC，SIL OFL 1.1 可商用），无需额外拉取。想换字体，设 `OVERLAY_FONT_PATH` 指向任意 CJK TTF/OTF 即可，无需改代码。

字体加载失败时 `overlay_text` 整体禁用（明确报错，不出豆腐块）。

## 渠道尺寸目录

`configs/channels.json` 是三级数据驱动目录：**渠道（分组：外渠/手机厂商/PC）→ 素材类型 → 尺寸**。每个尺寸带可选约束元数据（`format`/`maxKB`/`note`/`convergeMode`/`producible`），作为 UI 与 agent 的提示；裁剪本身不强制这些约束。编辑后重启即生效。缺文件时回退投影 `configs/platforms.json` 的两级预设，再缺则用内置 Universal 预设。

## 测试

```bash
go test ./...        # 全量单测（表驱动 + handler 契约 + 实时层 + 异步任务）
go vet ./...
make test            # 见 Makefile
```

各能力以 spec 的 Scenario 为验收基准（见 `openspec/specs/` 与已归档的 `openspec/changes/archive/`）。

## 开发辅助 · CodeGraph 索引

本仓库使用 [CodeGraph](https://github.com/colbymchenry/codegraph) 为 AI 编码代理预建代码知识图谱（符号关系 / 调用图 / 结构），让代理查图而非反复扫文件。索引落在 `.codegraph/codegraph.db`，**纯本地、不联网、无需密钥**，按机器生成，该 `.db` 已被 `.codegraph/.gitignore` 排除，不入库。

```bash
# 1. 安装 CLI（macOS / Linux）
curl -fsSL https://raw.githubusercontent.com/colbymchenry/codegraph/main/install.sh | sh

# 2. 接入本机的 AI 代理（自动探测并配置）
codegraph install

# 3. 在本项目根目录建索引（生成 .codegraph/ 并构建图谱）
codegraph init

# 升级 / 卸载
codegraph upgrade
codegraph uninstall
```

克隆仓库后各自跑一次 `codegraph init` 即可；索引数据按机器本地生成，不共享、不提交。

## 能力总览

对话编排（意图白名单 + 滑动窗口 + `submit_plan` 多步串行）、图生图（颜色适配 + 注入防护 + 主备 failover + 质检重试 + 像素预筛 + outpaint 收敛 + 抠图 + 主体锚点裁切）、文生图、裁剪与渠道适配、图层精修（分层抠图 + 固定画布拼接）、文字叠加、宣发文案、批量变体、营销主题分析、选区/点/多边形识别、图生视频、物料爬取、网页搜索、工作区（占位/状态/部分重试/按需分析）、下载/打包、per-session 模型切换、prompt 优化、结构化链路日志、sticky-last-output 续接、嵌入式前端。
