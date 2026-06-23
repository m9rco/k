## 1. 共享缓存 key（防漂移）
- [x] 1.1 `internal/vision` 新增导出纯函数 `CacheKey(md5s []string) string`：单图返回 `md5s[0]`，多图返回 `md5("group:"+有序逗号拼接)`
- [x] 1.2 `internal/vision/cachekey_test.go`：单图特例锁、多图顺序敏感、纯函数稳定
- [x] 1.3 `internal/agent/tools.go` `visionThemeReport` 改调 `vision.CacheKey`，删除内联 key 逻辑

## 2. workspace 只读端点
- [x] 2.1 `Service` 新增 `visionReport` 闭包字段 + `SetVisionReport` setter
- [x] 2.2 `RegisterRoutes` 加 `POST /api/session/{id}/vision-report`
- [x] 2.3 `handleVisionReport`：503（未注入）/ 400（空或 >16）/ 200 成功 `{available:true,report,count}` / 200 降级 `{available:false,error}`
- [x] 2.4 `internal/workspace/workspace_test.go`：未注入 503、空/超限 400、成功、降级四个用例
- [x] 2.5 编辑回写：`PUT /api/session/{id}/vision-report` + `SetSaveVisionReport`，写回同一 key（含 503/400/200 测试）
- [x] 2.6 重新分析：POST 增加 `force` 字段，命中缓存时绕过、现场重跑

## 3. main.go 注入闭包
- [x] 3.1 紧挨 `SetDescribeRegion` 注入 `SetVisionReport`/`SetSaveVisionReport`，门控同 describeRegion
- [x] 3.2 闭包：按序读字节+md5（读不到跳过）→ `vision.CacheKey` → 查缓存命中即回（force 跳过）→ 未命中 Analyze（onChunk=nil）→ 回写缓存；抽出共享 `visionGroupKey`

## 4. 前端
- [x] 4.1 `web/src/lib/api.ts` 新增 `visionReport(sid, assetIds, force)` + `saveVisionReport` + 类型
- [x] 4.2 新建 `web/src/components/workspace/report-block.tsx`（只读折叠 + 编辑 textarea + 重新分析/取消/保存）
- [x] 4.3 `stamp-album.tsx`：防抖 effect（700ms）+ 三态 + 竞态保护；挂载于 `RatioFamilyStamps` 之后；未配置/失败/空选隐藏；接 onSave/onReanalyze

## 5. 验证
- [x] 5.1 `go test ./internal/...` 全绿 + `go vet`
- [x] 5.2 手测（playwright）：单图命中预热缓存秒回、两图组合首次分析后缓存、重新分析现场重跑、编辑保存回写跨刷新保留、chat 选中数刷新不丢失

## 6. 关联修复（chat 选中持久化）
- [x] 6.1 `controller.ts` 持久化 `state.selected` 到 sessionStorage（`gas.selected`），boot 后按现存 asset 过滤恢复，`restoredRef` 门控避免初始空渲染覆盖

