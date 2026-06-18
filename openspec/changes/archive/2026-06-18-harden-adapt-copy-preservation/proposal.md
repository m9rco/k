# Change: 适配择优红线感知 + 中等比例差的保文案构图约束

## Why

`data/logs/app.log`（trace_0886668ffd608b1f）暴露两个问题，对应用户两个提问：

**Q1：同为 1920×1080，一个丢文案一个勉强及格。** 这是上一个 change（`preserve-adapt-key-elements`）引入的择优逻辑缺陷：

- `asset_94f0f50f5c557130`：首检 `key_elements_fidelity=100` total=99 一次过，文案在。
- `asset_e64eb12b865d5945`：首检 keyelem=30/total=80 ✗ → 重生 keyelem=25/total=83 → `recheck_accepted` 取了重生版。

两版**都没过 keyelem 红线**（30、25 均 < 60，文案都丢），但 `service.go` 的 recheck **只比 `total`**（`83 > 80` → 取重生），完全无视 `key_elements_fidelity`。`total` 被 subj/appeal/canvas（95-100）主导，文案丢了照样 80+。结果：
1. 两版都丢文案时**仍静默交付且报 `review_passed`**，用户收不到任何失败信号；
2. 甚至挑 keyelem **更差**的版本（日志中 `task_13dbcf62`：保留 keyelem=20 丢掉 25；`task_1aa95b64`：保留 25 丢掉 30）。

**Q2：900×600 必丢全部文案，900×500 却接近满足。** 源图 1920×1080 = 16:9 ≈ 1.778。

- 900×500（1.80，距源 **+1.2%**）：生成比例≈源，模型几乎不重排，文案保住。
- 900×600（1.50，距源 **−15.6%**）：16:9 强行重构成 3:2，横排 LOGO/大字/标签塞不下被丢。

`extremeRatioHint`（`prompt.go:430`）只在目标 ≥3:1 时注入安全区构图约束；900×600 这类**中等比例差**（超出裁剪快路径容差但未到极端档）**没有任何保文案约束**，模型自由重绘时优先保主体、牺牲文案。900×500 是比例侥幸接近，不是设计保证。

## What Changes

- **择优逻辑改为红线感知**（Q1，**BREAKING** 修改 recheck 判定）：首检版与重生版比较时，SHALL 先比红线通过性（通过 > 未通过），再比 `key_elements_fidelity`，最后才比 `total`。杜绝「两版都丢文案时挑 total 高/keyelem 更差的那版」。
- **两版均未过红线时下发诚实降级信号**（Q1）：当最终交付版仍未通过红线，SHALL 下发 `review_failed`（携带 `degraded:true` 与最终交付标记），而非伪报 `review_passed`。仍交付择优版本（沿用既有「不丢弃产物」决策），但前端与日志如实反映「带瑕疵交付」。
- **中等比例差注入保文案构图约束**（Q2，**BREAKING** 修改重绘提示）：当源图与目标的宽高比差异超出裁剪快路径容差、但目标未达极端档（`extremeRatioHint` 不触发的中间区间）时，重绘提示 SHALL 注入「重构画幅时保留并重排主体/LOGO/文案，使其在新比例下完整可见，不得丢弃」的约束。约束 SHALL 按 placement 文案约定过滤（「无文案」placement 只要求保主体/LOGO）。

## Impact

- Affected specs: `platform-adaptation`（`适配后质量打分门控与单次重生` MODIFIED；新增 `中等比例差的保文案重构约束`）
- Affected code:
  - `internal/generation/service.go`：recheck 改为红线感知比较（`bestOf`）、保存首检版完整 verdict（Pass + keyelem）、降级信号下发
  - `internal/generation/service.go` 的 `QualityVerdict` + `cmd/server/main.go` adapter：补 `KeyElementsFidelity` 字段
  - `internal/generation/prompt.go`：新增 `reproportionHint`，在 MODIFY 段注入中等比例差保文案约束；`Slots` 增 `SourceWidth/SourceHeight`
  - `internal/generation/service.go`：`run()` 在 BuildPrompt 前写入 `Slots.SourceWidth/Height`
  - 测试：`internal/generation` 表驱动单测 + 日志两问题回归
