# Change: Asset Studio MVP（对话式宣发素材生成 — 核心闭环）

## Why
游戏宣发团队需要在一个对话窗口里完成"换角色/换背景/换文案 + 切平台尺寸 + 下载"这类高频素材操作。目前没有任何实现。本提案落地 MVP 核心闭环：会话 → 意图识别 → 生图（含颜色适配）→ 平台裁剪 → 工作区预览/重试 → 下载。生视频、物料爬取、长期记忆与自进化作为后续 change 引入，但在前端预留入口与角落槽位。

## What Changes
- **session-management**：进入即按浏览器信息生成无登录 session；sessionStorage 同步；会话上下文面板
- **conversation-orchestration**：基于 Eino 的 ReAct agent；意图识别（仅执行预设意图，其余礼貌拒绝）；context 滑动窗口（裁剪+压缩）；工具分发；prompt 注入防护
- **image-generation**：换角色/背景/文案（**颜色适配**为硬要求）；参考图构图复用；点击已生成图二次调整
- **image-cropping**：平台尺寸预设（横/竖），胶囊按钮选择，**非 AI** 纯裁剪/缩放
- **asset-workspace**：可操作预览区；并发任务占位+状态；**部分重试**；点图二次调整入口
- **download-packaging**：单图下载 + 批量打包（后端 zip）
- **frontend-experience**：科技感品牌化 UI；响应式/骨架屏/CSS 过渡；对话窗口为核心（参考 Cursor/Agent 形态，工具调用与状态严格可视）；异常小弹窗通知；偏好角落槽位（空时不展示）
- **realtime-transport**：WebSocket 承载对话与工具事件；SSE 承载长任务进度
- 后端 Go 单二进制 + `embed` 嵌入前端；模型服务端硬编码

## Deferred（本提案不实现，仅预留）
- **asset-scraping**：游戏名 → 爬物料 → 预览（已定技术方向：headless browser）
- **video-generation**：静态图 → 动效（如走路）
- **memory-system**：长期记忆 + 用户偏好/反馈标签学习 + 自进化；前端"偏好角落"先占位，引擎后置

## Impact
- Affected specs（新增能力）：session-management, conversation-orchestration, image-generation, image-cropping, asset-workspace, download-packaging, frontend-experience, realtime-transport
- Affected code（全新代码库）：
  - `cmd/`（入口）、`internal/agent/`（Eino 编排+工具）、`internal/session/`、`internal/generation/`（生图）、`internal/crop/`、`internal/transport/`（WS+SSE）、`internal/store/`（SQLite）
  - `web/`（嵌入式前端：对话窗口、工作区、下载）
- External：Anthropic / DeepSeek / OpenAI 兼容生图端点（密钥硬编码，apply 阶段提供）
