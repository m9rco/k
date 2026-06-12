# Design: Expand Platform Catalog

## Context
现有尺寸目录是两层平铺 + name 全局唯一寻址，无法承载真实的 23+ 渠道、上百条尺寸、且带格式/大小/语义约束的投放规范。本设计把目录升级为三层、改为唯一 id 寻址，并把约束作为非强制的元数据透传。决策已与用户确认：**全量渠道、引入素材类型层、约束仅作元数据+提示（不强制裁剪满足）**。

## Goals / Non-Goals
**Goals**
- 三层目录 `渠道 → 素材类型 → 尺寸`，数据驱动（改 JSON 不改代码）。
- 尺寸唯一 id，裁剪与工具调用按 id 寻址，消除跨渠道 name 冲突。
- 约束（format/maxKB/note）作为元数据透传到前端与 Agent，供展示与提示。
- 前端分层选择器，能在上百条尺寸里高效浏览、跨渠道多选。
- 视频/外链类规格标注不可裁剪，UI 置灰、工具拒绝。

**Non-Goals**
- 不做约束的强制满足（自动压缩到 maxKB、自动转 format、加圆角/透明底）。裁剪仍是纯 cover-crop + 原格式（或按 format 提示）。
- 不实现生视频。
- 不针对 H5 超长图做特殊裁剪策略（仅录入尺寸）。

## Data Model

### 三层结构
```jsonc
// configs/channels.json
{
  "channels": [
    {
      "id": "taptap",
      "name": "TapTap",
      "group": "外渠",                // 速查分组：外渠/手机厂商/腾讯内渠/PC
      "assetTypes": [
        {
          "type": "screenshot",
          "name": "截图（5图）",
          "sizes": [
            { "id": "taptap.screenshot.landscape", "name": "横版 16:9",
              "width": 1280, "height": 720, "orientation": "landscape",
              "format": "png", "maxKB": 0, "note": "≥1280×720", "producible": true },
            { "id": "taptap.screenshot.portrait", "name": "竖版 9:16",
              "width": 720, "height": 1280, "orientation": "portrait",
              "format": "png", "maxKB": 0, "note": "≥720×1280", "producible": true }
          ]
        },
        {
          "type": "icon", "name": "ICON",
          "sizes": [
            { "id": "taptap.icon.512", "name": "ICON 512",
              "width": 512, "height": 512, "orientation": "square",
              "format": "png", "maxKB": 0, "note": "", "producible": true }
          ]
        }
        // video 类：producible=false（如视频本体/腾讯视频链接），仅保留视频封面这类图片
      ]
    }
  ]
}
```

### Go 类型（`internal/config`）
```go
type Size struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Width       int    `json:"width"`
    Height      int    `json:"height"`
    Orientation string `json:"orientation"`
    Format      string `json:"format,omitempty"`     // png/jpg/gif，空=不限
    MaxKB       int    `json:"maxKB,omitempty"`       // 0=无限制
    Note        string `json:"note,omitempty"`        // "无文案"/"圆角"/"透明底" 等
    Producible  bool   `json:"producible"`            // 纯裁剪能否产出；false→UI置灰/工具拒绝
}

type AssetType struct {
    Type  string `json:"type"`   // screenshot/icon/cover/banner/resource/h5
    Name  string `json:"name"`   // 中文展示名
    Sizes []Size `json:"sizes"`
}

type Channel struct {
    ID         string      `json:"id"`
    Name       string      `json:"name"`
    Group      string      `json:"group"`
    AssetTypes []AssetType `json:"assetTypes"`
}
```

### id 命名约定
`<channelId>.<assetType>.<slug>`，slug 取尺寸语义（如 `512`、`landscape`、`banner-1120x280`）。保证全局唯一、可读、稳定（写进 asset 记录与前端选中态）。

## Addressing 变更（核心）
现有 `resolveSizes(names)` 用 `map[name]Size` 跨平台查找，name 冲突即误裁。改为：
- 构建 `map[id]Size`（启动时一次性扁平化整个目录）。
- `CropToSizes(sessionID, sourceAssetID, sizeIDs []string)`：按 id 查；命中 `producible=false` 直接报错（而非静默跳过），让 Agent/前端拿到明确反馈。
- 产物 `CropResult` 增加 `SizeID` 与 `ChannelID`，便于工作区与下载按渠道归类（下载打包可按渠道分目录，后续可选）。

