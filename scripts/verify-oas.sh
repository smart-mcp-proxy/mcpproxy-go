#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

SWAGGER_OUT="${SWAGGER_OUT:-oas}"
SWAGGER_BIN="${SWAGGER_BIN:-$HOME/go/bin/swag}"
SWAGGER_ENTRY="${SWAGGER_ENTRY:-cmd/mcpproxy/main.go}"

cd "${REPO_ROOT}"

echo "📚 Regenerating OpenAPI artifacts..."
make swagger SWAGGER_OUT="${SWAGGER_OUT}" SWAGGER_BIN="${SWAGGER_BIN}" SWAGGER_ENTRY="${SWAGGER_ENTRY}"

echo "🔎 Checking for uncommitted OpenAPI changes..."
status="$(git status --porcelain -- "${SWAGGER_OUT}/swagger.yaml" "${SWAGGER_OUT}/docs.go" || true)"

if [[ -n "${status}" ]]; then
  echo "❌ OpenAPI artifacts are out of date. Run 'make swagger' and commit the regenerated files."
  echo "${status}"
  git diff --stat -- "${SWAGGER_OUT}/swagger.yaml" "${SWAGGER_OUT}/docs.go" || true
  exit 1
fi

echo "✅ OpenAPI artifacts are up to date."
