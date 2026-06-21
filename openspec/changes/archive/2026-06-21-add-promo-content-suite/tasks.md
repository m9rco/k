# Tasks

> 依赖顺序：阶段 1（文案）→ 阶段 2（叠加，引用文案）→ 阶段 3（变体，独立可并行）→ 阶段 4（编排与前端贯通）。阶段 3 与阶段 1/2 无强依赖，可并行开发。
>
> 注：原规划含「投放前合规审查（compliance-review）」，经决策**剖离不实现**（内部工具，合规终审走人工/法务，不以软件硬做），相关 spec 已从本 change 移除。

## 1. 宣发文案生成（copywriting-generation）
- [x] 1.1 新增 `internal/copywriting/` 包：定义文案结构（标题/广告语/卖点/投放文案）与服务端固定 system prompt（防注入、不虚构）
- [x] 1.2 复用视觉/会话 LLM 链路，消费工作区素材 + 视觉分析报告作为输入
- [x] 1.3 实现平台与字数约束的裁剪/重写
- [x] 1.4 注册 `generate_copy` 工具，产物以结构化形式（可被 overlay_text 引用）回填
- [x] 1.5 表驱动单测：有/无分析报告、字数约束、防注入

## 2. 文字/LOGO 叠加（text-overlay）
- [x] 2.1 新增 `internal/textoverlay/` 包：服务端确定性字体渲染（内置 Go Bold 回退 + 运行时 CJK 主字体 + 逐字缺字检测）
- [x] 2.2 入参支持九宫格/归一化坐标、字号、颜色、描边、背景色块、安全区
- [~] 2.3 样式默认取合理值；支持 CTA/折扣角标/定档大字/文字图层（LOGO 图片图层后续；色板协调取默认色）
- [x] 2.4 注册 `overlay_text` 工具，按唯一 id 寻址源图，产物链接 parent 回填工作区
- [x] 2.5 单测：安全区裁切、缺字回退、坐标/锚点渲染、描边、空/盲文本拒绝

## 3. 批量变体（batch-variants）
- [x] 3.1 新增变体编排层：变体维度（构图/配色/文案/风格）+ 批次标识，复用 `internal/generation/` 生图管线（`internal/agent/variants_tool.go`，复用 `EditBackground` 保主体，零碰生图管线）
- [x] 3.2 数量约束 2~8，超量收敛并提示（默认 4；min/max/offsets 三层收敛，clamped 经 ack 文案提示）
- [x] 3.3 注册 `generate_variants` 工具，N 个独立异步任务，单变体失败不连坐（每个变体独立 `Generation.Start`，单个失败 continue+记 Failed，全失败才报错）
- [x] 3.4 批量占位事件 + 同批分组回填（复用既有 `announce.AnnounceTask`/`task_created` 逐任务占位 + 前端 `variants_group` 卡同批分组对比；未新增 transport 事件类型——既有逐任务占位已满足实时占位/逐个回填/失败隔离）
- [x] 3.5 单测：数量收敛、失败隔离、维度选择、去重、防注入、batch_id 稳定（`variants_tool_test.go`）；复用质量门控由 `EditBackground` 走与单图一致的管线天然保证

## 4. 编排与前端贯通
- [x] 4.1 `internal/agent/`：将三类意图纳入白名单与确定性预分类（关键词/句式提示），更新分层 system prompt（白名单+预分类+prompt 第15/16/17条）
- [x] 4.2 前端 `web/src/`：文案卡片（分组 + 可引用，copy-card.tsx）、批量变体分组网格（variants-group.tsx）；叠加产物走既有工作区回填
- [x] 4.3 更新分层 system prompt 与意图提示的单测/契约测试（intent_test.go + variants_tool_test.go + 白名单/AsyncTaskTools 契约测试）

## 5. 验证与归档准备
- [x] 5.1 `openspec validate add-promo-content-suite --strict` 通过
- [x] 5.2 全量 `go test ./...` 通过（18 包无 FAIL）；前端 `npm run build` 通过
- [x] 5.3 README 补充文字叠加字体（CJK）说明与 `OVERLAY_FONT` 变量；三个新意图的能力已入欢迎清单与 system prompt
