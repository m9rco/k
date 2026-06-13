---
name: build-web
description: 按正确顺序构建前端并嵌入 Go 二进制——先 cd web && npm run build 生成 web/static，再 go build，避免 go:embed 打包到过期 UI。任一步失败即停止并报告。
disable-model-invocation: true
---

# build-web

前端是 Vite/React，构建产物落在 `web/static`，由 Go 的 `go:embed all:static` 打进单二进制。**顺序很重要**：必须先 `npm run build` 再 `go build`，否则二进制嵌入的是上一次的旧 UI（本仓库踩过这个坑）。本 skill 把这条链固定成一步。

## 步骤

1. 构建前端（含 `tsc -b` 类型检查 + vite 打包）：
   ```bash
   cd web && npm run build
   ```
   失败就停下，把 tsc/vite 的错误报给用户，不要继续。

2. 清除 stale 哨兵（embed-stale 钩子用它提醒"web/static 已过期"）：
   ```bash
   rm -f web/.embed-stale
   ```

3. 构建 Go 二进制到临时路径（此时嵌入的是刚生成的新 `web/static`）：
   ```bash
   go build -o /tmp/gas-server ./cmd/server
   ```
   失败就停下，报告编译错误。

4. 用一两句话报告：前端是否构建通过、产物 hash（`web/static/assets` 下的 index-*.js/css）、Go 是否编译通过、二进制路径。

## 注意

- 这只构建，不启动服务器。要冒烟验证用 `run-server`。
- 不碰 `data/` 真实数据，不提交。
- 若只想验证类型而不出包，可单独 `cd web && npx tsc -b --noEmit`；但要让运行中的服务器看到改动，必须走完整 `npm run build`。
