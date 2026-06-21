## ADDED Requirements
### Requirement: edit_image 选区限定改图（region_desc）
`edit_image` 工具 SHALL 新增**可选** `region_desc` 参数，承接由选区特征描述产出的局部主体特征（经服务端 sanitize 的 slot 文本）。当 `region_desc` 存在时，服务端生图模板 SHALL 在既有四段式骨架的 `[MODIFY]` 段以固定句式表达「仅针对该选区主体（`region_desc`）执行本次修改，其余区域、构图与其他主体保持不变」，并在 `[PRESERVE]`/`[AVOID]` 固定文案中补充「不改动选区外像素与其他主体」。该能力 SHALL **纯提示层**实现——MUST NOT 改变图生图 provider 的请求形态（不引入 mask/edits 的 mask 参数），并对所有图生图供应商（OpenAI 兼容与 Gemini 形态）一致表达。`region_desc` 为空或缺省时，行为 SHALL 与现状完全一致。

#### Scenario: 带 region_desc 的选区限定改图
- **WHEN** `edit_image` 以非空 `region_desc` 调用
- **THEN** 生成提示在 `[MODIFY]` 段含「仅针对该选区主体（<region_desc>）执行修改、其余保持不变」句式
- **AND** `[PRESERVE]`/`[AVOID]` 含「不改动选区外像素与其他主体」约束

#### Scenario: 不改变 provider 请求形态
- **WHEN** 带 `region_desc` 的请求经任一图生图适配器组装
- **THEN** provider 请求不新增 mask 参数，仅提示文本体现选区限定
- **AND** 主备失效切换、产物来源记录语义不变

#### Scenario: region_desc 缺省回退现状
- **WHEN** `edit_image` 未提供 `region_desc`
- **THEN** 生成提示与未引入本能力前完全一致

#### Scenario: region_desc 注入防护
- **WHEN** `region_desc` 含试图改写系统指令的内容
- **THEN** 系统通过结构化 slot 承接并由服务端模板组装，剥离/忽略控制类指令
- **AND** 该文本不被直接拼接为可改写系统行为的提示
