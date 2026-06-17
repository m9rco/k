# Project Context

## Purpose
游戏宣发素材生成系统（Game Asset Studio）。面向游戏发行/买量团队，通过一个对话式 Agent，根据用户意图完成 AI 生图、按平台裁剪尺寸、生视频（动效），以及游戏物料爬取。目标是把"换角色 / 换背景 / 换文案 / 切尺寸 / 加动效"这类高频宣发素材操作，收敛到一个对话窗口里完成。

## Tech Stack
- 后端: Go (Golang)，单二进制，前端资源用 `embed` 嵌入
- Agent 编排: CloudWeGo Eino（ReAct agent、工具调用、流式、interrupt/resume checkpoint）
- 持久化: SQLite（长期）+ 进程内内存（短期/会话）
- 前端: 原生/轻量栈，强调科技感、响应式、骨架屏、CSS 过渡、sessionStorage
- 实时: WebSocket（对话与工具事件）+ SSE（长任务生图/生视频进度）
- 模型（服务端硬编码，用户不可选）:
  - 会话理解: `claude-sonnet-4-6`（主） / DeepSeek chat（测试）
  - 生图: `gpt-image-2` ×2 供应商（主 + 备）
  - 视觉宣发分析: `gemini-2.5-flash-all`（Gemini 原生 inline，无需 COS 上传）
  - 适配质量门控: `doubao-seed-1-6-vision-250815`（Volcengine ARK，OpenAI 兼容 + data URI inline）
  - 生视频: 待定（后续 change 引入）

## Project Conventions

### Code Style
- Go 标准 `gofmt`，包名小写短名，导出符号写清楚 doc comment
- 错误显式处理并 wrap context（`fmt.Errorf("...: %w", err)`）
- 模型/供应商配置集中在一处常量/配置文件，硬编码（本工具仅小团队内部使用，不做安全加固）

### Architecture Patterns
- Agent 为核心：意图识别 → 工具分发（生图/裁剪/生视频/爬取）→ 结果回填工作区
- 工具（tool）边界清晰，单一职责，可独立测试
- 长任务异步化，前端用占位 + 状态 + 部分重试
- context 滑动窗口：裁剪 + 压缩，防止模型胡言乱语

### Testing Strategy
- BDD 风格：每个能力以 spec 的 Scenario 为验收基准
- Go 用表驱动测试；工具层、意图识别、context 窗口管理需有单测
- 长任务/实时层做契约测试

### Git Workflow
- 主分支 `main`；feature 分支提 PR
- 提交信息简洁，关联 change-id

## Domain Context
- 宣发素材有强烈的"平台尺寸"约束（横版/竖版、各广告位规格）
- 换素材时**颜色适配**是硬指标：替换角色/背景/文案后整体不能突兀
- 物料爬取仅做信息获取（图片预览），非商用再分发

## Important Constraints
- 仅供小团队内部使用，密钥硬编码，不做鉴权/安全加固
- 用户无需注册，进入即根据浏览器信息生成 session
- 仅执行预设几类意图，其余礼貌拒绝
- prompt 注入防护：用户可点图二次调整，需对注入到生图 prompt 的内容做约束

## External Dependencies
- Anthropic API（claude-sonnet-4-6）
- DeepSeek API（OpenAI 兼容，测试用）
- OpenAI 兼容生图 API（gpt-image-1，两个供应商端点）
- 生视频 API（待定）
- 爬取目标平台（待定，后续 change）
