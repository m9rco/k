# Design: enhance-output-quality

## D1 质检模型升级策略

### 现状
`vision/quality.go` 的 `QualityChecker` 已支持双传输层（Gemini native `generateContent` + OpenAI-compat `chat/completions`），通过模型名称自动路由。`doubao-seed-1-6-vision-250815` 走 OpenAI-compat 路径。

### 决策：可配置升级，不强制替换
- 引入环境变量 `QUALITY_MODEL`（默认保持 `doubao-seed-1-6-vision-250815`）
- 当 `QUALITY_MODEL` 包含 `gemini` 时，自动走 Gemini native 路径（已有代码分支，零变更）
- **无需新适配器**：`vision.NewQualityChecker` 已按模型名自动选路

```
QUALITY_MODEL=gemini-2.5-flash-all   →  走 Gemini generateContent + responseMimeType=application/json
QUALITY_MODEL=doubao-seed-1-6-vision →  走 OpenAI-compat chat/completions (当前默认)
```

### 为什么 Gemini 2.5 Flash 更优
1. 同项目 `marketing-analysis` 已用 `gemini-2.5-flash-all` 做 inline 分析，传输层成熟
2. Gemini 能强制 `responseMimeType=application/json`，避免 OpenAI-compat 路径有时输出带 markdown fence 的 JSON 需要额外清洗
3. `inlineData` 传图无需 COS，与 marketing analysis 路径一致

---

## D2 审美维度扩展

### 新增维度：ad_appeal（宣发吸引力，0-100）
评估该素材是否具备让用户在信息流中驻足的视觉冲击力：
- 主体是否醒目、占据视觉重心
- 色彩对比是否鲜明（非突兀）、有层次感
- 构图是否符合「黄金分割/三分法」等广告构图惯例
- 整体是否达到「投放级」而非「设计稿级」

**不纳入总分聚合**（避免大幅改变现有评分基线），仅作为**附加信号**：
- 记录到资产元数据（telemetry）
- 当 total ≥ threshold 但 ad_appeal < 50 时，在产物 hint 中追加一条「吸引力」建议（不触发重生成，避免额外成本）

### rawVerdict 变更（仅增量字段）
```go
type rawVerdict struct {
    // 现有字段不变 ...
    Scores struct {
        // 现有 5 个不变 ...
        AdAppeal int `json:"ad_appeal"` // 新增
    } `json:"scores"`
}
```

---

## D3 质检重试上限 1→2

### 当前逻辑
`service.go` 中 `p.Attempt` 字段：
- `Attempt==0`：首次生成，质检失败 → 创建 `retry` with `Attempt=1`
- `Attempt==1`：第二次生成（首次 hint 注入），不再质检

### 变更后逻辑
- `Attempt==0`：首次；失败 → retry with `Attempt=1`，hints=V1_hints
- `Attempt==1`：第二次；失败 → retry with `Attempt=2`，hints=V1_hints + V2_hints（拼接）
- `Attempt==2`：第三次（最终），不再质检，直接取最高得分产物

**取最优产物**：已有 `FirstAttemptData`/`FirstAttemptTotal` 机制，扩展为三路比较取最高 total 分。

---

## D4 普通生图接入质检

### 范围
- 意图：`change_character`, `change_background`, `change_text`, `add_character`
- 不含 `generate_icon`（icon 尺寸小，质检 prompt 语义不适用）、`text_to_image`（无参考图，主体一致性无法评估）

### 实现策略
`adapt_platform` 路径已有完整质检流程（pass/fail → retry with hints），可直接复用。普通生图：
- 没有 `themeReport`（视觉分析阶段不走），传空字符串（质检 prompt 已处理空 themeReport 情况）
- `specLabel` 用 `slots.Kind`（如 "change_character"）
- 质检通过/失败逻辑与 adapt 路径一致

---

## D5 视频首末帧质检

### 方案选择

| 方案 | 优点 | 缺点 | 决策 |
|------|------|------|------|
| A: ffmpeg 提取帧 | 精确，任意时间点 | 需要 ffmpeg 二进制依赖；不想引入系统依赖 | ✗ |
| B: Go 纯净 mp4 demux | 无依赖 | 实现复杂（Box 解析），工作量大 | ✗ |
| C: Gemini 直接理解视频 | 无需提取帧，Gemini 原生支持视频 inlineData | 视频文件可能较大（720P 5s ~5MB），Gemini inline 有大小限制；API 成本较高 | △ 降级备选 |
| D: 第一帧 = 源图（已持有）| 零成本，直接用源图做质检基础 | 只能检测「产物是否与源图一致」，不能检测视频内容 | ✓ 第一步 |

**决策**：Phase 1 用方案 D——将视频任务的 **源图**（`SourceAssetID` 对应的图片）作为「代理质检对象」，检验生视频任务的基础是否健壮（源图是否符合生视频的宣发要求）。同时提取 `specLabel = "video: {motion}"` 作为规格标签。

这不是完整的视频内容质检，但它是**零新依赖**的第一步，可在后续 change 扩展到真正的帧提取。

### VideoQualityChecker 接口
```go
// VideoQualityChecker scores a video task's source image as a proxy for the
// expected video output quality. Phase 1 uses the source asset image as the
// check subject.
type VideoQualityChecker interface {
    Configured() bool
    CheckVideoSource(ctx context.Context, srcImg []byte, mime, motion string) (VideoQualitySignal, error)
}

type VideoQualitySignal struct {
    SubjectScore int    // subject_consistency
    AppealScore  int    // ad_appeal (when checker supports it)
    Hints        string // improvement hints for prompt enrichment
}
```

---

## D6 生视频 Prompt LLM 扩写

### 动机
用户输入如「让角色走起来」→ 视频模型接收 prompt 信息量极少 → 动作生硬、背景未延续。

### 实现
在 `video.Service.Start` 中，motion description 组装最终 prompt **之前**，用 **claude-haiku-4-5-20251001**（现有 API，低延迟低成本）做 prompt 富化：

```
系统指令（服务端固定）:
你是游戏宣发视频运镜与动效描述专家。将用户的简短动作描述扩写为 2-3 句专业的图生视频 prompt（英文），涵盖：主体动作、镜头运动、节奏感、光影变化。不虚构游戏中不存在的元素。
用户输入: {sanitized_motion}
主题约束（可选）: {theme_report 精简版，如有}
```

**失败降级**：LLM 调用失败/超时（5s），直接使用原始 motion description，不阻断视频任务。

扩写结果缓存到 Params，方便 retry 时复用（不重复调用 LLM）。

---

## D7 配置变更汇总

新增环境变量（均有默认值，向后兼容）：
```
QUALITY_MODEL         # 质检模型，默认 doubao-seed-1-6-vision-250815
QUALITY_MAX_RETRY     # 最大重生成次数，默认 2（原为硬编码 1）
VIDEO_PROMPT_LLM      # 视频 prompt 扩写用的 LLM，默认 claude-haiku-4-5-20251001
```
