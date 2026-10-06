#!/usr/bin/env bash
# check-no-unquarantine-callers.sh - Spec 109 SC-005 (T147): no first-party
# surface calls POST /servers/{id}/unquarantine, and the one-click approvals
# 109-f removed stay removed.
#
#   1. No non-test source under frontend/src, native/macos/MCPProxy/MCPProxy,
#      cmd/mcpproxy, cmd/mcpproxy-tray or internal/tray references the
#      `/unquarantine` path. Test files are excluded: they assert the path is
#      never requested. Whole-line comments are excluded: they explain why the
#      call is gone and are not callers.
#   2. No `approveTools(` call in Views/HomeView.swift, Views/DashboardView.swift
#      (if present) or Views/ServersView.swift.
#
# Usage: scripts/check-no-unquarantine-callers.sh [ROOT]
# ROOT defaults to the repository root. Exit 0 = clean, 1 = a hit (printed).
set -uo pipefail

ROOT="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
cd "$ROOT" || { echo "cannot enter $ROOT" >&2; exit 2; }

status=0

dirs=()
for d in frontend/src native/macos/MCPProxy/MCPProxy cmd/mcpproxy cmd/mcpproxy-tray internal/tray; do
  [[ -d "$d" ]] && dirs+=("$d")
done

if [[ ${#dirs[@]} -gt 0 ]]; then
  hits="$(grep -rnE '/unquarantine' "${dirs[@]}" \
    --exclude='*_test.go' --exclude='*.spec.ts' --exclude='*.test.ts' \
    --exclude-dir=tests --exclude-dir=__tests__ --exclude-dir=MCPProxyTests \
    --exclude-dir=node_modules 2>/dev/null \
    | grep -vE '^[^:]+:[0-9]+:[[:space:]]*(//|\*|/\*|#)' || true)"
  if [[ -n "$hits" ]]; then
    echo "SC-005: first-party callers of /unquarantine found (the review location replaces them):"
    echo "$hits"
    status=1
  fi
fi

for view in HomeView DashboardView ServersView; do
  f="native/macos/MCPProxy/MCPProxy/Views/$view.swift"
  [[ -f "$f" ]] || continue
  hits="$(grep -nE 'approveTools\(' "$f" || true)"
  if [[ -n "$hits" ]]; then
    echo "SC-005: one-click approveTools( call in $f:"
    echo "$hits"
    status=1
  fi
done

if [[ $status -eq 0 ]]; then
  echo "SC-005: no first-party /unquarantine callers"
fi
exit $status
