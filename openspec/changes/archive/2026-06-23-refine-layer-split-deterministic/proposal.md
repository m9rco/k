# 优化图层精修：确定性裁切分层（保真 + 精简 + 不重绘）

## Why

现有「图层精修」用**两次 AI 重绘**做分层：N 个 `extract_layer`（让 Gemini 把主体重画在纯绿幕上再键绿）+ 1 个 `fill_background`（让模型移除主体重绘干净背景）。这套机制与用户真实诉求——「**背景作为底线固定、把人物/宣发文案分出来、用于人工微调、不要过多元素、不要抠的连接不上**」——存在三处根本矛盾：

1. **抠的连接不上**：AI 重绘抠图本质是让模型「重画一遍主体」。重画无法做到与原图主体逐像素一致，叠回原位时必然出现**错位、漏边、多余轮廓/光晕、主体被染绿键穿**——视觉上「接不回去」。这是重绘式抠图的固有缺陷，调 prompt/容差只能缓解、不能根治。
2. **背景不是「固定底线」**：当前底层是 AI 重绘的 inpaint，**每次结果不同**、会改写真实像素、且偶发返回空数据被迫回退原图。用户要的「底线固定」恰恰是**原图本身**——稳定、零等待、零失真。
3. **元素过多**：检测枚举「角色/人物 + 核心主体/显著道具/物件 + 宣发文案块」三类、上限 8，会把盾牌、武器、装饰物等碎片都抠成独立层，远超用户「只要人物 + 宣发文案、用于微调」的预期。

根因结论：分层的目的是**人工微调**，需要的是**忠实于原图的可移动块**，而非 AI 重新创作的图层。因此本期把分层机制从「AI 重绘」整体改为「**确定性裁切**」。

## What Changes

- **背景层 = 原图本身（不再 `fill_background`）**：分层的锁定底层直接复用源图 asset，零生图调用、零等待、稳定不变、严丝合缝。主体微调时若移走主体，底下露出的是原图原貌（可接受，符合「微调」语义）。
- **主体层 = 按分割掩码从原图抠出的透明底子图（不再 `extract_layer` 重绘）**：视觉模型对每个主体返回**分割掩码（segmentation mask）**，服务端把掩码作为 **alpha 通道**贴到原图对应区域的**原始像素**上——RGB 100% 来自原图（不缩放、不变形、不重绘），掩码之外透明。产出的是真正的「透明底抠图层」：移动主体时背后干净地露出背景层，叠回原位又与原图严丝合缝。**全程不调用任何生图大模型**，掩码只是分析输出（与包围盒同性质）。当模型未给出可用掩码时（非 Gemini 传输或缺失），降级为按包围盒裁出的不透明矩形子图（仍是原图像素），保证不失败。
- **检测收敛为两类、上限收紧**：`subjectsPrompt`/`subjectMasksPrompt` 只枚举「角色/人物」与「宣发文案块」两类前景主体；LOGO、道具/物件、场景、装饰一律留在背景。`maxDetectedSubjects` 由 8 收紧到 5。
- **检测框 + 掩码真正被使用**：当前 `layering` 丢弃了 `Subject.Box`，新方案把 Box 与 Mask 贯通到抠图与前端摆放。
- **裁切端点**：`layer-split` 不再 spawn 生成任务等待；改为同步对原图按掩码抠出主体层 asset（`composite` kind）并返回每层的归一化位置框，供前端按原位摆放。
- **前端按原位摆放**：主体层不再统一 `x:0,y:0`（那是「整图等尺寸」时代的假设）；抠图层是主体大小的透明底子图，需按其 Box 的左上角落位，初始与原图重合。
- **保留但停用两个 AI 意图**：`extract_layer` / `fill_background` 意图定义、prompt、chroma-key 代码**保留不删**（其它入口或未来手动抠图可能复用），仅本期分层编排不再调用它们。
- 产物收费链路（合成导出、持久化端点、时间轴【拼接】归类）**不变**。

## Impact

- Affected specs:
  - `layer-compositing`（MODIFIED：自动分层从「AI 重绘」改为「掩码抠图」；背景层=原图；主体层=按分割掩码抠出透明底子图并带原位，掩码缺失时降级为不透明矩形）
  - `marketing-analysis`（MODIFIED：前景主体检测收敛为人物+文案两类、上限 5、Box+Mask 被消费）
- Affected code:
  - `internal/vision/subject.go`：`subjectsPrompt` 两类化、`maxDetectedSubjects` 8→5；新增 `subjectMasksPrompt` 与 `parseSubjectMasks`（Gemini 分割掩码：`box_2d`/1000 + `mask` data URI 解码）；`Subject` 增 `Mask` 字段；`DetectSubjects` 按 Gemini/OpenAI 传输分流
  - `internal/layering/service.go`：核心重写——读原图 → 检测 → 逐主体抠图（掩码作 alpha）N 个主体层 + 原图为背景层 → 返回带位置的有序图层；移除对 `generation.Start` 的依赖与 await 轮询
  - `internal/layering/crop.go`：`cropSubject` 增 `mask` 参数——掩码缩放为 alpha 贴到原始 RGB 上（透明底抠图）；掩码缺失/不可解码时回退按框裁不透明矩形
  - `web/src/components/workspace/compositing-canvas.tsx`：按返回的 Box 摆放透明底主体层
  - `web/src/lib/api.ts`：`SplitLayer` 含位置框字段
  - 测试：layering 断言「背景=源图 id + N 个抠图层带正确位置框 + 掩码 alpha 生效且 RGB 逐像素来自原图 + 掩码不可解码回退不透明」；vision 检测两类化 + `parseSubjectMasks` 用例
- **不影响**：`extract_layer`/`fill_background` 意图与 chroma-key（保留）、合成持久化端点、时间轴归类、计费/打包链路。
- 行为变化（用户可见）：分层产出**真正的透明底抠图层**（移动主体背后干净露出背景）、**更快**（无生图等待）、**更稳**（无 provider 抽风）、**零错位**（主体 RGB 逐像素来自原图，掩码只控透明度，不重绘）；掩码不可用时降级为不透明矩形子图（仍是原图像素），保证不失败。