## 约束的处理（元数据+提示，不强制）
- `format/maxKB/note` 随尺寸透传到 `/api/platforms`（沿用路径或新增 `/api/channels`）与 `list_platform_sizes`。
- 前端：胶囊上以小字/角标展示 `note`（如「无文案」），tooltip 展示 `format`、`≤NKB`。
- 裁剪本身：
  - 输出格式：若 `format` 指定且与源不同，按 format 编码（png↔jpg 已支持；gif 不转，回退 png 并在结果里标注 `formatMismatch`）。
  - `maxKB`：**不强制**。仅在结果里附 `bytes`，超限给一个非阻断提示（前端角标/Agent 文案），把"压到多少"留给人决定。
- 这样保持 `crop` 包"纯尺寸处理"的单一职责，复杂度不上升。

## 前端分层选择器
现有：拉 `/api/platforms` → 平台分组 → 一行胶囊全铺 → 多选裁剪。
改为：
1. 拉三层目录（一次）。
2. 选择器顶部：渠道搜索框 + 渠道分组（按 `group`：外渠/手机厂商/腾讯内渠/PC）。
3. 选中渠道 → 右侧按 `assetType` 分组展示该渠道尺寸胶囊；`producible=false` 置灰不可选。
4. 已选尺寸聚合到底部"已选 N 项"区，支持跨渠道累加，确认后一次 `crop_to_sizes(size_ids)`。
5. 胶囊文案：`name`（如「横版 16:9」）+ `width×height` + 约束角标。

布局示意：
```
┌ 选尺寸 ───────────────────────────────┐
│ [搜索渠道…]   外渠 手机厂商 腾讯内渠 PC │  ← 渠道筛选
├──────────────┬────────────────────────┤
│ TapTap     › │ 截图  [横版16:9 1280×720]│
│ B站          │       [竖版9:16 720×1280]│
│ 4399         │ ICON  [512×512]          │
│ 好游快爆     │ 推广图[1920×1080 无文案] │
│ …            │ 视频  [✕1280×720 不可裁] │  ← producible=false 置灰
├──────────────┴────────────────────────┤
│ 已选 3 项： taptap.screenshot.landscape …│
│                              [确认裁剪] │
└────────────────────────────────────────┘
```

## Migration / Backward Compatibility
- `configs/platforms.json` → 新增 `configs/channels.json`（全量）。保留 `config.Load` 对旧两层结构的兼容解析：若读到旧 `platforms` 字段，包装成单一 assetType 注入（产出旧行为），便于平滑替换与回退。
- API：保留 `GET /api/platforms` 返回新三层结构（字段向后兼容地扩展）；如前端改动较大可同时暴露 `GET /api/channels`（语义更准）。本提案倾向**复用现有路径、扩展返回体**，减少前端改动面与路由蔓延。
- 工具：`crop_to_sizes` 入参 `size_names` → `size_ids`。这是 break，但工具仅服务端 Agent 调用，无外部消费者，直接切换。

## Risks / Trade-offs
- **数据录入量大且易错**：上百条尺寸手工录入。缓解：按 `docs/gen_size.md` 的速查表（ICON 汇总、视频汇总）交叉校验；apply 阶段加一个 `config` 单测校验 id 唯一、尺寸>0、producible 字段齐全。
- **cover-crop 对极端比例素材会裁掉主体**：如 1950×500 推广图、728×90 leaderboard。本提案不解决（沿用现状），仅在 design 标注；后续可引入 fit/留白策略作为独立 change。
- **约束不强制可能让产物不达标**（超 maxKB、格式不符）：明确为本提案 Non-Goal，用提示而非阻断；强制满足留作后续。

## Open Questions
- 下载打包是否按渠道分目录命名（如 `TapTap/截图_横版.png`）？倾向后续 change，本次不动 download-packaging。
- `/api/platforms` 复用 vs 新增 `/api/channels`：本设计取复用+扩展，apply 时若前端实现更顺可再议。
