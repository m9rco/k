# Design: pixel-quality-prefilter

## 算法选型

### 模糊检测：Laplacian 方差法

Laplacian 算子（3×3）对每个像素计算二阶导数之和，清晰图边缘多 → 响应大 → 方差高；模糊图边缘被平滑 → 响应小 → 方差低。

```
kernel:
 0  1  0
 1 -4  1
 0  1  0
```

实现步骤（纯 Go standard lib）：
1. 解码图片为 `image.Image`（支持 PNG/JPEG/WebP via golang.org/x/image）。
2. 转灰度（`color.GrayModel`）。
3. 对每个内部像素（排除 1px 边框）计算 Laplacian 值。
4. 计算所有 Laplacian 值的**方差**。
5. `variance < BlurThreshold` → 判模糊不及格。

**阈值校准**：宣发素材典型尺寸 720p–4K，AI 生图产物边缘丰富，推荐默认阈值 **80**（经验值；实测清晰的 AI 生图方差通常 ≥200，明显模糊 ≤40）。可通过 `PIXEL_BLUR_THRESHOLD` 调整。

### 画面完整度：边缘均匀带扫描

AI 重绘有时在极端比例（如 5:1 banner）的边缘留下纯色填充带。检测方法：

1. 取图片四边各 N 行/列（N = 图像对应维度 × `BorderScanRatio`，默认 0.08）。
2. 对扫描带内每行/列，检查颜色方差是否 < 颜色均匀阈值（默认 200，RGB sum of variance per channel）。
3. 若任意一边有 ≥ `BorderMaxRatio`（默认 15%）的宽度属于均匀带 → 判画面不完整。

这比 AI judge 的 `canvas_fill` 更精确：AI judge 给分时把均匀带面积比例混入主观评分，本方法直接像素计数，无误差。

## 集成位置

```
AI 重绘出图 → 收敛到目标尺寸
  ↓
[PixelChecker.Check(imgBytes)]   ← 新增，<10ms
  ├─ 模糊 or 画面不完整
  │     → 推 review_failed（reason="图像模糊"/"存在纯色留白带"）
  │     → hints 注入重生 prompt
  │     → 重生一次（同 taskID，attempt=1）
  │     → 重生产物跳过所有质检，直接持久化
  └─ 通过
        ↓
      [QualityChecker.Check(imgBytes)]  ← 现有 AI judge（语义层）
          ├─ 不及格 → 重生（同前逻辑）
          └─ 及格 → 持久化
```

`PixelChecker` 为 nil（未配置/禁用）时行为与现在完全一致，零破坏性。

## 配置

```
PIXEL_BLUR_THRESHOLD=80        # Laplacian 方差下限；0 = 禁用模糊检测
PIXEL_BORDER_MAX_RATIO=0.15    # 边缘均匀带最大允许宽度比例；0 = 禁用
```

两个参数均可独立置 0 禁用对应检测，`PixelChecker` 会在 `NewPixelChecker` 中判断是否需要运行。

## 性能

- 典型 1080p 图（~2M pixels）：灰度转换 + Laplacian 扫描 + 边缘扫描合计 <8ms（单线程，Go 原生）。
- 不引入任何 goroutine 或 I/O，纯 CPU 内存操作。
- 先于 AI judge（30s 超时）运行；若像素不及格，完全跳过 AI judge HTTP 调用，节省约 5–15s。

## 风险与缓解

| 风险 | 概率 | 缓解 |
|---|---|---|
| 阈值太严误杀正常图（清晰但风格化/暗调/水彩） | 低–中 | 默认阈值保守（80），可调；仅模糊到「方差<80」才拒绝，正常 AI 生图一般 ≥150 |
| 边缘扫描误报（渐变背景的边缘也可能均匀） | 低 | `BorderScanRatio` 扫描厚度保守（8%），颜色方差阈值宽松；且需 15% 宽度才触发 |
| WebP 格式解码需要额外包 | 已知 | 使用 `golang.org/x/image/webp`，已被多数 Go 项目引用 |
