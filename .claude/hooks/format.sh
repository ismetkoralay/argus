#!/usr/bin/env bash
# PostToolUse: auto-format the file Claude just wrote/edited.
# Requires: jq, gofmt (and optionally goimports).
set -euo pipefail
input="$(cat)"
file="$(printf '%s' "$input" | jq -r '.tool_input.file_path // .tool_input.path // empty')"
[ -z "${file:-}" ] && exit 0
case "$file" in
  *.go)
    gofmt -w "$file" || true
    command -v goimports >/dev/null 2>&1 && goimports -w "$file" || true
    ;;
esac
exit 0
