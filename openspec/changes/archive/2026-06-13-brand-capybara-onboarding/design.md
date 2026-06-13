# Design — Brand Capybara Onboarding

## Context (审计证据)

- `App.tsx:12` — 固定 `grid-cols-[7fr_3fr]`,无空状态区分
- `workspace-panel.tsx:54` — `isEmpty = nodes.length === 0`(任务+资产皆空时)
- `composer.tsx:134` — 静态 placeholder,有 suggestion chips 但只在有资产时出现
- `index.css` — `--bg: 222 18% 8%` 深蓝黑底,`--accent: 205 55% 60%` 蓝色强调
- `tailwind.config.ts` — 12px 圆角,`shimmer`/`fade-in` 动画 token,framer-motion 已用
- `docs/example/codebuddy.png` — 参考吉祥物风格(可视化参考)

## Decision 1 — 布局切换架构

**问题**:空状态需要"对话居中全屏",工作区有内容后需要"7:3 分栏"。如何切换?

**方案**:在 `App.tsx` 增加 `isEmpty` 判断(从 context 读取 `state.assets.size === 0 && state.tasks.size === 0`)。用 framer-motion 的 `motion.div` + `layout` prop 包裹整个 grid,配合 CSS 变量切换列定义。具体:
- 空态:`grid-cols-1 place-items-center` — 对话区居中,最大宽度 520px
- 有内容:`lg:grid-cols-[7fr_3fr]` — 恢复现有分栏

过渡用 `AnimatePresence` + `motion.div` 的 `initial/animate/exit`,持续约 400ms ease-out。工作区 panel 也用 framer-motion `layout` 保证展开时无跳变。

**注意**:空态下工作区(`main`)隐藏(`hidden`),对话区(`aside`)全宽居中。

## Decision 2 — 像素卡皮巴拉设计

**问题**:需要一个与深色克制主题协调、像素公仔风格的卡皮巴拉。

**方案**:纯 SVG 实现 16×16 像素格风格(每个"像素"是一个 `rect` 元素),无外部图片依赖。颜色盘:
- 毛色:暖棕 `#8B6914` / 浅棕 `#B8892A` / 高光奶白 `#E8D5A3`
- 深色阴影:`#5C4209`
- 眼睛:`#1a1a1a`
- 配饰(可选):一顶极小的方形帽子或一根草棍(像素级别)

**帧动画**:用 CSS `@keyframes` + `animation-name` 切换,不依赖 JS 定时器:
- **Idle-A**:默认坐姿,眼睛偶尔眨 (opacity 0→1→0 on eye-close rect)
- **Idle-B**:抬头左张望(头部 SVG 轻微位移 translateX)
- **Idle-C**:抬手挠头(前爪 rect 向上位移)
- **Idle-D(概率触发)**:打哈欠(嘴巴形状变换)

用 CSS animation-delay 分时触发各动作,加权随机用 `animation-duration` 错开。`prefers-reduced-motion` 时所有动画停止。

**位置**:渲染在 composer 输入框正上方,绝对定位于 `composer` 容器 + 负 top 值,z-index 高于输入框。

## Decision 3 — 打字机演示占位符

**问题**:空输入时展示能力演示,有输入时立即恢复正常 placeholder。

**方案**:composer 中维护一个 `demoText` 状态,只在 `isEmpty && !text` 时激活。用一个 `useEffect` + `setInterval` 轮播若干示例指令,每条指令逐字打出、停顿 2s、再逐字删除,循环。
- 示例指令列表:
  1. "把背景换成黄昏的赛博朋克城市…"
  2. "让图里的角色动起来，奔跑姿态"
  3. "切成抖音和小红书的投放尺寸"
  4. "根据游戏截图生成一批宣传图"
- `demoText` 渲染为 input 内容只读样式(text-fg-mute,cursor 不 focus)
- 当用户 focus/输入时立即隐藏演示,恢复正常 placeholder

## Decision 4 — 神秘感与高级感视觉增强

背景微纹:
- 用 CSS `background-image: url("data:image/svg+xml,...")` 生成极淡的点状噪点(4px tile),opacity 约 0.025,覆盖 `body` 或 `.app-root`。不依赖外部图片。

空状态标语:
- 在卡皮巴拉上方展示 `Game Asset Studio` 品牌名 + 一行副标题「游戏宣发资产工坊」,用 `tracking-widest text-fg-mute/60` 渲染为低调水印感。
- 首次进入时 `initial:{opacity:0,y:8}→animate:{opacity:1,y:0}` framer-motion 动画,延迟 0.3s。

## Decision 5 — 过渡时序

1. 用户发送第一条消息(或上传第一张图)→ `isEmpty` 变 false
2. framer-motion layout 动画触发:
   - 对话区从居中→右侧 3/10
   - 工作区从 hidden→左侧 7/10,用 `motion.div initial:{opacity:0,x:-20}→animate:{opacity:1,x:0}`
3. 卡皮巴拉:随工作区一起 `exit:{opacity:0,y:12,scale:0.8}` 淡出,持续 200ms
4. 整体 400ms,ease-out 曲线

## Risks

- 像素 SVG 体积:控制在 < 2KB(16×16 pixel grid),影响可忽略。
- 打字机演示:只在 `isEmpty && !text` 激活,不干扰任何正常输入;timer 在组件 unmount 时清理。
- 布局动画:framer-motion layout 动画在某些浏览器可能触发 repaint,用 `transform` 而非改变 width 实现,保证 GPU 合成层。
