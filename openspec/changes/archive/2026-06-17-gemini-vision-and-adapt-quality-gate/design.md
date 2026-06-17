# Design: gemini-vision-and-adapt-quality-gate

## Research：channels.json 与现有模型选型（用户问题 3）

### channels.json 尺寸格局

27 个渠道（`group` 分四类：外渠 / 手机厂商 / 腾讯内渠 / PC），尺寸跨度极大：

- **方形 ICON**：`100×100`、`512×512`、`216×216` …
- **横版 banner/截图**：`1280×720`、`1920×1080`、`1950×500`、`2000×400`（极端 ~5:1）
- **竖版**：`720×1280`、`1080×1920`、`828×1500`
- **超极端比例**：`1008×168`（~6:1）、`2080×828`（~2.5:1）

同一张源图要适配到从 1:1 到 6:1 的各种比例，**这是为什么需要多套图像模型**：

| 模型 | 角色 | 选用原因 |
|---|---|---|
| `gpt-image-2`（IMAGE_PRIMARY/BACKUP） | 适配 AI 重绘主力 | 图生图编辑质量最佳，主体保真度高；双供应商失效切换 |
| `gemini-3-pro-image` / `gemini-3.1-flash-image` | 重绘 fallback + outpaint 收敛 | Gemini 原生 inline，处理任意比例/扩图（outpaint）干净，补全极端 banner 空带 |
| `grok-4-fast`（现状） | 视觉宣发要素分析 | 多模态 chat，理解宣发素材语义，产出结构化文本约束 |

收敛逻辑（`adapt.go`）：比例差 ≤ 0.18（log-ratio）→ 缩放；> 0.18 → outpaint 扩图（Gemini）。所以极端 banner 必须靠 Gemini 扩图，gpt-image-2 出图后比例不够再交 Gemini 补。

## Decision 1：Gemini 视觉分析走原生 inline，不复用 OpenAI-compat 路径

### 方案对比

| 方案 | COS 依赖 | 改动 | 选择 |
|---|---|---|---|
| A. `vision.Analyzer` 仍用 image_url，传 data URI | 否（若代理支持 data URI） | 小 | 风险：yunwu 代理对 data URI 支持不确定 |
| **B. 新建 `GeminiVisionAnalyzer` 用 `:generateContent` inline** | **否** | 中 | **采用** |

选 B：复用 `generation/gemini.go` 已验证的 `:generateContent` + `inlineData` 调用范式，把图片字节直接 base64 inline 传入，文本 prompt 作为 text part，响应取 `candidates[].content.parts[].text`。这是 Gemini 官方多模态理解的标准用法，与图像生成对称，凭证/endpoint 解析复用现有 provider 三层回退。

### 配置

- `VISION_PROVIDER`（默认 `gemini`）、`VISION_MODEL`（默认 `gemini-flash-latest`）、`VISION_BASE_URL`/`VISION_API_KEY`（三层回退到 COMMON）。
- `VISION_PROVIDER=gemini` → 新 `GeminiVisionAnalyzer`（inline，无需 COS）。
- `VISION_PROVIDER=openai` → 保留现有 `vision.Analyzer`（image_url，需 COS），向后兼容。
- `main.go` 据 `VISION_PROVIDER` 选型；接口抽象为 `VisionAnalyzer interface { Configured() bool; Analyze(ctx, images, onChunk) (string, error) }`，但 inline 版接受 `[]imageBlob{data,mime}` 而非 URL。

### COS 关系

COS 从「视觉分析硬依赖」降级为「可选优化」：上传预热和 reanalyze（需 URL）保留；inline 分析不依赖它。COS 未配置时 inline 分析照常工作。

## Decision 2：质量门控位置 = service.go 收敛后、持久化前

### 流程（审核1次 + 重生1次封顶）

```
AI 重绘出图(gpt-image-2) → 收敛到目标尺寸 →
  [质量门控 EditAdaptPlatform 且 attempt==0]
    → 推送 review_started 事件（前端进入"审核中"态）
    doubao-seed-1-6-vision-250815 打分（inline base64 / data URI）
    ├─ 合规红线命中 OR 总分 < 阈值(默认75)
    │     → 推送 review_failed 事件（前端打✗，标"按建议重绘中"）
    │     → 把红线原因 + 低分维度 hints 注入图生图提示
    │     → 重走完整生图流程一次（gpt-image-2, attempt=1, 同一 taskID）
    │     → 重生产物【不再审核】（封顶）→ 持久化 → task_done
    └─ 合规通过 且 总分 ≥ 阈值
          → 推送 review_passed 事件（前端打✓）→ 持久化 → task_done
  [judge 异常/超时/未配置] → 推送 review_skipped（前端静默收起审核态）→ 按原产物持久化
```

