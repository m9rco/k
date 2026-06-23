# Change: 图章模式参考图区按需展示宣发分析

## Why

宣发分析目前只在 chat 适配流程里以流式分析块出现——用户在图章模式必须点"生成"触发一次适配，才能在对话区看到分析。图章模式的参考图区域本身没有任何分析展示。

更具体的根因：上传预热只按**单图 md5** 缓存分析报告，**不预热多图组合**；而适配流程对多图组合用的是 `md5("group:"+有序 md5 拼接)` 这条复合 key。所以"两张参考图组合"的宣发分析只有真正触发过一次适配才会被算出来、缓存住——在图章模式里无处可见。

目标：在图章模式参考图区加一个**只读**宣发分析块，按当前选中的参考图（单图或多图组合，≤16）动态展示分析，切换选中时自动（防抖）重拉并回填。复用与适配流程**完全相同的缓存 key**，使两条路径双向共享缓存（图章模式算过的组合，适配能直接命中；反之亦然）。

## What Changes

- 抽取共享缓存 key 函数 `vision.CacheKey(md5s)` 作为单一真源：单图返回裸 md5（对齐上传预热），多图返回 `md5("group:"+有序拼接)`。适配流程（`agent.visionThemeReport`）与新端点都调它，杜绝 key 规则漂移。
- 新增只读 HTTP 端点 `POST /api/session/{id}/vision-report`：输入有序 `assetIds`，命中缓存秒回、未命中现场分析并回写同一 key；未注入返回 503，参数非法 400，分析失败 200 降级（`available:false`）。
- 复用既有"注入式闭包"模式（同 `SetPrewarm`/`SetDescribeRegion`），在 `cmd/server/main.go` 注入真实逻辑；vision 未配置时不注入。
- 图章模式参考图区新增只读宣发分析展示块：选中变化时自动+防抖（700ms）拉取；vision 未配置/分析失败时**隐藏不显示**。

## Impact

- Affected specs: `marketing-analysis`（新增按需只读端点 + 共享 key 不变式）、`stamp-album-ref-guidance`（新增图章模式自动宣发分析展示）
- Affected code:
  - `internal/vision/vision.go`（新增 `CacheKey`）
  - `internal/agent/tools.go`（`visionThemeReport` 改调 `vision.CacheKey`）
  - `internal/workspace/workspace.go`（新字段 + Setter + 路由 + handler）
  - `cmd/server/main.go`（注入 `SetVisionReport` 闭包）
  - `web/src/lib/api.ts`、新建 `web/src/components/workspace/report-block.tsx`、`web/src/components/workspace/stamp-album.tsx`
