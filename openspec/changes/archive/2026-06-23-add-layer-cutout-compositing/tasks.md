# Tasks

## 1. 后端：前景主体检测（vision）
- [x] 1.1 `SubjectDetector.DetectSubjects`：多主体 JSON（desc + 归一化 box），复用 Gemini/OpenAI 传输与 `extractJSON`
- [x] 1.2 prompt 只列前景主体（排除背景/氛围）、按重要性排序、上限 8、空列表降级
- [x] 1.3 `parseSubjects` 单测：含 fence/prose、空列表、丢空 desc、clamp box、无 JSON

## 2. 后端：抠图意图 `extract_layer`
- [x] 2.1 `EditExtractLayer` 意图 + 模板（仅抠主体、其余全透明、同尺寸、不补背景）
- [x] 2.2 色键（chroma-key）抠图：模型画纯绿 #00FF00、服务端键出真 alpha；优先 Gemini、未配置降级默认适配器（不拒绝）；`RetryAsset` 复用
- [x] 2.3 单测：prompt 内容（#00FF00）、注入剥离、`chromaKeyToAlpha`、端到端键透明

## 3. 后端：背景补全意图 `fill_background`
- [x] 3.1 `EditBackgroundFill` 意图 + 模板（移除主体、补全干净背景、同尺寸、不留洞/不发明）
- [x] 3.2 优先 Gemini、可降级默认适配器（不拒绝）；`run` 与 `RetryAsset` 路由
- [x] 3.3 单测：prompt 内容

## 4. 后端：图层精修编排端点 `layer-split`
- [x] 4.1 新 `internal/layering`：检测 → 并发 spawn(1×fill_background + N×extract_layer) → await 汇总有序图层
- [x] 4.2 背景失败回退源图为锁定底层（避免单次 provider 抽风沉没整次分层）；个别主体失败 best-effort 丢弃；至少 2 层才成功
- [x] 4.3 `POST /api/session/{id}/layer-split` 端点；`main.go` 用 Vision/Quality 凭证装配检测器
- [x] 4.4 单测：产出背景+主体层且 canvas=源图尺寸、无主体报错、背景失败回退源图、未配置报错

## 5. 后端：合成产物持久化端点
- [x] 5.1 新 `internal/composite`：收 PNG 字节 + 来源 id，落库为会话 `composite` 资产，复用无损优化（保 alpha）
- [x] 5.2 `POST /api/session/{id}/composite` 端点；跨会话隔离与非法输入报错
- [x] 5.3 单测：透明 PNG 往返保 alpha、跨会话隔离、非图片/空/缺 session 报错

## 6. 前端：固定画布精修流程
- [x] 6.1 入口绑定到具体图：资产卡右键「图层精修」+ 预览内「图层精修」按钮（`onLayerSplit` 透传 grid/timeline）
- [x] 6.2 画布尺寸锁定=源图尺寸；调用 `layer-split`、加载态、用返回图层填充
- [x] 6.3 背景层锁定（不可移动/缩放/移除/被压底）；主体层平移/等比缩放/层叠/移除
- [x] 6.4 `canvas.toBlob` 拍平 → `persistComposite` 落库 → 刷新工作区；分层失败明确提示
- [x] 6.5 UI 遵循品牌审美与平滑过渡（transition-all duration-200 ease-out）

## 7. 时间轴与类型
- [x] 7.1 `composite` 资产类型；时间轴归类【拼接】事件（Layers 图标）；AI 抠图/背景层沿用【生成/编辑】

## 8. 校验
- [x] 8.1 `go build ./...` + `go test ./...` 全绿；前端 `tsc -b` + `vite build` 通过
- [x] 8.2 `openspec validate add-layer-cutout-compositing --strict` 通过
