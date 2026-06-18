# Proposal: fix-size-note-logo-loss

## Why

`taptap.cover.900x600`（`note: "无文案"`）适配产出一直缺少 logo 和宣发文案，而同尺寸的 `taptap.cover.caption`（无 note）产出正常。二者同为900×600，表面看像随机差异，实则是两个不同 sizeId 的设计差异被 prompt 放大为 bug。

## 根本原因

`rewriteSizeNote` 把 `"无文案"` 原样插入 prompt：
```
Respect this placement constraint: 无文案.
```
模型将这段中文宽泛地解读为「不显示任何文字图形要素（含 logo）」，而非仅抑制宣传文案/slogan。`reproportionHint` 仅在比例差较大时才注入显式的 `keep the LOGO` 指令；比例接近时（900×600 → 900×600）不触发，因此无任何兜底的 logo 保留指令。

## What Changes

### 受影响的 channels note 枚举

| sizeId 示例 | 当前 note | 期望行为 |
|---|---|---|
| `taptap.cover.900x600` | `无文案` | 抑制文案 slogan，**保留 logo** |
| `taptap.banner.community-1920x1080` | `仅 logo，无文案` | 只保留 logo，抑制文案 |
| `txgamephone.cover.1146x636` | `不带文案，带游戏 logo` | 只保留 logo，抑制文案 |
| `haoyoukuaibao.banner.guide-*` | `不带游戏 logo` | 不显示 logo |
| `bilibili.banner.pc-bg-1920x620` | `无 logo，无渐变蒙版…` | 不显示 logo |
| `game4399.banner.h5-1920x1080` | `含清晰游戏 logo` | 强调清晰 logo |
| `qqmusic.banner.zone-*` | `须带文案，突出游戏名` | 强调文案/游戏名 |
| `txgamephone.banner.*` | `带文案和游戏 logo` | 文案 + logo 都要 |

### 解决方案

在 `rewriteSizeNote`（`internal/generation/prompt.go`）中，对 logo/文案相关 note 做**中文→英文明确指令**展开，消除歧义：

- `"无文案"` → `"no marketing copy (no taglines, slogans or text overlays); keep the game LOGO fully visible and legible"`
- `"仅 logo，无文案"` → `"show the game LOGO only — no marketing copy, taglines or text overlays"`
- `"不带文案，带游戏 logo"` → `"include the game LOGO; no marketing copy or text overlays"`
- `"不带游戏 logo"` / `"无 logo"` → `"no game LOGO; do not add or invent a logo"`
- `"含清晰游戏 logo"` / `"带游戏 logo"` → `"include a clear, legible game LOGO"`
- `"须带文案，突出游戏名"` → `"prominently include marketing copy and the game title"`
- `"带文案和游戏 logo"` → `"include both marketing copy/game title and the game LOGO"`
- `"LOGO 居中或偏右，无广告语"` → `"center or right-align the game LOGO; no advertising slogans"`
- 其余 note（`透明底` / `圆角` / `安全区` 等）：原有逻辑不变

### 测试更新

- `TestBuildPromptAdaptPlatformCoversSemantics`：`SizeNote` 改为展开后的英文预期字符串，或改为传入原始 note 并断言英文输出出现
- 新增 `TestRewriteSizeNoteLogoAndCopy`：覆盖所有 note 变体的输入→输出映射

## 影响范围

仅 `internal/generation/prompt.go`（`rewriteSizeNote` 函数）+ 相关测试。不涉及 API、前端、数据库。
