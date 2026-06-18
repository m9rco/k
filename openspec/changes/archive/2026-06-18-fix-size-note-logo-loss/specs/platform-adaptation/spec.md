# Specification: platform-adaptation (delta)

## ADDED Requirements

### Requirement: size note 明确指令展开

`rewriteSizeNote` SHALL 把 logo/文案相关的中文 note 展开为对图像模型无歧义的英文指令，防止模型把「无文案」误解为「无 logo」。展开规则：

| 输入 note（含子串匹配） | 输出英文指令 |
|---|---|
| `"无文案"`（不含 logo 相关修饰） | `"no marketing copy (no taglines, slogans or text overlays); keep the game LOGO fully visible and legible"` |
| `"仅 logo，无文案"` | `"show the game LOGO only — no marketing copy, taglines or text overlays"` |
| `"不带文案，带游戏 logo"` | `"include the game LOGO; no marketing copy or text overlays"` |
| `"不带游戏 logo"` / `"无 logo"` | `"no game LOGO; do not add or invent a logo"` |
| `"含清晰游戏 logo"` / `"带游戏 logo"` | `"include a clear, legible game LOGO"` |
| `"须带文案，突出游戏名"` | `"prominently include marketing copy and the game title"` |
| `"带文案和游戏 logo"` | `"include both marketing copy/game title and the game LOGO"` |
| `"LOGO 居中或偏右，无广告语"` | `"center or right-align the game LOGO; no advertising slogans"` |

非 logo/文案 note（`透明底`/`透明背景`/`圆角`/`安全区` 等）：原有处理逻辑不变。

#### Scenario: 无文案 note 保留 logo

- **WHEN** 平台适配目标 size 的 note 为 `"无文案"`
- **THEN** 生成的 prompt 包含明确的 LOGO 保留指令（`keep the game LOGO`）
- **AND** 不包含 `无文案` 原始中文字串

#### Scenario: 仅 logo note 抑制文案

- **WHEN** note 为 `"仅 logo，无文案"`
- **THEN** prompt 包含 `show the game LOGO only` 语义指令
- **AND** 包含抑制文案的明确措辞

#### Scenario: 不带 logo note 不添加 logo

- **WHEN** note 含 `"不带游戏 logo"` 或 `"无 logo"`
- **THEN** prompt 明确告知模型不添加 logo（`no game LOGO`）
