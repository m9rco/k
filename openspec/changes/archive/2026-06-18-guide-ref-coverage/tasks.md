# Tasks: guide-ref-coverage

## 有序任务列表

1. - [x] **实现比例族覆盖检测工具函数**
   - 在 `stamp-album.tsx` 内定义 `RATIO_FAMILIES`（竖版/方形/横版三族阈值）
   - 写 `computeCoverage(refIds, assets): Set<familyId>` 纯函数
   - 依赖：Asset.width / Asset.height 字段（已存在）
   - 可验证：给定一组不同比例的 asset，函数返回正确的覆盖 Set

2. - [x] **渲染比例族引导图章组件 `<RatioFamilyStamps>`**
   - 3个图章卡片（竖版/方形/横版），以 CSS aspect-ratio 呈现对应比例框
   - 覆盖态：accent 边框 + ✓ 角标；未覆盖态：虚线灰色边框
   - 置于参考图行内，上传图缩略图右侧（或单独一行）
   - 可验证：截图可见3个图章随覆盖状态正确变色

3. - [x] **参考图区域头部添加最佳覆盖徽章**
   - 3族全覆盖时在「参考图」标题旁显示 `覆盖最佳 ✓`（accent 色 badge）
   - 未全覆时显示 `建议竖版 / 方形 / 横版各一张` 灰色提示
   - 可验证：切换不同比例参考图，徽章状态实时响应

4. - [x] **更新 spec 文档**（本文件完成后执行）
   - 在 `specs/stamp-album-ref-guidance/spec.md` 补充 ADDED Requirements

## 并行性

任务 1 → 任务 2、3 依赖（需先有检测函数）；任务 4 独立可并行。
