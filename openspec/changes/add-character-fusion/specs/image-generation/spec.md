## ADDED Requirements

### Requirement: 角色融合意图默认锁定 gpt-image-2
系统 SHALL 在 `change_character`（替换角色）与 `add_character`（新增角色/融合）两个意图的 AI 生图上，**请求级优先使用 `gpt-image-2`**（其在主体身份与构图保真上最强，最适合「把某角色融合进底图」）。该路由 SHALL 采用与 `adapt_platform` 一致的带兜底降级链：`gpt-image-2` → `gemini-3-pro-image` → 会话选型/服务默认；当某档凭据未配置（`ResolveImageModel` 返回不可用）时自动降到下一档，**绝不**注入一个无凭据的破损 override。

`change_background`、`change_text`、`generate_icon`、`text_to_image` 等其他意图的模型路由 **MUST NOT** 受此改动影响，仍使用会话选型/服务默认（`ImageOverride`）。主/备供应商失效切换、产物来源记录、颜色适配与参考图复用语义 MUST 保持不变。

#### Scenario: 融合默认走 gpt-image-2
- **WHEN** 用户发起「把图2角色融合到图1」（`add_character` 或 `change_character`，图1 为 `source_asset_id`，图2 为 `reference_asset_ids`）且 gpt-image-2 凭据已配置
- **THEN** 系统以 `gpt-image-2` 发起该次 AI 生图，无视会话当前的图像场景选型
- **AND** 产物记录其实际来源供应商

#### Scenario: gpt-image-2 未配置时降级
- **WHEN** 执行融合意图但 gpt-image-2 凭据未配置、gemini-3-pro-image 已配置
- **THEN** 系统降级使用 `gemini-3-pro-image` 发起生图
- **AND** 当两者均未配置时，降级到会话选型/服务默认（`ImageOverride`），不因缺密钥而失败

#### Scenario: 其他意图路由不受影响
- **WHEN** 用户发起 `change_background` 或 `change_text`
- **THEN** 系统仍按会话选型/服务默认选择生图模型，不强制 gpt-image-2

### Requirement: 角色融合以底图为真相源
系统 SHALL 在 `change_character`/`add_character` 的生图提示中显式声明融合真相源契约：**底图（`source_asset_id`，图1）是产物风格、宣发意图、文案/标题、构图与配色的唯一真相源**。生图 SHALL 完整保留底图的这些要素，**只**把参照图（`reference_asset_ids`，图2、图3…）中的**角色/主体**按底图风格**重绘式**融入（本地化到底图的光照、色温、笔触、比例），**MUST NOT** 把参照图的风格、文案、背景或配色带入并覆盖底图，**MUST NOT** 凭空生成参照图/底图之外的角色或主体。该提示由服务端固定模板组装，用户文本仅作为经注入防护的 slot 片段。

#### Scenario: 多图角色融入底图而不改写底图
- **WHEN** 用户要求「把图2、图3的角色融到图1里」（图1=`source_asset_id`，图2/图3=`reference_asset_ids`）
- **THEN** 产物保留图1的风格、文案/标题、构图与配色
- **AND** 图2、图3的角色被按图1的风格重绘式融入，身份特征忠于各自参照图
- **AND** 产物不引入图2/图3的风格、文案或背景，也不出现参照图/底图之外的多余角色
