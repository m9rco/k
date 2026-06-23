# Design: 图层精修——自动抠图分层与固定画布拼接

## Context
对一张指定尺寸的成品宣发图做精修时，诉求是把画面**拆成可独立摆放的图层**（角色/物件/LOGO/文案）再随手拼，而不是整图重生。关键约束三条：(1) 入口绑定在**那张图**上（右键/预览），是对该图的就地精修；(2) 画布尺寸**锁定为原图尺寸**；(3) 分层**自动**完成——系统分析画面、大模型逐主体抠图，用户不必手动框选。抠图层要真透明背景，gpt-image-2 做不到（现有 spec 已把它的「透明底」降级改写），所以抠图必须走 **Gemini 适配器**。

## Goals / Non-Goals
- Goals：右键某图→自动检测前景主体→每主体抠成源图同尺寸透明图层 + 补全干净背景底层→在锁定尺寸画布上移动/缩放/层叠/移除主体层→确定性拍平导出为新资产。
- Non-Goals：旋转/滤镜/混合/羽化、多画布、手动逐主体框选（被自动检测取代）、服务端重渲染管线。

## Decision 1: 三类原子能力 + 一个编排层
分层拆成可独立测试的原子能力，由编排层串起：
- **前景主体检测**（`internal/vision`，`SubjectDetector.DetectSubjects`）：复用既有 Gemini inline 视觉传输，prompt 要求只列前景主体（排除背景/氛围），返回 `{desc, box}` 列表，按重要性排序、上限 8。输出经 `{"subjects":[...]}` 包裹以复用既有 `extractJSON`（只认对象）。无主体/不可解析 → 空列表，不崩。
- **`extract_layer`**（`internal/generation`）：抠某主体为源图同尺寸透明 PNG，强制 Gemini，未配置则**拒绝**（不伪造不透明图）。
- **`fill_background`**（`internal/generation`）：移除主体、补全干净背景，不透明输出，优先 Gemini、**可降级**到默认适配器（有底总比没底好）。
- **编排**（新 `internal/layering`，`POST /layer-split`）：检测 → 并发 spawn「1×fill_background + N×extract_layer」→ 各自 await → 汇总有序图层（背景最底、主体按序）。背景失败=整体失败；个别主体失败=best-effort 丢弃；至少 2 层才算成功。

**为什么编排在后端而非前端串**：分层要顺序依赖检测结果、且要并发等待多个异步生成任务并做 best-effort 容错——放后端一次性返回有序图层，前端只管渲染，契约清晰。同步返回（非 SSE）是因为前端就是要拿到全套图层才能开画布。

## Decision 2: 画布锁定 = 源图尺寸；背景层锁定
画布 W/H 来自 `layer-split` 返回（= 源图尺寸），前端不提供调整。背景层 role=background，渲染为锁定底层：不接受拖拽/缩放/移除，z-order 也不能把别的层压到它下面（`moveZ` 守 index 0）。主体层 role=subject，自由 move + uniform scale + z + remove，非破坏式（只记录变换）。各层初始落原位（x=0,y=0,scale=1，因为每层都是源图同尺寸的透明图/背景图），画布初始与源图一致。

## Decision 3: 导出 = 客户端确定性拍平
分层阶段已耗尽全部 AI；导出只是几何合成。前端 `canvas.toBlob` 按层序 drawImage 拍平为 PNG，经 `internal/composite` 直连端点（不经 agent/LLM）落库为 `composite` 资产，复用 PNG 无损优化（保 alpha）+ 下载/打包链路。时间轴 composite→【拼接】事件；抠出的透明图层与背景层是 AI 产物（带重试），沿用【生成/编辑】。

## Decision 4: 入口绑定到具体图，prop 透传而非全局状态
`onLayerSplit(asset)` 与既有 `onCrop`/`onVideo` 同构，从 WorkspacePanel 经 grid/timeline 透传到 AssetCard 右键项与 Lightbox 按钮；画布以 `splitFor: Asset|null` 受控打开。不引入全局 controller 状态，保持与现有交互一致。

## 与既有能力的衔接（无需改契约）
- 多供应商适配器 / 低发散骨架 / region_desc 注入防护 / 已生成图重试：两个新意图直接沿用。
- `image-optimization` 无损优化涵盖 alpha；`download-packaging` 透明 PNG 走兜底目录；`asset-workspace` 编号/移除/会话隔离自动适用。

## Risks
- Gemini 透明度/移除补全质量有波动（毛边、残影）：本期接受，定位「随手拼精修」；必要时后续加确定性 alpha 清理或重试，不在本期。
- 一次分层=1+N 次生图调用，成本随主体数上升：检测上限 8 封顶；背景失败即整体失败避免出半成品。
- 大图拍平内存：画布=源图尺寸有界，按需缩放预览（dispScale），导出用全分辨率离屏 canvas。
