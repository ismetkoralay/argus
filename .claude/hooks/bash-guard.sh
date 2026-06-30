#!/usr/bin/env bash
# PreToolUse(Bash): veto obviously destructive commands.
# Exit 2 => deny the tool call (Claude sees stderr). Exit 0 => allow.
set -euo pipefail
input="$(cat)"
cmd="$(printf '%s' "$input" | jq -r '.tool_input.command // empty')"
[ -z "${cmd:-}" ] && exit 0
if printf '%s' "$cmd" | grep -Eq 'rm[[:space:]]+-rf[[:space:]]+/|:\(\)\s*\{|mkfs|dd[[:space:]]+if=|>[[:space:]]*/dev/sd'; then
  echo "bash-guard: blocked a potentially destructive command." >&2
  exit 2
fi
exit 0
