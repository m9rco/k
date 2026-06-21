#!/usr/bin/env bash
# 拉取文字叠加（overlay_text）所需的 CJK 主字体到 data/fonts/overlay-cjk.ttf。
#
# 为什么需要单独拉：字体本体 ~16MB，且与生成产物同属 /data/（已被 .gitignore
# 排除，不入库）。没有它时叠加仍可工作，但只支持 ASCII/Latin（内置 Go Bold 回退），
# 中文会"明确报错而非出豆腐块"。跑一次本脚本即可让中文 CTA / 定档大字可用。
#
# 字体：Noto Sans CJK SC Bold（思源黑体，SIL OFL 1.1，可商用）。
# 想换字体：设环境变量 OVERLAY_FONT 指向任意 CJK TTF/OTF 即可，无需本脚本。
#
# 幂等：已存在且体积正常则跳过；加 --force 强制重下。
set -euo pipefail

cd "$(dirname "$0")/.."

DEST="data/fonts/overlay-cjk.ttf"
URL="https://raw.githubusercontent.com/notofonts/noto-cjk/main/Sans/OTF/SimplifiedChinese/NotoSansCJKsc-Bold.otf"
MIN_BYTES=1000000 # 合理下限，挡住 404/截断的空文件

force=0
[ "${1:-}" = "--force" ] && force=1

if [ "$force" -eq 0 ] && [ -f "$DEST" ]; then
  size=$(wc -c < "$DEST" | tr -d ' ')
  if [ "$size" -ge "$MIN_BYTES" ]; then
    echo "✓ 字体已就位：$DEST （$size 字节），跳过。用 --force 重下。"
    exit 0
  fi
  echo "! $DEST 体积异常（$size 字节），重新下载…"
fi

mkdir -p "$(dirname "$DEST")"
echo "↓ 下载 Noto Sans CJK SC Bold（约 16MB，OFL 可商用）…"
tmp="$(mktemp)"
curl -fSL --max-time 300 "$URL" -o "$tmp"

size=$(wc -c < "$tmp" | tr -d ' ')
if [ "$size" -lt "$MIN_BYTES" ]; then
  rm -f "$tmp"
  echo "✗ 下载体积异常（$size 字节），可能是网络问题或源失效。" >&2
  exit 1
fi

mv "$tmp" "$DEST"
echo "✓ 完成：$DEST （$size 字节）。重启服务即可启用中文叠加。"