落点在 `service.go` 的 `run()`：`EditAdaptPlatform` 收敛分支之后、`InsertAsset` 之前。理由：此时已有最终像素，judge 看到的就是用户将看到的产物；重生只需在同一 `run` 内带 hints 把出图+收敛整段重跑一次。**全程复用同一 taskID**：前端始终看到「生图中」的同一张占位卡片，审核态作为该卡片上的子状态演进，不新增任务、不切卡片。

### 重试语义（已确认）

- 每个适配产物**最多审核一次**、**最多重生一次**（封顶 2 轮 gpt-image-2）。
- 重生产物直接作为最终结果持久化，**不二次审核**，杜绝循环、绝不卡死。
- 用 `attempt int`（0=首次，1=重生）标记，`attempt>=1` 时跳过门控。

### 打分契约（合规硬红线 + 加权总分）

judge prompt（服务端固定）携带：目标渠道/尺寸、themeReport（宣发主体真值）、产物图片（inline）。要求模型只输出结构化 JSON：

```json
{
  "compliance": {"pass": true, "violations": ["..."]},
  "scores": {"subject_consistency": 0-100, "character_appeal": 0-100, "overall_quality": 0-100},
  "total": 0-100,
  "hints": "重绘时应强化的要点，一句话"
}
```

判定（服务端，不信任模型自报 verdict）：

- `compliance.pass == false` → **一票否决**，直接判不及格（红线优先于任何分数）。
- 否则 `total < QUALITY_THRESHOLD`（默认 75）→ 不及格。
- 否则 → 及格。
- 不及格时 `hints` + `compliance.violations` + 最低分维度名 一起注入重生 prompt。
- JSON 解析失败 / 超时（默认 30s）/ 未配置 → 视为及格（降级，不阻塞，推送 review_skipped）。

### 审核态事件协议（下发前端，不留空白）

复用 SSE 任务流，新增 4 个加法式事件（旧客户端忽略即可，不影响 task_done）：

| 事件 | 触发 | 前端表现 |
|---|---|---|
| `review_started` | judge 开始打分 | 占位卡片显示「🔍 审核中…」，不让进度条空转 |
| `review_passed` | 及格 | 卡片闪现 ✓「审核通过」，随后 task_done 替换为产物 |
| `review_failed` | 不及格触发重生 | 卡片显示 ✗ + 红线/低分原因 + 「按建议重绘中…」，进度回退到生图态 |
| `review_skipped` | 降级 | 静默收起审核态，按普通生图中继续 |

事件 Data 携带：`taskId`、`attempt`、（passed/failed 时）`total` 分数与简短 `reasons`（红线/低分维度）。前端在同一占位卡片上演进 审核中 → ✓/✗ → （✗ 时）重绘中 → 产物，**全程无空白**。重生走完后正常 task_done。

### doubao 调用方式

`doubao-seed-1-6-vision-250815` = Volcengine ARK 视觉模型，OpenAI 兼容 `/chat/completions`。产物图片在本地磁盘，转 `data:image/png;base64,...` 作为 `image_url` 传入（ARK 支持 data URI），无需 COS。新建 `internal/vision/quality.go`，与 `vision.Analyzer` 同构但 prompt 不同、输出走 JSON 解析、非流式。

### 配置

- `QUALITY_PROVIDER`（默认 `openai`）、`QUALITY_MODEL`（默认 `doubao-seed-1-6-vision-250815`）、`QUALITY_BASE_URL`/`QUALITY_API_KEY`（三层回退）、`QUALITY_THRESHOLD`（默认 75）。
- `QUALITY_API_KEY` 为空 → 质量门控整体禁用（degrade to 及格），适配行为与现状一致。

## Open Questions 解答

1. **Gemini 经代理还是直连**：复用 provider 三层回退；默认走 COMMON（yunwu）。yunwu 若把 Gemini 暴露为 `:generateContent` 则直接可用；`gemini.go` 的 baseURL 已处理 `/v1`/`/v1beta` 后缀剥离。
2. **doubao data URI**：ARK OpenAI-compat 支持 `image_url` 传 data URI，故无需 COS。
3. **超时**：judge 默认 30s 超时，超时即降级为 pass，绝不让门控拖慢或卡死适配。

## Risks

- judge 误判（把好图判不及格）→ 浪费一次重绘。缓解：合规红线之外的分数阈值不宜过高（默认 75），且最多重生一次。
- 重生仍不及格但已封顶 → 直接产出。可接受：避免循环/卡死优先于追求完美；前端仍打✓（流程完成），分数细节不强行暴露给用户造成困惑。
- gemini-flash-latest 模型 ID 在目标网关不存在 → 视觉分析失败降级（现有降级路径覆盖）。上线前需用真实凭证验证模型 ID 可用。
- 审核+重生使适配单产物耗时翻倍（最坏 2 轮 gpt-image-2 + 1 次 judge）。缓解：审核态事件持续下发，前端无空白；judge 30s 超时兜底。
