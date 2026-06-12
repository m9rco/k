#!/usr/bin/env bash
# PreToolUse hook: block writes to secret-bearing files (.env and friends).
#
# Receives the tool-call payload as JSON on stdin. If the target path looks like
# an env/secret file, we deny the call by exiting 2 with a reason on stderr; the
# .env.example template is explicitly allowed. Any other path passes through.
set -euo pipefail

payload="$(cat)"

if command -v jq >/dev/null 2>&1; then
  file="$(printf '%s' "$payload" | jq -r '.tool_input.file_path // empty')"
else
  file="$(printf '%s' "$payload" | python3 -c 'import sys,json; print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null || true)"
fi

[ -n "$file" ] || exit 0

base="$(basename "$file")"

# Allow the committed template.
if [ "$base" = ".env.example" ]; then
  exit 0
fi

# Block .env, .env.local, .env.* and *.env files.
case "$base" in
  .env|.env.*|*.env)
    echo "Blocked: $file looks like a secret-bearing env file. Edit .env manually outside Claude, or use .env.example for templates." >&2
    exit 2
    ;;
esac

exit 0
