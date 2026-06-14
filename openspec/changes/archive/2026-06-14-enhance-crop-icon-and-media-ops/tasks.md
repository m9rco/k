## 1. 裁剪模式（后端）
- [x] 1.1 `internal/crop/crop.go`：引入 `Mode` 与参数（anchor 方位、rect 归一化区域），实现 `cover`/`contain`/`anchor`/`rect`，`CropBytes` 增加模式入参；`contain` 支持背景色填充（透明/JPEG 退白）
- [x] 1.2 `internal/crop/service.go`：`CropToSizes` 透传模式参数到 `CropBytes`
- [x] 1.3 `internal/crop/handler.go`：`cropRequest` 增加可选 `mode`/`anchor`/`rect` 字段，校验非法参数并报错，缺省 `cover`
- [x] 1.4 `internal/agent/tools.go`：`crop_to_sizes` 工具增加 `mode`/`anchor` 参数（不暴露 `rect`），描述说明各模式
- [x] 1.5 `internal/crop/crop_test.go`：补四种模式的表驱动单测（含 contain 留白、anchor 方位、rect 区域、非法参数）

## 2. 图生 Icon（后端）
- [x] 2.1 `internal/generation/prompt.go`：新增 `EditKind = generate_icon` 与 icon 提示模板（提炼主体/居中留边/小尺寸辨识度/底色）
- [x] 2.2 `internal/generation` 服务：支持目标 icon 尺寸 slot（默认 150×150），生成后经 `crop` 收敛到目标尺寸（默认 contain 不裁主体）
- [x] 2.3 `internal/agent/tools.go`：新增 `generate_icon` 工具（参数：source_asset_id、可选 desc、可选 size），纳入 `Tools()` 与 `AsyncTaskTools()`
- [x] 2.4 单测：icon 模板组装、注入防护、默认尺寸、尺寸收敛

## 3. 视频处理（前端 ffmpeg.wasm）
- [ ] 3.1 引入 `@ffmpeg/ffmpeg`(wasm) 依赖，按需懒加载封装一个 `lib/video-ffmpeg.ts`（裁剪片段、抽帧）
- [ ] 3.2 新增视频处理弹窗组件（输入起止时间/时间点、进度态、超限与非法输入提示）
- [ ] 3.3 复用上传/资产接口将产物（视频片段/抽帧图片）回填工作区
- [ ] 3.4 `asset-card.tsx` 视频右键菜单/放大面板增加"裁剪片段""抽帧"入口
- [ ] 3.5 设定并提示可处理时长/体积上限

## 4. 对话内同步产物即时回填（BUG）
- [ ] 4.1 `web/src/store/controller.ts`：`onToolResult` 中，当工具为产出资产的同步工具（如 `crop_to_sizes`）时主动 `refreshWorkspace`
- [ ] 4.2 验证：对话内切图后工作区即时出现产物，无需刷新页面

## 5. 裁剪弹窗前端体验
- [ ] 5.1 `size-picker.tsx`：左侧渠道栏加 `max-height` + 独立纵向滚动
- [ ] 5.2 `size-picker.tsx`：加裁剪模式选择 UI（cover/contain/anchor/rect），anchor 九宫格方位选择，rect 源图拖拽框选
- [ ] 5.3 `web/src/lib/api.ts`：`crop` 请求带上 `mode`/`anchor`/`rect`
- [ ] 5.4 新增/扩展 icon 生成入口（右键菜单或放大面板，含可选尺寸输入）

## 6. 角标配色
- [ ] 6.1 `asset-card.tsx`：编号角标按媒体类型取色（图=accent，视频=第二强调色），遵循克制调色板
- [ ] 6.2 视觉核对：图/视频角标在浅色/深色模式下均可辨且不刺眼

## 7. 验证
- [ ] 7.1 后端 `go test ./internal/crop/... ./internal/generation/... ./internal/agent/...` 通过
- [ ] 7.2 前端 `tsc` 与 build 通过，产物 embed
- [ ] 7.3 端到端手测：四种裁剪模式、图生 icon（默认/指定尺寸）、视频裁剪+抽帧、对话切图即时回填、角标配色
- [ ] 7.4 `openspec validate enhance-crop-icon-and-media-ops --strict` 通过
