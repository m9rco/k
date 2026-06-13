# Design: React 设计系统重写

## Context
后端是 Go 单二进制，前端经 `go:embed` 嵌入静态资源，部署为单可执行文件。现有前端零构建（原生 JS + 手写 CSS）。本次在**不改后端契约、不改部署形态**的前提下，把前端重写为 React + Vite 工程，并落地「去 AI 化」视觉与 Framer Motion 微动效。功能必须全量平移（行为不回归）。

后端对接面（前端唯一依赖）已固定，重写时严格照此消费：
- 会话：`POST /api/session`、`GET /api/session/{id}/context`、`GET /api/session/{id}/window`、`POST /api/session/{id}/context/clear`
- 实时：`GET /api/ws?session=`（WS：message/reasoning/tool_call/tool_result/capsule/error/task_created）、`GET /api/tasks/{id}/events`（SSE：task_queued/running/progress/done/failed）
- 工作区：assets/tasks 列举、upload、asset 删除、task 删除/失败清除/重试、clear
- 裁剪：`GET /api/platforms`、`POST /api/session/{id}/crop`
- 下载：单图 download、`POST /api/session/{id}/download/zip`
- 提示词优化：`POST /api/session/{id}/prompt/optimize`

## Goals / Non-Goals
- Goals：现代组件化栈（Tailwind+shadcn/Radix+Framer Motion+Lucide）、统一设计 token（12px 圆角/1.6 行高/轻边框/无装饰色块）、丝滑微动效、单二进制部署不变、功能零回归。
- Non-Goals：不动后端逻辑/模型/数据模型/API 形状；不引入服务端渲染；不引入路由库（单页单视图，无需 React Router）。

## Decision D1：Vite + React + TS，构建产物 embed 进 Go
**方案**：在 `web/` 建立 Vite + React + TypeScript 工程，`vite build` 输出到一个被 `go:embed` 的目录（如 `web/dist`，相应更新 `web/web.go` 的 embed 路径与 `FS()`）。Go 侧 SPA fallback 继续对未知路径回 `index.html`，并正确服务带内容 hash 的 `assets/*`。
- 开发：`vite dev` 起前端，`server.proxy` 把 `/api`、`/api/ws`（含 WS upgrade）、`/api/tasks/*/events`（SSE）代理到本地 Go（:8080）。
- 生产：先 `vite build` 再 `go build`，单二进制不变。文档化两步（或加 Makefile 目标）。
**备选（否决）**：前后端分离部署——改变了「单可执行文件」这一项目约束，否决。

## Decision D2：设计 token 与「去 AI 化」规范
集中在 Tailwind theme + CSS 变量：
- 圆角：`--radius: 12px`（DEFAULT），`sm: 8px`，`full: 9999px`（仅 chip/头像/状态点）。直角与杂乱圆角全部归一。
- 行高：正文 `leading` 1.6；标题适当收紧。
- 边框：弱化为低对比分隔线（如 `border` 用 8–12% 不透明度的前景色），多处用留白/层级背景替代实线边框。
- 色彩：移除装饰性渐变色块与高饱和强调；强调色降饱和、克制使用；中性灰阶为主，暗色基调。
- 阴影：极轻或无，避免「卡片漂浮」的模板感。
这些 token 是后续所有组件样式的唯一来源，杜绝散落硬编码。

## Decision D3：shadcn/ui + Radix 承接所有「复杂交互」组件
- Dialog（lightbox 预览、清空确认）、DropdownMenu/ContextMenu（资产右键菜单）、Tabs（尺寸选择器的渠道分组）、Toast（异常通知）、Tooltip、ScrollArea、Progress 等一律用 shadcn 组件组合，禁止手写。
- shadcn 组件源码纳入仓库（`src/components/ui/`），按设计 token 定制，不引入未用到的组件。
- 可访问性（焦点管理、Esc 关闭、aria）由 Radix 提供，替代现有手写实现。

## Decision D4：Framer Motion 微动效（克制）
- 原则：短时长（120–240ms）、ease-out、位移小（≤8px）、透明度为主；尊重 `prefers-reduced-motion`。
- 落点：助手消息/思考块涌现与折叠、工具卡片 spinner→✓/✗ 状态切换、占位骨架→产物卡片的交叉淡入、工作区卡片进出场（`AnimatePresence`）、lightbox 开合、Toast 进出。
- 打字机：保留「前端按速率逐字吐出」的现有体感（与流式增量解耦），用 React state + rAF/定时器实现；Framer 负责容器层动效而非逐字。

## Decision D5：图标与品牌
- 全量用 Lucide React 替换 Emoji：工具标签（换背景/换角色/换文案/生成视频/切尺寸/查询尺寸/爬取物料）、菜单项、状态、操作按钮。
- 品牌 mark 重做为简洁单色线性图标（不含彩色渐变），与整体克制风格统一。
- 工具→图标的映射集中在一处常量（取代现有 `TOOL_LABELS` 的 emoji map）。

## Decision D6：状态管理与实时层
- 单页单视图，状态用 React 内置（`useState`/`useReducer`/Context）即可，不引入 Redux 等。会话、资产/任务、对话消息、选择集、连接状态各自一个 reducer/context slice。
- WS 与 SSE 封装为 hooks（`useConversationSocket`、`useTaskStream`），复刻现有逻辑：WS 断线重连、SSE 具名事件监听（addEventListener 各事件名）、task_created 即时占位、演出式进度（时间驱动爬升 + 完成冲线）。
- 打字机、思考块、占位进度等「体感」逻辑按现有实现等价迁移，保证行为不回归。

## Decision D7：迁移与验证策略
- 旧文件（app.js/styles.css/index.html）在新工程跑通并逐项对照后移除（或先保留至验收通过）。
- 按功能模块逐块平移并自测：对话流式 → 工具卡片 → 工作区/进度 → 尺寸选择器 → lightbox/视频 → 上传/下载 → 上下文/优化/偏好/建议 → 响应式。
- 验证：`vite build` 通过、`go build` 通过、单二进制启动后逐项手测对照功能清单；关键交互（流式、占位进度、失败移除、多图参考、视频预览）回归确认。

## Risks
- **大重写回归风险**：1940 行交互逻辑迁移易遗漏。缓解：以现有函数清单为对照表逐项核销；分模块提交。
- **构建链引入**：项目首次有 Node/构建步骤。缓解：文档化 + Makefile 目标；CI/本地两步（vite build → go build）。
- **WS/SSE 在 Vite 代理下的开发体验**：需正确配置 proxy 的 ws 与 SSE。缓解：dev 配置中显式开启 `ws: true` 并禁用对 SSE 的缓冲。
- **embed 路径变更**：`web.go` 与 SPA fallback 调整需与构建输出目录一致。缓解：约定固定输出目录并在 design 固化。
