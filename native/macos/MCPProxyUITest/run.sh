#!/usr/bin/env bash
# Launcher for the mcpproxy-ui-test MCP server.
# Builds the binary from source (kept in this project dir) if it's missing or
# stale, then execs it — so the MCP server self-heals after a clean checkout or
# a /tmp wipe instead of failing with ENOENT. Build output goes to stderr so it
# never corrupts the stdout JSON-RPC stream.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$DIR/Sources/main.swift"
BIN="$DIR/.bin/mcpproxy-ui-test"

mkdir -p "$DIR/.bin"
if [[ ! -x "$BIN" || "$SRC" -nt "$BIN" ]]; then
  echo "[run.sh] building mcpproxy-ui-test from source…" >&2
  SDK="$(xcrun --sdk macosx --show-sdk-path)"
  # Keep swiftc's module cache under .build/ (gitignored) so it never litters
  # the source dir with *.swiftmodule / hash-named cache directories.
  swiftc -target arm64-apple-macosx13.0 -sdk "$SDK" -O \
    -module-cache-path "$DIR/.build/ModuleCache" \
    -o "$BIN" "$SRC" >&2
fi

exec "$BIN" "$@"
