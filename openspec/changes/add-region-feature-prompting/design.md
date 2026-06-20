## Context
预览 modal 的二次调整目前是单一自由文本框，局部改图的"特征定义"全靠人工描述，指代模糊导致改错对象或整图发散。目标是用鼠标在图上智能选中物体，由系统产出精确特征描述并优化改图提示。关键约束来自既有现实：gpt-image 的 mask 仅作引导、不保证局部不重绘，且本项目 provider 当前不传 mask；视觉分析器（gemini-2.5-flash-all, inline base64）与图生图四段式提示骨架均已就位。

## Goals / Non-Goals
- Goals:
  - 浏览器内点击即吸附整个物体轮廓（智能选区），矩形框选作降级/兜底。
  - 对选区裁片产出**服务端固定指令**驱动的结构化特征描述，零用户文本注入。
  - 用该描述优化 `edit_image` 二次调整提示，约束"仅改选区主体、其余不变"。
- Non-Goals:
  - 不引入服务端 SAM（推理放浏览器端）。
  - 不改 provider 的 mask/edits 调用形态，不做像素级 inpaint 合成（后续独立 change）。
  - 不保证供应商真正像素级局部（语义约束 + 既有保真即为本期上限）。

## Decisions

### D1：核心动作 = 选区→精确语义描述→优化 prompt（非 mask inpaint）
- 选区只用于"裁出像素喂给视觉模型"与"在 prompt 里指明改谁"，**不**作为 provider mask。
- 理由：mask 在 gpt-image 上不可靠；语义路径复用既有图生图 + 注入防护管线，落地最稳、改动最小。
- Alternatives：(a) mask inpaint —— 受 provider 限制且需服务端框外合成，工程重、效果不确定，列为后续增强；(b) 纯人工描述 —— 即现状，不解决精确性。

### D2：智能选区用浏览器端 SAM（SlimSAM/MobileSAM via Transformers.js），矩形框选兜底
- encoder 在图片载入时跑一次缓存 embedding，每次点击只跑轻量 decoder，交互即时。
- 权重**同源 embed**进 `web/static`（项目内网约束：不可走 CDN/unpkg，与 ffmpeg core 同源策略一致——见 MEMORY）。
- 模型未加载/失败时自动降级为手动矩形框选，功能不阻塞。
- Alternatives：SAM2 ONNX + onnxruntime-web（质量更高但权重更大、集成更重）—— 本期取 SlimSAM/MobileSAM 的体积/集成平衡，SAM2 留作后续可替换实现。

### D3：选区裁剪复用 internal/crop，描述复用 internal/vision（gemini inline）
- 服务端按归一化 bbox 从原始资产裁出选区像素（crop 已有能力），inline 传给 `vision.Analyzer`。
- 新增**局部特征描述指令**（固定文案）：要求只描述选区内确有的要素（主体类别 / 外观材质颜色 / 可见文字 / 在整图中的相对位置 / 必须保留项），不虚构、不描述框外。
- 复用 `NeedsPublicURL()=false` 的 inline 路径，COS 未配置也能用。

### D4：edit_image 新增可选 region_desc slot，纳入四段式骨架
- `region_desc` 经 sanitize 进入 `[MODIFY]`，模板固定句式："仅针对该选区主体（<region_desc>）执行 <用户修改>，其余区域、构图、其他主体保持不变。"
- `[PRESERVE]`/`[AVOID]` 固定文案补一条"不改动选区外像素与其他主体"。
- 不新增 provider 参数，纯提示层；对所有图生图供应商一致表达。

## Risks / Trade-offs
- **SAM 权重体积撑大前端包** → 用 Slim/Mobile 量化权重、同源 embed、按需懒加载（仅进入选区模式才下载/初始化）。
- **浏览器算力差导致 encoder 慢** → 首帧后缓存 embedding；WASM 后端保底，WebGPU 可用则提速；加载期用矩形框选不阻塞。
- **语义约束仍可能整图轻微变化** → 明确为已知上限；像素级框外合成列为后续 change，不在本期承诺。
- **选区描述被视觉模型虚构** → 指令强约束"只描述框内确有要素、不虚构"，与既有分析指令同源策略。

## Migration Plan
- 纯增量：新增接口与可选 slot，旧的自由文本二次调整完全保留。SAM 不可用时自动退化为矩形框选 + 描述，再退化为纯文本（现状）。无数据迁移。

## Open Questions
- SlimSAM vs MobileSAM 的最终选型与量化精度（按实测包体/质量定，实现期决定，不影响 spec）。
- 选区 mask 轮廓是否随描述一并回传前端做高亮可视化（本期可只用 bbox，mask 高亮列为可选增强）。
