---
name: smoke-test
description: 全栈可视化冒烟——构建最新前端并嵌入、在临时端口起隔离实例、检查 /healthz、用 Playwright 打开首页截图确认 UI 能渲染，最后清理。用于改动后快速确认「前后端都还能跑、界面没白屏」。
disable-model-invocation: true
---

# smoke-test

比 `run-server`（纯后端无头）更进一层：它会**先构建最新前端再 embed**，并用 Playwright **实际打开页面截图**，验证全栈链路（Go 二进制 + 嵌入的 React UI）能正常渲染，而不只是端点返回 200。用于改了前端或前后端交互后，一步确认「界面没白屏、关键端点正常」。

## 约定

- **端口用 `:18066`**（高位、隔离），**绝不占用用户的 8080**（见项目 memory）。被占用就换 18067 等。
- 全程跑在**临时 DB / 资产目录**,不碰真实的 `data/`。
- 这两个被检端点不依赖外部 AI key；UI 首屏渲染也不需要真实模型服务。

## 步骤

1. 构建最新前端（顺序很重要：先前端再 Go，否则 embed 的是旧 UI）：
   ```bash
   cd web && npm run build
   ```
   失败就停下，把 tsc/vite 错误报给用户，不要继续。

2. 清除 stale 哨兵并编译服务器到临时二进制：
   ```bash
   rm -f web/.embed-stale
   go build -o /tmp/gas-smoke-server ./cmd/server
   ```
   编译失败就停下并报告。

3. 用临时 DB / 资产目录 / 隔离端口后台启动，给约 1.5 秒起来：
   ```bash
   DB_PATH=/tmp/gas-smoke.db ASSET_DIR=/tmp/gas-smoke-assets ADDR=:18066 /tmp/gas-smoke-server &
   SRV=$!
   sleep 1.5
   ```

4. 后端健康检查：
   ```bash
   curl -s -o /dev/null -w "healthz: HTTP %{http_code}\n" http://localhost:18066/healthz
   ```
   期望 200。非 200 就跳到第 7 步清理并报告失败。

5. 用 Playwright MCP 打开首页并截图（验证 UI 真的渲染，不是白屏）：
   - `browser_navigate` → `http://localhost:18066/`
   - `browser_snapshot` 看可访问性树里有没有关键元素（如输入框 / 工作区容器）
   - `browser_take_screenshot` → 存到 `/tmp/gas-smoke.png`
   - `browser_console_messages`（level: error）确认控制台没有报错

6. 关掉浏览器页：`browser_close`。

7. 无论成功失败都清理（不要遗留进程/临时文件）：
   ```bash
   kill $SRV 2>/dev/null || true
   rm -f /tmp/gas-smoke.db* /tmp/gas-smoke-server /tmp/gas-smoke.png
   rm -rf /tmp/gas-smoke-assets
   ```

8. 用一两句话报告：前端是否构建通过、Go 是否编译通过、`/healthz` 状态、首页是否成功渲染（截图所见 + 控制台有无报错）。截图可在清理前先给用户看。

## 注意

- 只冒烟、不提交、不碰真实数据。
- 想只验证后端端点而不构建前端/不开浏览器，用更轻的 `run-server`。
- 想只出包不启动，用 `build-web`。
