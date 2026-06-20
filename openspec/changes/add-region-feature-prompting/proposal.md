# Change: 选区驱动的精确特征描述与改图提示优化

## Why
当前二次调整只有一个自由文本框（lightbox.tsx），用户改某个局部时，prompt 里的"特征定义"完全靠人脑描述——"把左边那个角色的盔甲改成红色"既费力又不精确，模型常因主体/位置指代模糊而改错对象或发散重绘。需要让用户用鼠标在预览图上**智能选中一个物体/图层**，由系统对该选区自动产出**精确、结构化的特征描述**，并据此优化注入生图的改图提示，把"特征精确定义"这件事从人脑转移到工具。

研究结论已定调架构（避免走 gpt-image mask 的弯路）：

- **gpt-image mask 不可靠**：OpenAI 官方与社区一致确认 `images/edits` 的 mask 仅作引导、不保证局部不重绘，要真正局部还得服务端把产物在框外合成回原图（[OpenAI 社区](https://community.openai.com/t/mask-is-completely-ignored-in-the-image-edit-api/1350867)）。本项目当前 `http_provider.go` 也未传 mask。故**不以 mask 为核心**，而是把选区转成**语义精确描述**，沿用既有图生图管线。
- **浏览器端智能选区可行**：SlimSAM / MobileSAM（Transformers.js）或 SAM2 ONNX 可在浏览器内点击即吸附整个物体轮廓，encoder 跑一次、decoder 每次点击轻量重算（[Transformers.js SAM](https://huggingface.co/vietanhdev/segment-anything-onnx-models)、[SAM2 in browser](https://medium.com/@geronimo7/in-browser-image-segmentation-with-segment-anything-model-2-c72680170d92)）。
- **精确描述能力已就位**：`gemini-2.5-flash-all` 视觉分析器（`internal/vision`）已支持 inline base64、无需 COS，正好用来对选区裁片产出特征报告。

## What Changes
- **前端：预览 modal 内的智能选区**。Lightbox 增加"圈定图层"模式：浏览器端加载 SAM（SlimSAM/MobileSAM via Transformers.js，权重同源 embed），用户点击图中某物体即吸附其轮廓为选区；提供手动矩形框选作为零模型依赖的降级路径与模型加载期兜底。选区产出归一化 bbox + 可选 mask 轮廓。
- **后端：选区特征描述接口**。新增 `POST /api/session/{id}/assets/{assetId}/describe-region`，入参为归一化 bbox（及可选 mask）。服务端按 bbox 裁出选区像素，调既有 `vision.Analyzer`（gemini inline）以**固定的"局部特征精确描述"指令**产出结构化特征（主体类别、外观/材质/颜色、文字、相对位置、必须保留项），全部服务端模板、零用户自由文本注入。
- **前端：用描述优化改图提示**。把返回的结构化特征回填进 lightbox 的改图输入框（用户可编辑），并以"在【该选区主体：<特征>】上 <用户修改>，其余画面保持不变"的句式组装，再走既有 `edit_image` 二次调整链路。
- **后端：edit_image 接受可选 region 语境**。`edit_image` 工具/生图模板新增可选 `region_desc` slot（sanitized），在 `[MODIFY]` 段以"仅针对该选区主体、其余区域保持不变"约束改图，复用既有四段式骨架与注入防护；**不**改 provider mask 形态。

## Impact
- Affected specs: `marketing-analysis`（新增局部特征描述能力）、`frontend-experience`（新增选区交互）、`image-generation`（edit_image 新增 region_desc slot）
- Affected code:
  - 前端：`web/src/components/workspace/lightbox.tsx`（选区 UI + 描述回填）、新增选区/SAM 组件与 hook、`web/package.json`（`@huggingface/transformers`）、SAM 权重同源 embed（`web/static`）
  - 后端：`internal/vision`（新增局部描述指令/方法）、新增 `describe-region` handler（挂在 workspace 路由）、`internal/crop` 复用其裁剪以切选区、`internal/generation/prompt.go` + `internal/agent/tools.go`（`region_desc` slot）
- 非目标：不引入服务端 SAM、不改 gpt-image mask 调用形态、不做像素级 inpaint 合成（后续可作独立增强 change）
