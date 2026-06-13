#!/usr/bin/env bash
# PostToolUse hook: type-check the frontend after Claude edits a TS/TSX file
# under web/src. Mirrors gofmt.sh — extract the edited path from the tool-call
# JSON on stdin, and if it is a web/src TypeScript file, run `tsc --noEmit` so
# type errors (TDZ ordering, bad props, unused imports) surface immediately
# instead of only at `npm run build`. Non-fatal: never blocks the edit.
set -euo pipefail

payload="$(cat)"

# Pull the file path from the tool input. Prefer jq; fall back to python3.
if command -v jq >/dev/null 2>&1; then
  file="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty')"
else
  file="$(printf '%s' "$payload" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null || true)"
fi

[ -n "$file" ] || exit 0
# Only care about TypeScript sources under the frontend.
case "$file" in
  */web/src/*.ts|*/web/src/*.tsx|web/src/*.ts|web/src/*.tsx) ;;
  *) exit 0 ;;
esac

web_dir="$CLAUDE_PROJECT_DIR/web"
[ -d "$web_dir/node_modules" ] || exit 0  # deps not installed; skip silently

# Run the project's own type-check (no emit). Surface diagnostics on stderr so
# Claude sees them, but exit 0 so formatting/edit flow is never blocked.
out="$(cd "$web_dir" && npx tsc -b --noEmit 2>&1)" || {
  echo "tsc reported type errors after editing $file:" >&2
  printf '%s\n' "$out" | head -40 >&2
  exit 0
}
exit 0
