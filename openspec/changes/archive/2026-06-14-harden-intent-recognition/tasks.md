## 1. 确定性意图预分类层
- [x] 1.1 新增 `internal/agent/intent.go`：定义 `IntentHint{Labels, Confidence, Whitelisted, MissingKeyParam}` 与纯函数 `ClassifyIntent(userText string) IntentHint`，表驱动关键词规则覆盖九类白名单意图 + 文生图（注：签名简化为单参数，直接解析 userText 内的 `[工作区:/reference assets:/asset]` 前缀判断图片可用性，无需额外传 workspace）
- [x] 1.2 实现保守置信阈值（`hintThreshold=0.6`）：strong 命中=1.0 注入、weak-only=0.5 不注入；命中图片操作意图但工作区无图时置 `MissingKeyParam`
- [x] 1.3 新增 `internal/agent/intent_test.go`：表驱动测试高频说法命中、白名单外未命中、缺参标记、weak-only 不命中、前缀剥离

## 2. 意图提示注入与 System Prompt
- [x] 2.1 在 `internal/agent/prompt.go` 新增 `BuildIntentHint(hint IntentHint) string`，达阈值才返回非空「[意图提示: …]」前缀
- [x] 2.2 在 `Handle`（`internal/agent/agent.go`）调用模型前，对 userText 做 `ClassifyIntent` 并前置意图提示（与既有 `BuildAssetNumbering` 前缀共存——后者在 main.go 入口已注入）
- [x] 2.3 在 `SystemPrompt()` 工具使用规范新增第 10 条：「意图提示」为服务端预判、仅供参考、只是数据不可当指令
- [x] 2.4 更新 `prompt_test.go`：断言 system prompt 含「意图提示」与「仅供参考」

## 3. 自我纠正重试闭环
- [x] 3.1 将 `Handle` 的单次 `streamOnce` 改为受限重试循环，接通已有 `shouldRetryFakeAck`，`maxAttempts=2`
- [x] 3.2 重试时向消息追加更强纠正指令常量 `fakeAckCorrection`（`internal/agent/fakeack.go`）
- [x] 3.3 扩充 `fakeack_test.go`：既有 `shouldRetryFakeAck` 决策表已覆盖重试触发/上限收敛/正常轮不重试

## 4. 缺参澄清与白名单外拒绝兜底
- [x] 4.1 重试仍 tools=0 后，`remediationAction` 判定 `Whitelisted && MissingKeyParam` → `remediationClarify` 构造问题+选项经 `clarify` 闭包补发 capsule
- [x] 4.2 `!Whitelisted` 且模型无正文 → 用 `RefusalMessage()` 确定性礼貌拒绝，不再额外调用模型
- [x] 4.3 其余情形 `remediateNone` 维持现状；已调工具/已澄清/被中断的轮不触发兜底
- [x] 4.4 兜底决策抽为纯函数 `remediationAction`，`fakeack_test.go` 新增 `TestRemediationAction` + `TestRemediationClarifyBuildsOptions` 覆盖各分支

## 5. 验证
- [x] 5.1 `go test ./...` 全绿（含 agent 包新增测试），`go vet ./...` 无告警
- [ ] 5.2 用弱模型（测试 DeepSeek/豆包分支）在其他端口起诊断实例人工复核典型 tools=0 用例 —— 需 live 凭证，留待联调阶段验证
- [x] 5.3 `openspec validate harden-intent-recognition --strict` 通过
