#!/usr/bin/env bash
# PostToolUse hook: warn when frontend source is edited so the embedded bundle
# in web/static is now stale. The Go binary serves web/static via go:embed, so
# editing web/src without running `cd web && npm run build` before `go build`
# ships a STALE UI (a real footgun in this repo). We only print a reminder; we
# never rebuild or block. Fires once per "stale streak" using a sentinel file.
set -euo pipefail

payload="$(cat)"

if command -v jq >/dev/null 2>&1; then
  file="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty')"
else
  file="$(printf '%s' "$payload" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null || true)"
fi

[ -n "$file" ] || exit 0
case "$file" in
  */web/src/*|web/src/*) ;;
  *) exit 0 ;;
esac

sentinel="$CLAUDE_PROJECT_DIR/web/.embed-stale"
# Only emit the reminder once until a build clears the sentinel, to avoid noise
# on every edit in a multi-file change.
if [ ! -f "$sentinel" ]; then
  touch "$sentinel" 2>/dev/null || true
  echo "Reminder: web/src changed — web/static (go:embed bundle) is now stale. Run 'cd web && npm run build' before 'go build' to avoid serving an old UI. (Or use /build-web.)" >&2
fi
exit 0
