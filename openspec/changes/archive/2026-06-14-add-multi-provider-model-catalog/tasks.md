## 1. 逻辑推理(会话理解)新模型
- [x] 1.1 ⚠️ 待真实端点核实:doubao reasoning 字段名(已做多 key 兼容 `reasoning_content`/`reasoning`);gpt-5.4 走标准 chat/completions
- [x] 1.2 `chatmodel.go` + `stream.go` 兼容 doubao reasoning 字段别名(缺失则无思考、不报错)
- [ ] 1.3 ⚠️ 真实端点联调:claude×2(anthropic)、gpt-5.4/doubao(openai)各跑通一轮工具调用 + 流式(需 yunwu 凭证,留待部署环境)
- [x] 1.4 `.env.example` 增列四个逻辑推理模型的 provider/model 样例

## 2. 生图适配器工厂 + Gemini
- [x] 2.1 ⚠️ 文档不可联网核实:Gemini 形态按原生 generateContent 实现;若 yunwu 暴露为 OpenAI 兼容,设 `IMAGE_*_PROVIDER=openai` 即零代码切回
- [x] 2.2 `generation.NewProvider(cfg)` 工厂按 `cfg.Provider` 选型,default 回退 `HTTPProvider`(零回归)
- [x] 2.3 实现 `GeminiProvider`(generateContent + inline_data base64,多参考图多 parts)
- [x] 2.4 httptest 表驱动单测:成功/错误/空数据;工厂选型;主备不同供应商失效切换(复用既有 Failover 测试)
- [x] 2.5 `main.go` 用 `NewProvider` 装配生图主/备

## 3. 生视频 Veo 适配器
- [x] 3.1 ⚠️ 文档不可联网核实:Veo submit/poll 路径与字段按通用异步 task 形态实现,字段解析做多形态容错;待真实端点校正
- [x] 3.2 `video.NewProvider(cfg)` 工厂,default 回退 happyhorse
- [x] 3.3 实现 `veoProvider`(submit→poll→fetch),复用 Service 的 COS 源图发布 + 异步进度管线
- [x] 3.4 httptest 单测:全链路 + submit 失败 + 未配置降级 + 工厂选型
- [x] 3.5 `main.go` 经 `VIDEO_PROVIDER=veo` 装配

## 4. 文生图(text-to-image)新能力
- [x] 4.1 ⚠️ 文档不可联网核实:wan/qwen 按 DashScope image-synthesis 异步形态实现;待真实端点校正
- [x] 4.2 `DashScopeProvider`(submit→poll→fetch)实现 generation.Provider,经工厂 `dashscope` 选型
- [x] 4.3 新增 agent 工具 `generate_image_from_text`(无源图,新 EditTextToImage prompt 模板 + 注入防护)并纳入白名单(仅配置时暴露)
- [x] 4.4 复用异步任务/进度/回填管线(独立 generation.Service 实例,SetTextToImage 注入);系统提示新增文生图分流规则
- [x] 4.5 httptest 适配器单测 + prompt 模板单测 + 工厂选型
- [x] 4.6 `.env.example` 增列 wan/qwen provider/model 样例
- [ ] 4.7 前端工作区「文生图」显式入口(对话入口已可用;UI 显式入口留待前端单独跟进)

## 5. 配置与集成验证
- [x] 5.1 config 选型单测:TextToImage 默认 dashscope + 继承公共 key;各 provider 工厂选型单测
- [x] 5.2 `.env.example` 汇总:11 个新模型 + 各能力 provider 取值说明,旧默认保持兼容
- [x] 5.3 `go test ./...` 与 `go vet ./...` 全绿
- [x] 5.4 `openspec validate add-multi-provider-model-catalog --strict` 通过
- [ ] 5.5 README 模型矩阵(可选,留待文档跟进)

## 说明:待真实端点核实项
yunwu 文档站为 SPA 且当前网络受限,无法联网抓取字节级 schema。标 ⚠️ 的项已按各厂商**公开 API 形态**实现,并在代码内以注释明确标注;请求/响应解析均做了多形态容错。部署到可访问 yunwu 的环境后,按 1.3 联调逐个核实即可,无需改动工厂/接口架构。
