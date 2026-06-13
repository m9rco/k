# material-crawling Specification

## Purpose
TBD - created by archiving change expand-studio-capabilities. Update Purpose after archive.
## Requirements
### Requirement: 按游戏名爬取素材
系统 SHALL 支持用户在会话中按**游戏名**发起素材爬取：从可配置的素材源搜索该游戏的图片素材并抓取预览，作为资产回填工作区。爬取 SHALL 仅做信息获取（图片预览），并标注来源；SHALL NOT 用于商用再分发。该能力以异步任务执行，进度经实时通道反馈。

#### Scenario: 按游戏名发起爬取
- **WHEN** 用户在会话中请求"爬取《某游戏》的宣传素材"
- **THEN** 系统按游戏名搜索素材源并抓取若干图片预览
- **AND** 抓取到的图片作为资产回填工作区，标注来源
- **AND** 抓取过程以任务进度形式反馈

#### Scenario: 无结果或部分失败
- **WHEN** 搜索无匹配结果，或部分图片抓取失败
- **THEN** 系统明确反馈无结果或哪些条目被跳过
- **AND** 不静默成功，不因单条失败中断整个爬取

#### Scenario: 来源不可用时降级
- **WHEN** 素材源未配置或不可访问
- **THEN** 系统礼貌告知该能力暂不可用，而非崩溃或返回空白成功

### Requirement: 爬取意图纳入白名单
系统 SHALL 将「物料爬取」从预留意图激活为可执行意图，纳入 Agent 的工具白名单，使会话可分发到爬取工具。

#### Scenario: 命中爬取意图
- **WHEN** 用户请求爬取某游戏素材
- **THEN** Agent 识别为爬取意图并分发到爬取工具
- **AND** 工具调用过程以事件形式可见于前端

