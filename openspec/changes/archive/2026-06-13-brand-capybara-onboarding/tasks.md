# Tasks — Brand Capybara Onboarding

## 1. 品牌神秘感背景微纹
- [x] 1.1 `web/src/index.css`:在 `:root` 或 `body` 层添加 SVG data-URI 点状噪点背景图，`background-size:4px 4px`，`opacity` 约 0.03，不影响任何前景内容。
- [x] 1.2 验证：各浏览器下内容可读性不受影响，`prefers-reduced-motion` 不需要特殊处理（纯 CSS 静态背景）。

## 2. 像素卡皮巴拉 SVG 组件
- [x] 2.1 新建 `web/src/components/capybara/capybara.tsx`：纯 SVG 像素风卡皮巴拉（16×16 格、暖棕色盘），基础坐姿帧。
- [x] 2.2 CSS 帧动画：Idle-A(眨眼)、Idle-B(左右张望)、Idle-C(挠头)，用 `@keyframes` + `animation-delay` 分时触发。
- [x] 2.3 概率惊喜动作(Idle-D 打哈欠)：用额外延迟较长的 keyframe 周期触发，无需 JS。
- [x] 2.4 退出动画：prop `exiting` → framer-motion `exit:{scale:0.8,opacity:0,y:12}`。
- [x] 2.5 `prefers-reduced-motion`：CSS media query 将所有 animation-play-state 设为 paused。

## 3. 空状态沉浸引导态布局
- [x] 3.1 `web/src/store/context.ts` 或通过现有 `state`：导出 `isWorkspaceEmpty` 计算属性（`assets.size === 0 && 无 active/failed task`）。
- [x] 3.2 `web/src/App.tsx`：读取 `isWorkspaceEmpty`；为整个 grid 容器加 framer-motion `layout` 动画，空态时 className 切换为单列居中，有内容时切回 `lg:grid-cols-[7fr_3fr]`。
- [x] 3.3 `web/src/components/chat/chat-panel.tsx`：空态下对话区在居中单列内以 `max-w-[560px] mx-auto` 收窄。品牌标语区在 `ChatPanel` 顶部（仅空态可见）：`Game Asset Studio` + 副标语，framer-motion `initial:{opacity:0,y:8}→animate:{opacity:1,y:0}` 延迟 300ms。
- [x] 3.4 `web/src/components/workspace/workspace-panel.tsx`：有内容时 framer-motion `initial:{opacity:0,x:-24}→animate:{opacity:1,x:0}` 进场动画；空态下整个 main 隐藏（`hidden`）而非渲染空文案。

## 4. 打字机演示占位符
- [x] 4.1 `web/src/components/chat/composer.tsx`：新增 `demoMode`（仅 `isWorkspaceEmpty && !text && !focused` 时为 true）。
- [x] 4.2 `useEffect` + `setInterval`：循环轮播 4 条示例指令，逐字 append → 停顿 2s → 逐字 delete → 下一条。结果存入 `demoText` state，覆盖 input placeholder 内容（以 disabled/readonly 叠层方式显示，避免干扰真实 value）。
- [x] 4.3 用户 focus 或输入时立即清除定时器，`demoText` 置空；unmount 时清理定时器。
- [x] 4.4 `prefers-reduced-motion`：跳过打字机动效，直接静态显示第一条示例。

## 5. 卡皮巴拉注入 Composer 空状态
- [x] 5.1 `web/src/components/chat/composer.tsx`：`demoMode` 时在 form 上方渲染 `<Capybara exiting={!demoMode} />` 组件，`AnimatePresence` 包裹。
- [x] 5.2 定位：`absolute -top-[72px] left-1/2 -translate-x-1/2`（相对于 composer 容器），z-index 高于输入框。
- [x] 5.3 `AnimatePresence` 保证 `demoMode` 变 false 时触发 exit 动画（缩小淡出），布局同步展开。

## 6. 工作区展开过渡
- [x] 6.1 App.tsx 的 grid 用 framer-motion `motion.div` + `layout` 驱动，`transition:{duration:0.4,ease:"easeOut"}`。
- [x] 6.2 workspace-panel 根节点用 `motion.div` + `initial:{opacity:0,x:-24}→animate:{opacity:1,x:0}` + `AnimatePresence`，进场时 400ms，退场淡出。
- [x] 6.3 验证 Playwright：空→有内容→清空三次切换无跳变；`prefers-reduced-motion` 下静止切换仍功能正常。

## 7. 验证与回归
- [x] 7.1 `tsc -b` + `npm run build` 通过。
- [x] 7.2 `go build ./...` 通过（本提案不改后端，仅确认无意外影响）。
- [x] 7.3 Playwright E2E：首次进入→沉浸态可见+卡皮巴拉+打字机；发送消息→工作区丝滑展开+卡皮巴拉退出；清空→工作区收起+卡皮巴拉回来；`prefers-reduced-motion` 模拟→静态展示。
- [x] 7.4 回归：既有对话/工具/上传/切尺寸/生视频/时间轴/网格切换功能不退化。
- [x] 7.5 `openspec validate brand-capybara-onboarding --strict` 通过。
