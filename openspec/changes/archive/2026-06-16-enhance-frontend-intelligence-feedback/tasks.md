# Tasks: 前端智能感与反馈优化

## 1. 分析报告完成自动折叠
- [x] 1.1 `controller.ts` `onAnalysisDelta`：`done=true` 时 `collapsed: true`（当前为 `false`）
- [x] 1.2 单测：`analysis_done` 事件后 item.collapsed === true

## 2. 关键交互 loading 补全
- [x] 2.1 Composer 上传按钮：`uploadFiles` 执行期间按钮 disabled + `<Loader2 animate-spin>`
- [x] 2.2 SizePicker 确认按钮：`running` 为 true 时内联 spinner（已有 disabled 逻辑，补图标）
- [x] 2.3 AssetCard 重试菜单项：点击后短暂 disabled 防连点（异步调用期间）

## 3. 参考图来源标识
- [x] 3.1 后端 `AssetView` 新增 `referenceIds []string`（从 `gen_origin` 读取 `reference_asset_ids`）
- [x] 3.2 前端 `Asset` 类型新增 `referenceIds?: string[]`
- [x] 3.3 `AssetCard`：`referenceIds.length > 1` 时渲染「参考 N 张」徽章（左下角，同尺寸标注位置）
- [x] 3.4 `AssetCard` hover 时展示 ≤4 张参考图缩略图行（`<img>` 宽 20px，头像叠叠乐样式）
- [x] 3.5 单测：`gen_origin` 有 `reference_asset_ids` 时 `AssetView.referenceIds` 填充正确

## 4. AI 修图 chat 同步（重试）
- [x] 4.1 `retryAsset` 触发时在 chat 插入 `kind:"tool"` 卡片（`edit_image` / `retry` / `running`）
- [x] 4.2 任务 done → chat 卡片状态更新为 `done`；failed → `failed` + error
- [x] 4.3 tool-meta：`edit_image:retry` 映射为「重试生成」+ `RotateCcw` 图标
- [x] 4.4 单测：retryAsset 后 chat 含 running tool 卡；task_done 后卡片变 done

## 5. 验证
- [x] 5.1 `go build ./...` 与 `go test ./...` 全绿
- [x] 5.2 `npm run build` 通过
- [x] 5.3 `openspec validate enhance-frontend-intelligence-feedback --strict` 通过
