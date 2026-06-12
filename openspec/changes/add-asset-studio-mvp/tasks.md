# Tasks: Asset Studio MVP

> 实施顺序按「能力逐个完整实现」推进。密钥/端点统一走环境变量（代码留配置结构与占位），不硬编码真实密钥。

## 0. Foundation（地基）
- [ ] 0.1 初始化 `go.mod`（module `gameasset`，Go 1.23）与目录骨架
- [ ] 0.2 `internal/config`：集中配置（模型/端点/密钥读 env，平台尺寸读 JSON，存储目录/保留策略）+ 单测
- [ ] 0.3 `internal/store`：SQLite schema（sessions / assets / tasks，预留 preferences）+ 迁移 + 单测
- [ ] 0.4 `cmd/server`：HTTP server 入口 + `web/` 的 `embed` 挂载 + 健康检查
- [ ] 0.5 平台尺寸默认配置 `configs/platforms.json`（通用默认值，数据驱动）

## 1. session-management
- [ ] 1.1 `internal/session`：基于浏览器指纹生成匿名 session id + 内存活跃态 + DB 持久化
- [ ] 1.2 复用/重连逻辑（sessionStorage id 命中即恢复）
- [ ] 1.3 会话上下文状态查询（活跃任务数、最近意图）
- [ ] 1.4 会话级隔离（context/任务/产物按 session 分区）
- [ ] 1.5 HTTP handler：`POST /api/session`、`GET /api/session/{id}/context`
- [ ] 1.6 单测（创建/复用/隔离）

## 2. realtime-transport
- [ ] 2.1 `internal/transport`：WebSocket hub（对话消息、工具事件、胶囊选择回传）
- [ ] 2.2 SSE：按 task id 订阅的任务进度流（queued→running→progress→done/failed）+ no-buffer header
- [ ] 2.3 SSE 断线重连（Last-Event-ID 恢复最新状态）
- [ ] 2.4 事件类型定义（统一 envelope）+ 单测

## 3. conversation-orchestration
- [ ] 3.1 `internal/agent`：ChatModel 抽象接口（Anthropic 主 / DeepSeek 测试，env 切换）
- [ ] 3.2 Eino ReAct agent 接入 + 工具注册表（生图/裁剪/下载，预留生视频/爬取）
- [ ] 3.3 意图白名单分发：命中→调工具；未命中→礼貌拒绝 + 能力清单
- [ ] 3.4 context 滑动窗口（裁剪 + 摘要压缩；大块结果以引用 id 入 context）+ 单测
- [ ] 3.5 流式输出经 WS 增量推送
- [ ] 3.6 单测（意图分发、窗口压缩、引用 id）

## 4. image-generation
- [ ] 4.1 `internal/generation`：ImageProvider 接口 + 主/备 gpt-image-1 供应商（env 端点）
- [ ] 4.2 主失败切备 + 记录产物来源供应商 + 单测（mock provider）
- [ ] 4.3 换角色/背景/文案工具（结构化 slot 承接，服务端模板组装 prompt）
- [ ] 4.4 颜色适配：提取来源图主色板 → 注入约束 + 「避免突兀对比」固化模板 + 单测
- [ ] 4.5 参考图构图复用
- [ ] 4.6 已生成图二次调整 + prompt 注入防护（剥离控制类指令）+ 单测
- [ ] 4.7 异步任务化（SSE 进度）+ 产物落地本地文件 + DB 元数据

## 5. image-cropping
- [ ] 5.1 `internal/crop`：纯图像处理 resize/crop（保持主体可见，横竖适配）+ 单测
- [ ] 5.2 平台尺寸预设读取（数据驱动），返回分组尺寸清单给前端
- [ ] 5.3 裁剪工具（多尺寸批量产出，不调 AI）+ 产物回填

## 6. asset-workspace
- [ ] 6.1 工作区资产模型（上传/生成/裁剪条目，选择/点击/下一步）
- [ ] 6.2 并发任务占位 + 实时状态（排队/进行中/成功/失败）
- [ ] 6.3 单个失败项部分重试（不影响已成功）+ 单测

## 7. download-packaging
- [ ] 7.1 单图下载 handler
- [ ] 7.2 批量打包 zip handler（跳过无效项 + 通知被跳过条目）+ 单测

## 8. frontend-experience（embed 嵌入）
- [ ] 8.1 品牌化科技感首屏 + 核心能力入口
- [ ] 8.2 Agent 式对话区（工具调用卡片、状态、context 面板）
- [ ] 8.3 工作区预览（占位/骨架屏/状态/重试入口/点图二次调整）
- [ ] 8.4 尺寸胶囊按钮（数据驱动渲染）
- [ ] 8.5 下载/批量打包入口
- [ ] 8.6 toast 异常通知 + 偏好角落占位（空时隐藏）
- [ ] 8.7 响应式 + CSS 过渡 + sessionStorage 会话态

## 9. 收尾
- [ ] 9.1 `go vet` + `gofmt` + 全量 `go test ./...` 通过
- [ ] 9.2 `openspec validate add-asset-studio-mvp --strict` 通过
- [ ] 9.3 README/运行说明（env 变量清单、启动命令）
