# Change Proposal: pixel-quality-prefilter

## 摘要

在现有 AI judge（`doubao-seed-1-6-vision-250815`）**之前**，插入一道纯 Go 像素级质量预过滤器（`PixelChecker`），专门识别 AI judge 容易漏判的**客观像素缺陷**：

1. **模糊检测**（Laplacian 方差法）：图像清晰度低于阈值时直接判不及格，无需调用 AI judge。
2. **画面完整度检测**（边缘均匀带扫描）：检测图像四边是否存在大面积纯色/透明留白条带（AI judge 已有 `canvas_fill` 维度但常被评分"平均"拉高而漏判）。

预过滤器：
- **纯 Go 标准库**（`image`/`image/png`/`image/jpeg`），零外部依赖，无 CGo。
- 在主机内存中运行，典型执行时间 <10ms，远小于 AI judge 的 30s 超时。
- 不及格时携带具体原因与改进 hints，直接复用现有重生流程，**跳过 AI judge 调用**（节省配额）。
- 及格时透明放行，AI judge 照常运行（语义层检查补充）。

## Motivation

### AI judge 的固有局限

视觉 LLM 是**语义判官**，并非像素分析器：
- 模糊图片只要主体内容"对"，评分就容易在 `overall_quality` 维度给出 70–80 分而通过门控（感知质量 vs. 清晰度是两回事）。
- `canvas_fill` 维度分数容易被其他三个维度的高分"平均"稀释，实际存在 30% 纯色边框的图可能总分仍 ≥75。

### 算法检测的优势

| 方法 | 检测对象 | 准确率 | 速度 | 依赖 |
|---|---|---|---|---|
| Laplacian 方差 | 模糊 | 高（无假阴性，只有阈值调整问题） | <5ms | 零 |
| 边缘均匀带扫描 | 纯色留白条带 | 高（像素级，不依赖模型理解力） | <5ms | 零 |
| AI judge（现有） | 语义合规、主体一致性、人物卖相 | 高（语义层） | ~5–30s | API 配额 |

两者互补，而非替代：算法层滤掉客观缺陷，AI judge 专注语义层评估。

### 开源 IQA 调研结论

| 工具 | 适用场景 | Go 集成 |
|---|---|---|
| [IQA-PyTorch (pyiqa)](https://git.durrantlab.pitt.edu/chaofengc/IQA-PyTorch) | 全面 NR-IQA，含 BRISQUE/NIQE/MUSIQ/CLIPIQA 等 30+ 指标 | 需 Python sidecar |
| [BRISQUE (OpenCV)](https://github.com/krshrimali/No-Reference-Image-Quality-Assessment-using-BRISQUE-Model) | 通用无参考质量评分 | 需 CGo/gocv |
| [go-blurry](https://github.com/indyka/go-blurry) | 模糊检测，基于 gocv | 需 CGo |
| **Laplacian 方差（本提案）** | 模糊检测，纯算法 | **纯 Go 标准库** |

结论：对本项目（单二进制、embed 前端、无 CGo）最合适的方案是**自实现纯 Go Laplacian 方差**和**边缘均匀带扫描**，效果针对性强、不引入任何外部依赖。BRISQUE/NIQE 等通用 NR-IQA 指标针对自然图像标定，对 AI 生成的宣发素材（风格化、过饱和）可能有较多误判，且都需要 CGo 或 Python 进程，不适合本架构。

## Scope

### 本 change 范围

- `internal/vision/pixel.go`：`PixelChecker`（Laplacian 方差 + 边缘均匀带扫描），纯 Go。
- `service.go`：在 `QualityChecker.Check()` 前插入 `PixelChecker.Check()`；像素不及格 → 直接推 `review_failed` + 重生，**跳过** AI judge。
- `internal/config/config.go`：新增 `PIXEL_BLUR_THRESHOLD`（默认 80）与 `PIXEL_BORDER_MAX_RATIO`（默认 0.15）两个可调参数。
- 单测：模糊图/清晰图/有留白图/正常图的检测准确性。

### 不在本 change 范围内

- BRISQUE/NIQE 等需 CGo 或外部进程的指标。
- 颜色过饱和/偏色检测（后续可叠加）。
- 像素检测用于非适配生图（文生图首次生成）。
- 前端新增「像素质检不及格」的专属显示（复用现有 `review_failed` UI）。

## Open Questions

无待解问题，算法选型与集成点已明确。
