# Tasks: Expand Platform Catalog

## 1. 数据模型与目录数据
- [x] 1.1 在 `internal/config` 扩展类型：`Size` 增 `ID/Format/MaxKB/Note/Producible`；新增 `AssetType{Type,Name,Sizes}` 与 `Channel{ID,Name,Group,AssetTypes}`
- [x] 1.2 新增 `configs/channels.json`，按 `docs/gen_size.md` 全量录入所有**图片类**规格（截图/ICON/视频封面/推广图/资源位/H5），视频本体与外链标 `producible:false`
- [x] 1.3 `config.Load` 支持读取三层 `channels.json`；保留对旧 `platforms.json` 两层结构的兼容解析（包装为单一 assetType）
- [x] 1.4 加 config 单测：id 全局唯一、宽高>0、每个 size 含 producible 字段、跨渠道同名尺寸不冲突

## 2. 裁剪服务按 id 寻址
- [x] 2.1 `crop.Service` 维护扁平化 `map[id]Size`（含所属 channel/assetType 反查）
- [x] 2.2 `resolveSizes(names)` → `resolveSizeIDs(ids []string)`，按 id 查找
- [x] 2.3 `CropToSizes` 入参改为 `sizeIDs`；命中 `producible=false` 返回明确错误（不静默跳过）
- [x] 2.4 `CropResult` 增 `SizeID`、`ChannelID`、`Bytes`（实际字节数，用于超限提示）
- [x] 2.5 `Service.Platforms()` → 返回三层目录（或新增 `Channels()`）；过滤逻辑保留 producible 标记供前端置灰
- [x] 2.6 更新 `crop_test.go`：表驱动覆盖 id 寻址、跨渠道同尺寸区分、不可裁剪报错、format 透传

## 3. Agent 工具接口升级
- [x] 3.1 `crop_to_sizes` 入参 `size_names` → `size_ids`，描述同步更新
- [x] 3.2 `list_platform_sizes` 返回三层结构（渠道→素材类型→尺寸含 id 与约束）
- [x] 3.3 `list_platform_sizes` 增可选 `channel` 过滤参数
- [x] 3.4 更新 `agent_test.go` 覆盖新工具入参/返回

## 4. HTTP 接口
- [x] 4.1 `GET /api/platforms` 返回三层目录结构（向后兼容地扩展返回体）
- [x] 4.2 更新 `crop/handler.go` 及相关契约测试

## 5. 前端分层选择器
- [x] 5.1 `loadPlatforms()` 适配三层目录数据结构
- [x] 5.2 重写尺寸选择器 UI：渠道搜索 + 速查分组 → 渠道内按素材类型分组胶囊 → 已选区跨渠道累加
- [x] 5.3 胶囊展示约束角标/tooltip（format/maxKB/note）；`producible=false` 置灰不可选
- [x] 5.4 `cropToSizes()` 改为提交 `size_ids`
- [x] 5.5 `styles.css` 增分层选择器样式（保持科技感/过渡，移动端可滚动）

## 6. 验证与收尾
- [x] 6.1 `go build ./...` 通过
- [x] 6.2 `go test ./internal/config/ ./internal/crop/ ./internal/agent/` 全绿
- [x] 6.3 手动验证：打开选择器→选不同渠道尺寸→跨渠道多选→裁剪→工作区回填产物
- [x] 6.4 `openspec validate expand-platform-catalog --strict` 通过
