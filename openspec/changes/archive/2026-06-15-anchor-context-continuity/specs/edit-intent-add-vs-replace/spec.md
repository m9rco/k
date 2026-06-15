# edit-intent-add-vs-replace Specification

## Purpose
区分「替换角色」与「增加角色」两种意图：用户说"增加一位男性角色在旁边"时，应在保留原有角色的基础上新增一个角色，而非把原角色替换掉。原系统仅有 `change_character`（替换语义），导致"增加"被错误执行为"替换"。

## ADDED Requirements

### Requirement: edit_image 支持 add_character 意图
`edit_image` 工具的 intent 枚举 SHALL 新增 `add_character`，语义为「在保留画面现有角色/主体的前提下新增一个角色」，与 `change_character`（替换主角色）区分。生图 prompt 模板对 `add_character` SHALL 明确指示模型新增角色、保留并不得移除/替换已有角色与场景构图；对 `change_character` 仍为替换语义。两者都 SHALL 在描述为空时报错。

#### Scenario: 增加角色不替换原角色
- **WHEN** 模型以 `intent=add_character, character_desc="废土风格男性"` 调用 edit_image
- **THEN** 生成 prompt 含"Add a new character"且明确"do NOT replace"现有主体
- **AND** 不含"Replace the main character"指令

#### Scenario: 替换角色仍为替换语义
- **WHEN** 模型以 `intent=change_character` 调用 edit_image
- **THEN** 生成 prompt 含"Replace the main character"

#### Scenario: 缺少描述报错
- **WHEN** 以 `intent=add_character` 调用但 `character_desc` 为空
- **THEN** 工具返回明确错误，不发起生成任务

### Requirement: 意图路由识别"增加角色"
系统的确定性预分类与会话 system prompt SHALL 能识别"增加/添加/多加一个角色/在旁边加一个人"等表达并引导模型选择 `add_character` 而非 `change_character`；能力清单 SHALL 列出"增加角色"作为一项独立能力描述。

#### Scenario: 预分类命中增加角色
- **WHEN** 用户文本含"增加一位角色 / 加个人物 / 旁边再加一个"
- **THEN** 确定性预分类产出"增加角色"标签，建议工具 edit_image
- **AND** 不与"换角色"（替换）标签混淆

#### Scenario: 能力清单包含增加角色
- **WHEN** 系统构建 system prompt 的能力白名单
- **THEN** 清单含"增加角色：在保留原有角色的基础上往画面里新增一个角色（不替换原角色）"
