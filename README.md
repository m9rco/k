# Game Asset Studio

对话式游戏宣发素材生成系统的 MVP。在一个对话窗口里完成 **换角色 / 换背景 / 换文案 → 切平台尺寸 → 预览 / 重试 → 下载** 的闭环。后端 Go 单二进制，前端用 `embed` 嵌入，开箱即跑。

> 仅供小团队内部使用，无鉴权。密钥通过环境变量注入，**不硬编码**。

## 架构

```
cmd/server          单二进制入口（HTTP + 优雅退出 + embed 前端）
internal/config     集中配置（env + 平台尺寸 JSON）
internal/store      SQLite 持久化（sessions / assets / tasks，预留 preferences）
internal/session    匿名 session（浏览器指纹生成、复用、隔离）
internal/transport  实时层：WebSocket（对话）+ SSE（任务进度，含 Last-Event-ID 重连）
internal/agent      Eino ReAct 编排：意图白名单分发 + context 滑动窗口 + 工具注册
internal/generation 生图：主/备 gpt-image-1 失败切换、颜色适配、prompt 注入防护、异步任务
internal/crop       纯图像裁剪（cover-fit 居中，横竖适配），数据驱动平台尺寸
internal/workspace  工作区：列资产/任务、上传、部分重试
internal/download   单图下载 + 批量 zip 打包（跳过无效项并报告）
web/static          前端单页（原生 ES 模块，无构建步骤）
configs/platforms.json  平台尺寸预设（可编辑，数据驱动胶囊按钮）
```

## 运行

```bash
# 1. 配置密钥（见下方清单）。最小可跑：仅前端 + 上传/裁剪/下载，无需任何密钥。
export ANTHROPIC_API_KEY=sk-...          # 对话编排（缺失时对话功能不可用，其余正常）
export IMAGE_PRIMARY_API_KEY=...         # 生图主供应商
export IMAGE_PRIMARY_BASE_URL=https://...

# 2. 启动
go run ./cmd/server
# 或构建单二进制
go build -o asset-studio ./cmd/server && ./asset-studio

# 3. 打开 http://localhost:8080
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ADDR` | `:8080` | HTTP 监听地址 |
| `DB_PATH` | `data/asset-studio.db` | SQLite 文件路径 |
| `ASSET_DIR` | `data/assets` | 生成/裁剪产物存储目录 |
| `ASSET_RETENTION_HOURS` | `0` | 产物保留时长（0 = 永久保留） |
| `CONFIG_PLATFORMS` | `configs/platforms.json` | 平台尺寸配置文件 |
| `CONTEXT_TOKEN_BUDGET` | `8000` | 对话上下文滑动窗口 token 预算 |
| **对话模型** | | |
| `CHAT_PRIMARY_PROVIDER` | `anthropic` | 主对话模型供应商 |
| `CHAT_PRIMARY_MODEL` | `claude-opus-4-8` | 主对话模型 id |
| `CHAT_PRIMARY_BASE_URL` | （供应商默认） | 自定义端点 |
| `ANTHROPIC_API_KEY` | — | 主对话模型密钥 |
| `USE_TEST_MODEL` | `false` | 切换到测试模型（DeepSeek） |
| `CHAT_TEST_MODEL` | `deepseek-chat` | 测试模型 id |
| `CHAT_TEST_BASE_URL` | `https://api.deepseek.com/v1` | 测试模型端点 |
| `DEEPSEEK_API_KEY` | — | 测试模型密钥 |
| **生图供应商（主 + 备，均为 gpt-image-1）** | | |
| `IMAGE_PRIMARY_BASE_URL` | OpenAI 官方 | 主生图端点（OpenAI 兼容） |
| `IMAGE_PRIMARY_API_KEY` | — | 主生图密钥 |
| `IMAGE_PRIMARY_MODEL` | `gpt-image-1` | 主生图模型 |
| `IMAGE_BACKUP_BASE_URL` | OpenAI 官方 | 备用生图端点 |
| `IMAGE_BACKUP_API_KEY` | — | 备用生图密钥 |
| `IMAGE_BACKUP_MODEL` | `gpt-image-1` | 备用生图模型 |

模型在服务端硬编码（由配置决定），用户不可在前端切换。

## 平台尺寸

`configs/platforms.json` 是数据驱动配置：平台 → 多个尺寸（含横/竖/方）。编辑后重启即生效，前端胶囊按钮自动渲染。当前为一套通用默认值（Universal / Social Feed / App Store / Web Banner），可按实际广告位替换。

## 测试

```bash
go test ./...        # 全量单测（表驱动 + handler 契约 + 实时层 + 异步任务）
go vet ./...
```

各能力以 spec 的 Scenario 为验收基准（见 `openspec/changes/add-asset-studio-mvp/specs/`）。

## 已实现 / 后续

**MVP 已实现**：session、对话编排（意图白名单 + 滑动窗口）、生图（颜色适配 + 注入防护 + 主备切换）、裁剪、工作区（占位/状态/部分重试）、下载/打包、实时传输、嵌入式前端。

**后续 change（已预留扩展点）**：生视频、物料爬取、长期记忆 / 偏好自进化。前端「偏好角落」已占位（空时隐藏）。
