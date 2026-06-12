---
name: run-server
description: 在临时端口启动 game-asset 服务器并冒烟测试关键端点（/healthz 与 /api/platforms），用完自动清理临时 DB 与资产目录。
disable-model-invocation: true
---

# run-server

在隔离的临时环境里启动 game-asset 服务器，验证它能正常起来并对外提供关键端点，然后清理。用于改动后快速确认「还能跑」。

## 步骤

1. 编译服务器到临时二进制：
   ```bash
   go build -o /tmp/gas-server ./cmd/server
   ```
   编译失败就停下，把错误报给用户。

2. 用临时 DB / 资产目录 / 非占用端口启动（后台），给它约 1.5 秒起来：
   ```bash
   DB_PATH=/tmp/gas-smoke.db ASSET_DIR=/tmp/gas-assets ADDR=:18099 /tmp/gas-server &
   SRV=$!
   sleep 1.5
   ```

3. 冒烟测试两个关键端点：
   ```bash
   curl -s -o /dev/null -w "healthz: HTTP %{http_code}\n" http://localhost:18099/healthz
   curl -s http://localhost:18099/api/platforms \
     | python3 -c "import json,sys; d=json.load(sys.stdin); print('channels:', len(d.get('channels',[])))"
   ```
   期望：`/healthz` 返回 200；`/api/platforms` 返回非空 channels 数组。

4. 无论成功失败，都清理：
   ```bash
   kill $SRV 2>/dev/null || true
   rm -f /tmp/gas-smoke.db* /tmp/gas-server
   rm -rf /tmp/gas-assets
   ```

5. 用一两句话报告结果：编译是否通过、两个端点的状态、渠道数。不要保留任何临时进程或文件。

## 注意

- 这是只读冒烟测试，不碰真实的 `data/` 目录和真实 DB。
- 端口 `:18099` 若被占用，换一个高位端口（如 18100）。
- 不需要真实 API key——这两个端点不依赖外部模型服务。
