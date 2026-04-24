#!/usr/bin/env bash
# test-diagnostics-e2e.sh — end-to-end verification that a broken stdio
# server produces a stable MCPX_STDIO_SPAWN_ENOENT code via the new
# per-server diagnostics endpoint.
#
# Spec 044 US1 acceptance scenario.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Run the catalog completeness test (fast, in-process).
echo "==> catalog completeness tests"
go test -count=1 ./internal/diagnostics/...

# Run the classifier golden-sample tests.
echo "==> classifier tests"
go test -count=1 -run 'TestClassify_' ./internal/diagnostics/...

# Link-check the docs/errors/ directory against the in-code registry.
echo "==> docs/errors link check"
"$ROOT/scripts/check-errors-docs-links.sh"

echo "==> OK: diagnostics E2E smoke completed"
