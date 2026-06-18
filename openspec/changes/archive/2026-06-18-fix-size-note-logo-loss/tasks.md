# Tasks: fix-size-note-logo-loss

1. **扩展 `rewriteSizeNote`，覆盖所有 logo/文案 note 变体**
   - 用 `strings.Contains` / 精确匹配处理各类 note
   - 输出明确英文指令（suppress copy / keep logo / no logo 等）
   - 可验证：`go test ./internal/generation/...` 全绿

2. **更新 / 新增测试**
   - `TestRewriteSizeNoteLogoAndCopy`：逐一覆盖 8 种 note 变体
   - `TestBuildPromptAdaptPlatformCoversSemantics`：断言改为检测英文展开字符串
   - 可验证：无回归，新增 cases 覆盖 `无文案`/`仅 logo，无文案`/`不带游戏 logo` 等

## 并行性

任务 1 → 任务 2（测试必须在实现后才能有意义断言）。
