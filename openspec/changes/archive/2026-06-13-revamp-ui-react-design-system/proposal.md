# Change: 前端重构为 React 设计系统 + 视觉「去 AI 化」与微动效

## Why
当前前端是原生 JS（1940 行 app.js）+ 手写 CSS（791 行），虽然功能完整，但视觉上是典型的「AI 生成模板感」：装饰性渐变色块多、边框重、直角与不统一的圆角混用、文字行距拥挤、品牌图标与工具标签大量使用彩色 Emoji。整体不够沉静、专业。

同时团队希望把前端栈升级到现代组件化体系（Tailwind + shadcn/ui + Radix + Framer Motion + Lucide），以便后续以「组合现有组件」而非手写复杂弹窗/下拉/Tab 的方式持续演进，并获得一致的丝滑微动效。

参考 `docs/example/demo-1/2/3.png`（CodeBuddy 风格 IDE 工作台）：深色克制主调、左侧导航/资源栏、中间主区、右侧常驻对话面板、底部状态栏，整体安静、信息密度高而不杂乱。

## What Changes
- **BREAKING（前端实现栈）**：前端由「原生 JS + 手写 CSS、零构建」整体重写为 **Vite + React + TypeScript**，构建产物经 `embed` 进 Go 单二进制（部署形态「单可执行文件」不变）。开发期 Vite dev server 代理 `/api`、`/api/ws`、SSE 到 Go。
- **设计系统与 token**：建立统一设计 token——圆角统一为 **12px**（small 8px、full 用于 chip/头像）、正文行高 **1.6**、收紧/弱化边框（用更轻的分隔线与留白替代重边框）、移除装饰性渐变色块、降低强调色饱和度，达成「沉静、高级」。
- **组件库**：引入 **shadcn/ui（基于 Radix UI）**。弹窗（lightbox/确认）、下拉/右键菜单、Tab（尺寸选择器的渠道分组）、Toast 等**一律用 shadcn/Radix 组件组合**，不再手写。
- **微动效**：引入 **Framer Motion**，为消息涌现、思考块展开/折叠、工具卡片状态切换、占位骨架→产物的过渡、卡片进出场等加丝滑微动效（克制、短时长、ease-out）。
- **图标**：引入 **Lucide React**，**移除全部 Emoji**（工具标签 🎨/🎬/✂️ 等、品牌标识），统一为线性图标；**重做品牌图标**为简洁单色 mark。
- **布局对齐参考图**：采用 IDE 式三区布局（左导航/资源栏 · 中工作区 · 右对话面板）+ 底部状态栏，呼应 demo 截图的专业克制感（在现有「工作区/对话」两栏基础上重构）。
- **功能全量平移**：现有全部能力行为不变，仅换皮与交互质感——对话流式与思考打字机、工具调用卡片、工作区里程碑分组、占位骨架与演出式进度、失败重试/移除/一键清除、多图参考、二次调整、视频预览、尺寸选择器（渠道→类型→尺寸）、上传、下载/打包、上下文面板与清理、提示词优化、偏好角落、下一步建议、拖拽排序、响应式。

## Impact
- Affected specs: `frontend-experience`（视觉规范、组件来源、动效、图标、技术栈约束的 MODIFIED/ADDED；功能性 Scenario 行为保持）
- Affected code:
  - 新增 `web/` 下 React 工程（`package.json`、`vite.config.ts`、`tailwind.config.ts`、`src/`、shadcn 组件目录），构建输出到 `web/static/`（或新目录）供 `embed`
  - `web/web.go`（embed 路径可能调整为构建产物目录）
  - 移除/归档 `web/static/app.js`、`web/static/styles.css`、`web/static/index.html`（被构建产物取代）
  - 后端 API/WS/SSE 契约**不变**（仅前端消费方式变化）；`cmd/server` 的 SPA fallback 可能微调以适配带 hash 的静态资源
- 非目标：不改任何后端业务逻辑、不改模型/供应商、不改数据模型、不改 API 形状。
