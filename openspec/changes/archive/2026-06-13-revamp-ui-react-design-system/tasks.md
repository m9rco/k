# Tasks: 前端 React 设计系统重写

## 1. 工程脚手架与构建链
- [x] 1.1 在 `web/` 初始化 Vite + React + TypeScript 工程（`package.json`、`vite.config.ts`、`tsconfig.json`）
- [x] 1.2 接入 Tailwind CSS（`tailwind.config.ts`、postcss），配置暗色基调与设计 token（圆角 12/8、行高 1.6、轻边框、降饱和强调色）
- [x] 1.3 初始化 shadcn/ui（`components.json`，按 token 定制主题变量），仅安装将用到的组件
- [x] 1.4 安装 Framer Motion 与 Lucide React
- [x] 1.5 配置 dev server 代理：`/api`、`/api/ws`（ws:true）、`/api/tasks/*/events`（SSE 不缓冲）转发到 Go
- [x] 1.6 配置 `vite build` 输出目录，更新 `web/web.go` 的 embed 路径与 `FS()`，调整 SPA fallback 以服务 hash 资源
- [x] 1.7 文档化两步构建（vite build → go build），可加 Makefile 目标

## 2. 设计 token 与基础样式
- [x] 2.1 定义全局 CSS 变量 + Tailwind theme：颜色层级、圆角、行高、分隔线、阴影（极轻/无）
- [x] 2.2 重做品牌 mark 为单色线性图标，替换旧 SVG
- [x] 2.3 建立工具→Lucide 图标映射常量（取代 emoji map）

## 3. 实时层与状态（hooks）
- [x] 3.1 `useSession`：bootSession（指纹）、context/window 拉取与刷新
- [x] 3.2 `useConversationSocket`：WS 连接/断线重连，分发 message/reasoning/tool_call/tool_result/capsule/error/task_created
- [x] 3.3 `useTaskStream`：SSE 按具名事件 addEventListener，task_created 即时占位、演出式进度（时间驱动爬升+完成冲线）、幂等去重
- [x] 3.4 工作区状态 slice：assets/tasks/selected/order，多选与拖拽排序

## 4. 对话区（中/右面板）
- [x] 4.1 消息气泡与流式增量；前端打字机逐字渲染（与增量解耦）
- [x] 4.2 思考块：逐字涌现 + 结论/工具开始时折叠（Framer 展开/折叠动效，可回看）
- [x] 4.3 工具调用卡片：图标+中文短语+可读副信息，spinner→✓/✗ 状态切换（Lucide 图标）
- [x] 4.4 composer：输入、发送、上传入口、提示词优化、无损开关、多图参考态条与清除
- [x] 4.5 上下文状态面板与清理、异常 Toast（shadcn Toast）

## 5. 工作区
- [x] 5.1 里程碑分组（进行中/已完成/失败），空状态
- [x] 5.2 占位骨架卡 + 演出式进度条（Framer 过渡到产物卡片）
- [x] 5.3 资产卡：图片/视频（hover 预览）、尺寸标识、渠道/类型标签、多选、单图查看按钮
- [x] 5.4 失败卡：重试/移除；失败分组「清除全部」
- [x] 5.5 工作区操作：全选/取消全选、批量切尺寸、清空、打包下载
- [x] 5.6 拖拽排序（前端展示态）、下一步建议、偏好角落

## 6. 弹层与复杂交互（shadcn/Radix）
- [x] 6.1 lightbox 预览（Dialog）：图片预览 + 二次调整 + 生视频入口；视频资产用 `<video>` 预览并隐藏图片专用工具
- [x] 6.2 资产右键菜单（ContextMenu/DropdownMenu）：按图片/视频类型显示对应项
- [x] 6.3 尺寸选择器（Dialog + Tabs）：渠道分组 Tab → 类型 → 尺寸 chip，已选汇总条，执行裁剪（含批量）
- [x] 6.4 确认类弹窗（清空工作区/清理上下文）用 shadcn Dialog

## 7. 微动效打磨（Framer Motion）
- [x] 7.1 消息/思考/工具卡片进出与状态切换动效（克制、ease-out）
- [x] 7.2 工作区卡片进出场 `AnimatePresence`、占位→产物交叉淡入
- [x] 7.3 lightbox/Toast 开合动效；全局尊重 prefers-reduced-motion

## 8. 布局与响应式
- [x] 8.1 对齐参考图的 IDE 式布局（导航/工作区/对话面板 + 底部状态栏）
- [x] 8.2 响应式：窄屏堆叠顺序与分隔处理

## 9. 验证与回归
- [x] 9.1 `vite build` 通过、`go build ./...` 通过、单二进制启动可打开完整前端
- [x] 9.2 以现有功能函数清单为对照表，逐项手测核销（对话流式/思考/工具卡/占位进度/失败移除/多图参考/二次调整/视频预览/尺寸选择/上传/下载打包/上下文/优化/偏好/建议/拖拽/响应式）
- [x] 9.3 视觉验收：12px 圆角、1.6 行高、无装饰色块、Lucide 图标、品牌 mark 单色
- [x] 9.4 移除旧 `app.js`/`styles.css`/`index.html`（验收通过后）
- [x] 9.5 `openspec validate revamp-ui-react-design-system --strict` 通过
