#!/usr/bin/env bash
# gen-errors-docs.sh — generate a stub docs/errors/<CODE>.md for every
# registered diagnostic code that does not yet have a file. Runs locally
# and in CI as a linter (no-op when all files exist).
#
# Spec 044.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DOCS="$ROOT/docs/errors"
mkdir -p "$DOCS"

# Extract every registered code constant from codes.go. The values are
# the stable identifiers we need.
CODES=$(grep -oE '"MCPX_[A-Z0-9_]+"' "$ROOT/internal/diagnostics/codes.go" | tr -d '"' | sort -u)

for code in $CODES; do
  f="$DOCS/$code.md"
  if [ -f "$f" ]; then
    continue
  fi
  cat > "$f" <<EOF
# $code

**Severity**: see \`internal/diagnostics/registry.go\` for the authoritative severity.
**Registered in**: [\`internal/diagnostics/registry.go\`](../../internal/diagnostics/registry.go)

## What happened

mcpproxy classified a terminal failure as \`$code\`. This page is a stub
and will be expanded with cause, symptoms, and remediation guidance.

## How to fix

See the fix steps emitted by the CLI and web UI:

\`\`\`bash
mcpproxy doctor --server <name>
mcpproxy doctor fix $code --server <name>    # dry-run by default for destructive fixes
\`\`\`

## Related

- [Spec 044 — Diagnostics & Error Taxonomy](../../specs/044-diagnostics-taxonomy/spec.md)
- [Design doc](../superpowers/specs/2026-04-24-diagnostics-error-taxonomy-design.md)
EOF
  echo "generated $f"
done
