## 1. 模型目录与可用性(服务端)
- [x] 1.1 `internal/config/catalog.go` 定义模型目录:每项 {ID, DisplayName, Scene, Vendor, IconKey, Provider},覆盖四场景 11+ 模型
- [x] 1.2 `AvailableModels`/`AvailableModelsByScene`:按各场景凭证(api_key 非空)过滤可用集合
- [x] 1.3 单测:可用性过滤、IsModelAvailable、ResolveChatModel/ResolveImageModel(catalog_test.go)

## 2. 会话级选择存储
- [x] 2.1 `store` 增 `SetPreference`/`GetPreferences`(复用 preferences 表,id=session:key 做 upsert);`internal/usermodel` 封装 key=model.<scene> 的读写
- [x] 2.2 写入校验 modelID 属于可用集合,否则拒绝(usermodel.Set)
- [x] 2.3 单测:往返、拒绝不可用、隔离、跨 manager 持久化(store_test 复用 + usermodel_test)

## 3. 模型目录与切换 API
- [x] 3.1 `GET /api/session/{id}/models`:返回按场景分组目录 + 当前会话选择(orch.AvailableModels)
- [x] 3.2 `POST /api/session/{id}/models`:设置覆盖并持久化;chat 场景触发自我介绍(orch.SwitchModel)
- [x] 3.3 路由注册(main.go inline,与既有 window/optimize 端点一致);拒绝不可用模型返回 400

## 4. 会话级模型解析(后端核心)
- [x] 4.1 `Orchestrator` 每轮 `models.ChatModel(sessionID)` 解析,有覆盖则 `newChatModel`,无则默认
- [x] 4.2 生图/文生图:`GenerateParams.ProviderOverride`(零值回退默认);工具从会话覆盖注入
- [x] 4.3 视频:`video.Params.ProviderOverride`,run 时按 task 固化 provider
- [x] 4.4 测试:agent/config/usermodel 全绿;构造隔离天然实现进行中轮用旧实例

## 5. 切换后自我介绍(仅 chat)
- [x] 5.1 chat 切换成功后 `selfIntroduce` 用新模型流式生成自我介绍,经 hub 走 turn_start/增量/turn_end
- [x] 5.2 复用 sessionTurnLock 串行;注册 cancel 可被新输入打断
- [x] 5.3 切非 chat 场景不触发(SwitchModel 内分支)

## 6. 前端入口与弹窗
- [x] 6.1 `components/vendor-icons.tsx`:品牌 SVG/monogram，IconKey→图标映射,未知回退中性点
- [x] 6.2 配置 icon 入口(ContextBar 顶栏「模型」按钮,SlidersHorizontal)
- [x] 6.3 `components/chat/model-picker.tsx`:shadcn Dialog,四场景分区,模型卡片(品牌图标+名称+选中态),遵循设计令牌
- [x] 6.4 store/api:getModels/switchModel + controller loadModels/switchModel,乐观更新;chat 切换后自我介绍经现有 message 通道呈现
- [x] 6.5 可用性为空场景显示「该场景暂无已配置的模型」

## 7. 验证
- [x] 7.1 `go test ./...` 与 `go vet ./...` 全绿
- [x] 7.2 前端 `tsc -b` 无错 + `vite build` 通过(产物 embed)
- [x] 7.3 `openspec validate add-per-user-model-switching --strict` 通过
- [ ] 7.4 手动走查 A/B 两会话互不影响、刷新保留、进行中不受影响、chat 自我介绍 —— 留待可访问运行环境(需 yunwu 凭证联调)

## 说明
- API 路由沿用 main.go inline handler 模式(与既有 window/context/optimize 端点一致),未新建独立 handler 包;故无独立 handler 单测,解析逻辑由 config/usermodel 单测覆盖。
- 7.4 手动走查需真实运行环境与凭证,留待部署后验证。
