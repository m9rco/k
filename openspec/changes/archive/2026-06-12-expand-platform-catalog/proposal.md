# Change: Expand Platform Catalog（全量渠道素材规格 — 三层目录 + 分层选择）

## Why
当前裁剪能力的平台尺寸是一份内置的「平台 → 尺寸」两层平铺数据（`configs/platforms.json`，仅 4 个通用平台共 12 个尺寸），且裁剪靠 **size name 全局唯一** 寻址。

而团队真实的投放规范（`docs/gen_size.md`）是 23+ 个发行渠道（TapTap、B站、4399、好游快爆、QQ、微信、WeGame、华为/OPPO/vivo/小米/荣耀、腾讯系内渠、iOS、抖音、快手……）、每个渠道下分多种**素材类型**（截图 / ICON / 视频封面 / 推广图·banner / 资源位 / H5），共上百条尺寸，并带格式（PNG/JPG/GIF）、文件大小上限、圆角/透明底/无文案等约束。

把真实规范接进来会撞三个结构性问题：
1. **size name 必然冲突**：512×512 的 ICON 在 TapTap、4399、B站、小米、荣耀、抖音都出现；640×360、1280×720 等更是跨渠道复用。全局唯一 name 寻址不再成立。
2. **两层胶囊 UI 装不下上百尺寸**：现有「平台分组 → 一行胶囊」一次性铺开会爆屏，无法浏览。
3. **存在非图片裁剪能产出的规格**：视频尺寸、「提供腾讯视频链接」类不是纯裁剪能产出的，需要在数据层标注并在 UI/工具层排除或弱化。

## What Changes
- **image-cropping**（MODIFIED + ADDED）
  - 平台尺寸预设从两层升级为**三层目录**：`渠道(channel) → 素材类型(assetType) → 尺寸(size)`；每个尺寸带**全局唯一 `id`**（如 `taptap.icon.512`），裁剪改为按 id 寻址，彻底消除 name 冲突。
  - 尺寸新增**约束元数据**：`format`（png/jpg/gif）、`maxKB`、`note`（如「无文案」「圆角」「透明底」）。这些作为**提示/标注**透传给前端与 Agent，**不强制**裁剪满足（保持纯尺寸裁剪，复杂度不上升）。
  - 尺寸标注 `producible`（默认 true）：视频/外链类规格标 false，前端置灰、工具层拒绝，避免对纯裁剪发起无效请求。
  - 全量录入 `docs/gen_size.md` 中所有 **图片类** 规格（视频本体与外链不产出，但保留其「视频封面」这类可裁剪图片）。
- **frontend-experience**（ADDED）
  - 尺寸选择从「平台分组平铺胶囊」升级为**分层选择器**：先选渠道 → 按素材类型分组展示该渠道的尺寸胶囊 → 跨渠道多选后批量裁剪。支持渠道搜索/筛选，约束 `note`/`format`/`maxKB` 作为胶囊上的小标注或 tooltip。
- **conversation-orchestration**（MODIFIED）
  - `crop_to_sizes` 工具入参从 `size_names []string` 改为 `size_ids []string`（按唯一 id 寻址）。
  - `list_platform_sizes` 工具返回结构升级为三层（渠道 → 素材类型 → 尺寸含 id 与约束），并支持可选的 `channel` 过滤参数，避免一次性把上百条尺寸灌进模型 context。

## Impact
- Affected specs：
  - `image-cropping`（MODIFIED 平台尺寸预设；ADDED 素材类型与约束元数据、可裁剪标记）
  - `frontend-experience`（ADDED 分层尺寸选择器）
  - `conversation-orchestration`（MODIFIED 裁剪/列举工具接口）
- Affected code（apply 阶段）：
  - `internal/config/`：`Size` 增 `ID/AssetType/Format/MaxKB/Note/Producible` 字段；`Platform`→`Channel` 语义（保留向后兼容的 JSON 解析）。
  - `configs/platforms.json` → 重构为 `configs/channels.json`（全量渠道目录，数据驱动，改数据不改代码）。
  - `internal/crop/`：`resolveSizes` 改为按 id 寻址；`Service.Platforms()` 返回三层目录；过滤 `producible=false`。
  - `internal/agent/tools.go`：`crop_to_sizes` 与 `list_platform_sizes` 接口升级。
  - `web/static/`：`loadPlatforms()`、胶囊渲染、`cropToSizes()` 改为分层选择器 + id 寻址。
- 兼容性：旧 `platforms.json` 的两层结构在 apply 阶段提供一次性迁移/兼容解析；前端旧胶囊逻辑替换。
- 不在本提案范围：生视频、约束的强制满足（自动压缩/转格式）、H5 长图的特殊处理（仅录入尺寸，裁剪策略沿用 cover-crop）。
