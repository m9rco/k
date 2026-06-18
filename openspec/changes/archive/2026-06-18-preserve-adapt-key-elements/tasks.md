## 1. 判官 prompt：hints 过滤 + key_elements_fidelity 维度（internal/vision/quality.go）

- [x] 1.1 `qualityPrompt` 新增 hints 过滤规则：若【目标规格】含「无文案」，hints SHALL NOT 建议补充纯文案要素；若含「仅 logo」，hints 仅可提 LOGO
- [x] 1.2 `qualityPrompt` 增第 6 维 `key_elements_fidelity`（0-100）：依据【宣发主题约束】「必须保留」清单核对核心主体/LOGO 是否在画面内、要求保留文字是否存在且字符正确（非糊化/改写/乱码）；按 placement 约定过滤文案要素；JSON schema 增字段
- [x] 1.3 `rawVerdict`/`QualityVerdict.DimScores` 增 `KeyElementsFidelity`；`evaluate()` 增硬红线：低于 `KEY_ELEMENTS_FIDELITY_MIN`（缺省 60）直接 `Pass=false`，原因「核心主体/LOGO 缺失或文字被改写」
- [x] 1.4 `quality.check` 日志增 `key_elements_fidelity` 字段

## 2. 配置（internal/config）

- [x] 2.1 新增 `KEY_ELEMENTS_FIDELITY_MIN`（缺省 60，0 关闭该红线）

## 3. 重生复检择优（internal/generation/service.go）

- [x] 3.1 首检不及格分支保存首检版字节与 `verdict.Total`
- [x] 3.2 重生产物（`Attempt=1`）收敛后增一次判官打分（复用 35s 超时 + 降级为及格逻辑，不触发第三轮生图）
- [x] 3.3 比较首检版与重生版 `Total`，落库总分更高者；相等取重生版；两版均未过红线仍择优交付
- [x] 3.4 确认封顶：最多两轮生图、最多两次判官调用，无循环

## 4. 测试

- [x] 4.1 `internal/vision`：表驱动单测——必保红线触发（TestEvaluateKeyElementsFidelityRedLine）、红线关闭（TestEvaluateKeyElementsFidelityDisabled）、默认值 60、parse 失败降级为及格（既有）
- [x] 4.2 `internal/generation`：重生复检择优——重生更差回退首检版（TestQualityGateRegenWorseRevertsToFirst，断言落库为红色首检版）；既有重生测试更新为 2 次复检
- [x] 4.3 复刻缺陷回归：c0fbd56/8825（高分掩盖、文字改写 → key_elements_fidelity 红线）、caba3ad（重生更差盲发 → 择优回退首检版）
- [x] 4.4 `go build ./...` 与 `go test ./internal/vision/... ./internal/generation/...` 全绿（`TestToolsBuildWhitelist` 为 main 上既有失败，与本 change 无关）

## 5. 收尾

- [x] 5.1 `openspec validate preserve-adapt-key-elements --strict` 通过
- [x] 5.2 全部 tasks 勾选
