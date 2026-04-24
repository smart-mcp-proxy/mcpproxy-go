#!/usr/bin/env bash
# check-errors-docs-links.sh — verify every registered diagnostic code has a
# docs/errors/<CODE>.md file, and every file under docs/errors/ corresponds
# to a registered code.
#
# Spec 044 FR-017.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOCS="$ROOT/docs/errors"
CODES_GO="$ROOT/internal/diagnostics/codes.go"

if [ ! -d "$DOCS" ]; then
  echo "ERROR: $DOCS does not exist" >&2
  exit 1
fi

REGISTERED=$(grep -oE '"MCPX_[A-Z0-9_]+"' "$CODES_GO" | tr -d '"' | sort -u)

fail=0

# Every registered code must have a file.
for code in $REGISTERED; do
  if [ ! -f "$DOCS/$code.md" ]; then
    echo "MISSING: docs/errors/$code.md" >&2
    fail=1
  fi
done

# Every file must correspond to a registered code.
for f in "$DOCS"/MCPX_*.md; do
  [ -e "$f" ] || continue
  base=$(basename "$f" .md)
  if ! printf '%s\n' "$REGISTERED" | grep -qx "$base"; then
    echo "ORPHAN: $f (no registered code $base)" >&2
    fail=1
  fi
done

if [ "$fail" -ne 0 ]; then
  echo "check-errors-docs-links: FAIL" >&2
  exit 1
fi

echo "check-errors-docs-links: OK ($(echo "$REGISTERED" | wc -l | tr -d ' ') codes, all linked)"
