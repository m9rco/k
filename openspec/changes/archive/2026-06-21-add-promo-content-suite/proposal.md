# Change: 投放素材增强套件（文案生成 / 文字叠加 / 批量变体）

> **范围调整（实施后）**：原提案含第 4 块「投放前合规审查（compliance-review）」，经决策**剖离不实现**——内部小团队工具，合规终审依赖人工/法务，不以软件硬做。本 change 最终交付前三块（文案 / 叠加 / 变体）；下文保留合规相关原始论述作为历史规划记录，其 spec 已从本 change 移除。

## Why
现有系统的宣发链路已覆盖「分析素材 → 生图/换素材 → 平台适配 → 生视频 → 下载打包」，但投放团队的高频闭环仍有四处真实空白（已逐一比对现有 specs 确认）：

1. **没有主动产出宣发文案的能力**。`marketing-analysis` 只「分析」图里已有的要素；`image-generation` 的「换文案」是把文字画进图里；`platform-adaptation` 的文案只是适配时「保留」。无任何能力主动生成广告语 / 标题 / 卖点 / 平台投放文案——这是宣发链路最显眼的空白。
2. **文字只能靠生图模型「画」**，CTA、促销角标、定档大字、LOGO 经常糊、错字、位置不可控。缺一个确定性的文字/LOGO 叠加层。
3. **一次只产一张图**，而买量核心打法是批量跑 creative 测 CTR，缺「一键 N 变体」。
4. **投放前无任何合规护栏**。敏感词、平台投放政策、版号/资质信息缺失等过审完全空白，出海与买量刚需。

这四者构成一个自然闭环：**生成文案 → 叠加到图上 → 批量出变体 → 投放前合规审查**，因此合并为一个 change，按依赖分期实施。

## What Changes
- **新增 `copywriting-generation` 能力**：新增 `generate_copy` 工具，基于游戏信息 + 已上传素材 + 视觉分析报告，产出结构化宣发文案（标题/广告语/卖点/平台投放文案），支持平台与字数约束、防注入。
- **新增 `text-overlay` 能力**：新增 `overlay_text` 工具，对工作区某张图做**确定性**文字/LOGO 叠加（CTA、促销角标、定档大字、品牌 LOGO），服务端字体渲染，位置/字号/描边/安全区可控，不经生图模型。
- **新增 `batch-variants` 能力**：新增 `generate_variants` 工具，对一个生图/改图意图按变体策略（角度/配色/文案/构图）一次性产出 N 个 creative，复用现有生图与长任务管线，前端批量占位回填。
- ~~**新增 `compliance-review` 能力**~~（**已剖离不实现**：内部工具，合规终审依赖人工/法务，不以软件硬做）。
- **修改 `conversation-orchestration`**：将上述三类意图纳入意图白名单与确定性预分类，使 Agent 能识别「写文案 / 加个 CTA / 多出几版」并分发到对应工具。

## Impact
- 受影响 specs：
  - 新增：`copywriting-generation`、`text-overlay`、`batch-variants`（`compliance-review` 已剖离，不在本 change）
  - 修改：`conversation-orchestration`（意图白名单 + 预分类）
- 受影响代码（实现阶段）：
  - `internal/agent/`（工具注册、意图白名单、预分类提示）
  - 新增 `internal/copywriting/`、`internal/textoverlay/`
  - `internal/generation/`（批量变体复用生图管线）
  - `web/src/`（文案卡片、批量变体网格）
- 非目标（Non-Goals）：不做真实平台投放/上传 API 对接；不做自动文案翻译出海（本地化作为后续 change）；**投放前合规审查已剖离不做**（合规终审依赖人工/法务）。
