#!/usr/bin/env bash
# Pre-commit hook: refuse to commit compiled native executables (Mach-O, ELF,
# PE). `go build ./bench/cmd/warmtiktoken` run from the repo root drops a
# 43 MB binary at ./warmtiktoken, and one of those made it into #1153.
set -euo pipefail
rc=0
for f in "$@"; do
  [ -f "$f" ] || continue
  if file -b "$f" | grep -qE '^(Mach-O|ELF|PE32|MS-DOS executable)'; then
    echo "refusing to commit native executable: $f (add it to .gitignore)" >&2
    rc=1
  fi
done
exit $rc
