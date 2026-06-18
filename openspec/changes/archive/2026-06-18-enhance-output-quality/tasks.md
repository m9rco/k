# Tasks: enhance-output-quality

## Implementation Order

- [x] **T1** 新增 `QUALITY_MAX_RETRY` / `VIDEO_PROMPT_LLM_MODEL` 配置读取
  - `internal/config/config.go`：新增 `QualityMaxRetry`（QUALITY_MAX_RETRY，默认 2）、`VideoPromptLLMModel`（VIDEO_PROMPT_LLM_MODEL，默认 claude-haiku-4-5-20251001）。注：`QUALITY_MODEL` 已由现有 `resolveEndpoint("QUALITY",...)` 读取（cfg.Quality.Model），模型可配置天然支持
  - `internal/config/config_test.go`：验证默认值与覆盖行为 ✅
  - _可独立完成，其余任务依赖此项_

- [x] **T2** 质检 prompt 新增 `ad_appeal` 维度
  - `internal/vision/quality.go`：更新 `qualityPrompt` 常量（新增第 7 维度说明 + JSON schema）、`rawVerdict.Scores` 新增 `AdAppeal int`、`QualityVerdict.DimScores` 新增 `AdAppeal int`、Check 日志新增 ad_appeal
  - `internal/vision/quality.go`：解析新字段；当 total 通过但 ad_appeal ∈ (0,50) 时将吸引力建议追加到 `Hints`（不影响 pass/fail）
  - `internal/vision/quality_test.go`：TestEvaluateAdAppealLowAddsHint / TestEvaluateAdAppealHighNoHint ✅
  - _依赖 T1_

- [x] **T3** 质检重试上限提升（1 → 2，可配置）
  - `internal/generation/service.go`：新增 `maxRetry` 字段 + `SetMaxRetry`；Attempt==1 失败且 maxRetry≥2 时触发第 2 次（hints 拼接）；新增 Attempt==2 三路 bestOf 取最优产物
  - `internal/generation/quality_gate_test.go`：TestQualityGateSecondRetryWhenBothFail / TestQualityGateSecondRetryDisabledByMaxRetry1；既有单次重试测试 pin maxRetry=1 保留原意 ✅
  - _依赖 T1_

- [x] **T4** 普通生图（换角色/背景/文案/加角色）接入质量门控
  - `internal/generation/service.go`：新增 `isQualityGatedKind` + `qualitySpecLabel`；质检门控条件从 `==EditAdaptPlatform` 改为 `isQualityGatedKind`；普通生图 themeReport 传空、specLabel 用 Kind 名；generate_icon/text_to_image 不纳入
  - `internal/generation/quality_gate_test.go`：TestQualityGatePlainGenerateChangeBackground ✅
  - _依赖 T1、T2、T3_

- [x] **T5** 生视频 Prompt LLM 扩写
  - `internal/video/service.go`：新增 `PromptEnricher` 接口 + `SetPromptEnricher` + `Params.ThemeReport`；run 在组装 prompt 前调用 enricher，5s 超时降级
  - `internal/video/enricher.go`：`NewLLMEnricher`（OpenAI 兼容 chat），cmd/server/main.go 用 ChatPrimary 凭证 + VideoPromptLLMModel 接入
  - `internal/video/service_test.go`：TestPromptEnricherRichensMotion / TestPromptEnricherFallsBackOnError ✅
  - _依赖 T1_

- [x] **T6** 视频源图质检（代理质检 Phase 1）
  - `internal/video/service.go`：新增 `VideoQualityChecker` 接口 + `VideoQualitySignal` + `SetVideoQualityChecker`；run 在提交前用源图字节调用质检（10s 超时），hints 注入 enricher themeCtx 兜底
  - `cmd/server/main.go`：`videoQCAdapter` 复用 vision.QualityChecker
  - `internal/video/service_test.go`：TestVideoQualityCheckerHintsPassedToEnricher / TestVideoQualityCheckerNotConfiguredSkips ✅
  - _依赖 T2、T5_

- [x] **T7** 端到端验收与清理
  - `go test ./internal/generation/... ./internal/video/... ./internal/vision/... ./internal/config/...` 全绿 ✅
  - gofmt 全部 clean ✅
  - 无临时调试日志 / TODO 遗留 ✅
  - 注：`internal/agent.TestToolsBuildWhitelist` 为预先存在失败（stash 后干净 tree 同样 expected 6 got 5，依赖本地 .env provider 配置），与本 change 无关

## Dependencies
```
T1 → T2, T3, T5
T2 → T4, T6
T3 → T4
T5 → T6
T1+T2+T3+T4 可并行于 T5+T6
T7 最后
```
