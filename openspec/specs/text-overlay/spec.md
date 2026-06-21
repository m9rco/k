# text-overlay Specification

## Purpose
TBD - created by archiving change add-promo-content-suite. Update Purpose after archive.
## Requirements
### Requirement: 确定性文字/LOGO 叠加工具
系统 SHALL 新增 `overlay_text` 工具，对工作区某张图（按唯一 id 寻址）做**确定性**的文字与 LOGO 叠加，由服务端字体渲染完成，**MUST NOT** 经生图模型「画字」。叠加要素 SHALL 至少支持：CTA 按钮文字、促销/折扣角标、定档大字、品牌 LOGO 图层。工具产出 SHALL 是在源图基础上合成的新资产，并作为新条目回填工作区、链接 parent。

#### Scenario: 叠加 CTA 到指定图
- **WHEN** 用户请求「给图3 右下角加一个『立即预约』按钮」
- **THEN** 系统调用 `overlay_text`，以确定性字体渲染在图3 右下角合成「立即预约」CTA
- **AND** 产物为新资产、链接到图3 作为 parent，文字清晰无糊、无错字

#### Scenario: 不经生图模型
- **WHEN** 执行任意 `overlay_text` 叠加
- **THEN** 文字与 LOGO 由服务端确定性渲染合成
- **AND** 不调用生图模型，产物文字与入参完全一致

### Requirement: 叠加位置、样式与安全区可控
`overlay_text` 入参 SHALL 支持位置（九宫格锚点或归一化坐标）、字号、颜色、描边/阴影、对齐方式与可选背景色块；当目标用于某平台尺寸时 SHALL 遵守该尺寸的安全区约束，避免文字被裁切或压标题栏。未指定的样式参数 SHALL 取与素材色板协调的合理默认（不澄清）。

#### Scenario: 安全区内渲染
- **WHEN** 叠加目标图带有安全区约束
- **THEN** 文字与 LOGO SHALL 渲染在安全区内
- **AND** 不溢出到会被平台裁切的边缘区域

#### Scenario: 样式默认取色板协调值
- **WHEN** 用户只说「加个折扣角标 8 折」而未指定颜色
- **THEN** 角标配色取与素材主色板协调的默认值
- **AND** 不出现与画面突兀的高饱和撞色

### Requirement: 叠加文本防注入与字体可用性
叠加文本 SHALL 作为纯渲染内容处理，MUST NOT 被解释为指令；当文本含表情/生僻字而所选字体缺字形时，系统 SHALL 回退到具备覆盖的内置字体或给出明确不可渲染信号，而非渲染出豆腐块（缺字方块）后静默交付。

#### Scenario: 缺字形回退
- **WHEN** 叠加文本含当前字体不支持的字符
- **THEN** 系统回退到覆盖该字符的内置字体或返回明确告警
- **AND** 产物不出现缺字方块（tofu）

