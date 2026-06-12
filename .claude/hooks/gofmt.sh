#!/usr/bin/env bash
# PostToolUse hook: run gofmt on Go files after Claude edits them.
#
# Receives the tool-call payload as JSON on stdin; we extract the edited file
# path and, if it is a .go file that exists, format it in place. Any failure is
# non-fatal (exit 0) so formatting never blocks the edit.
set -euo pipefail

payload="$(cat)"

# Pull the file path from the tool input. Prefer jq; fall back to python3.
if command -v jq >/dev/null 2>&1; then
  file="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty')"
else
  file="$(printf '%s' "$payload" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null || true)"
fi

[ -n "$file" ] || exit 0
[[ "$file" == *.go ]] || exit 0
[ -f "$file" ] || exit 0

gofmt -w "$file" 2>/dev/null || true
exit 0
